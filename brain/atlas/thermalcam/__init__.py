"""Câmera térmica: roda as redes do Atlas-MachineL (ONNX) e devolve o filme de temperatura previsto.

As redes vêm de stars-beyond/Atlas-MachineL, exportadas por ml/export_thermal.py para
atlas/thermalcam/models/. (Fica num subpacote para não entrar na impressão digital da ingestão, que
cobre só os .py de atlas/: mexer aqui não refaz o acervo.) Cada uma recebe 4 quadros, a geometria e as condições (O2, pressão, fluxo) e
devolve o próximo quadro; o filme é o rollout autorregressivo, e a previsão é a média das redes.

Roda com o onnxruntime que o fastembed já instala: não há torch nesta imagem.

A detecção da frente, a velocidade e o critério de "parou" são as mesmas contas do repositório do
modelo (atlas_ml/thermal.py), copiadas aqui para a tabela (ml/spread_table.py) e o filme darem o mesmo
número.

Limites, que a API repete na resposta: um material (filme de PMMA de 0,075 mm), 0 g, chama contra o
vento; a rede quase nunca apaga a chama, então o filme diz como ela anda, não se ela para.
"""
import base64
import json
import os
import threading
import zlib
from concurrent.futures import ThreadPoolExecutor
from functools import lru_cache

import numpy as np

PASTA = os.path.join(os.path.dirname(os.path.abspath(__file__)), "models")
T_MAX = 640.0        # °C no topo da escala dos quadros (uint8: 2,5 °C por nível)
STD_MAX = 127.5      # °C no topo da escala do desvio entre as redes (0,5 °C por nível)
PASSOS_MAX = 80
ESPERA_MAX = 50.0    # s na fila por um filme, abaixo dos 60 s em que a API em Go desiste da chamada
_trava = threading.Lock()   # um filme por vez: cada um já usa um núcleo por rede


class Ocupado(Exception):
    """A fila de filmes não andou em ESPERA_MAX segundos; o servidor responde 503."""


def disponivel():
    return os.path.exists(os.path.join(PASTA, "meta.json"))


@lru_cache(maxsize=1)
def _carregar():
    import onnxruntime as ort
    meta = json.load(open(os.path.join(PASTA, "meta.json"), encoding="utf-8"))
    # Uma thread por rede, sem espera ativa, e as redes em paralelo (ver _rollout): com o padrão do
    # onnxruntime, 8 sessões de 10 threads disputam os núcleos e o filme leva minutos num contêiner.
    op = ort.SessionOptions()
    op.intra_op_num_threads, op.inter_op_num_threads = 1, 1
    op.add_session_config_entry("session.intra_op.allow_spinning", "0")
    nomes = meta["nets"]
    n = int(os.environ.get("THERMAL_NETS", "0") or 0)       # servidor pequeno: usar menos redes (2 a 8)
    if 2 <= n < len(nomes):
        nomes = nomes[::max(1, len(nomes) // n)][:n]
    redes = [ort.InferenceSession(os.path.join(PASTA, x), op, providers=["CPUExecutionProvider"]) for x in nomes]
    d = np.load(os.path.join(PASTA, "sample.npz"))
    frames, geo = d["frames"].astype(np.float32), d["geo"].astype(np.float32)
    cont = geo.copy()                                  # amostra contínua: as barreiras viram PMMA
    metal = np.where(geo[1].any(0))[0]
    if len(metal):
        a, b = metal.min(), metal.max()
        cont[1] = 0
        cont[0][:, a:b + 1] = geo[0][:, [b + 1]]
    return meta, redes, frames, {"original": geo, "continuous": cont}


# ---------------------------------------------------------------- frente (como em atlas_ml/thermal.py)

def _linhas(forma, geo):
    h = forma[0]
    if geo[2].any() and geo[0].any():
        ys = np.where(geo[0].any(1))[0]
        a, b = ys.min(), ys.max() + 1
    else:
        a, b = 8, h - 8
    q = (b - a) // 4
    return slice(a + q, b - q)


def _frente(frames, geo, limiar):
    linhas = _linhas(frames.shape[1:], geo)
    out = []
    for q in frames:
        faixa = q[linhas] > limiar
        tem = faixa.any(1)
        out.append(float(np.median(faixa[tem].argmax(1))) if tem.mean() >= 0.3 else np.nan)
    return np.array(out)


def _fim(geo, margem=8):
    if geo[2].any() and geo[0].any():
        return int(np.where(geo[0].any(0))[0].min())
    return margem


def _acumulada(fr):
    out, m = np.full(len(fr), np.nan), np.inf
    for i, v in enumerate(fr):
        if np.isfinite(v):
            m = min(m, v)
        if np.isfinite(m):
            out[i] = m
    return out


def _velocidade(fr, fim):
    fr = _acumulada(fr)
    chegou = np.where(fr <= fim + 3)[0]
    if len(chegou):
        fr = fr[:chegou[0] + 1]
    t = np.where(np.isfinite(fr))[0]
    if len(t) < 5:
        return float("nan")
    return float(-np.polyfit(t, fr[t], 1)[0])


def _parou(fr, fim, ultimos=15, tol=2.0):
    cauda = _acumulada(fr)[-ultimos:]
    if not np.isfinite(cauda).any():
        return True
    cauda = cauda[np.isfinite(cauda)]
    return bool(cauda.max() - cauda.min() < tol and cauda[-1] > fim + 5)


# ---------------------------------------------------------------- rollout

def _b64(a, topo):
    q = np.clip(np.rint(a / topo * 255), 0, 255).astype(np.uint8)
    return base64.b64encode(zlib.compress(q.tobytes(), 6)).decode("ascii")


def _num(v, casas=3):
    return round(float(v), casas) if np.isfinite(v) else None


@lru_cache(maxsize=48)
def _rollout(o2, kpa, fluxo, passos, geometria, medido):
    meta, redes, frames, geos = _carregar()
    k, mm, fps = meta["k"], meta["mm_px"], meta["fps"]
    geo = geos[geometria]
    hist_c = frames[:k]
    H, W = hist_c.shape[1:]
    ph, pw = (-H) % 4, (-W) % 4
    h0 = np.pad((hist_c - meta["t_ref"]) / meta["t_scale"], ((0, 0), (0, ph), (0, pw)), mode="edge")[None].astype(np.float32)
    g = np.pad(geo, ((0, 0), (0, ph), (0, pw)), mode="edge")[None].astype(np.float32)
    c = (np.array([o2, kpa, fluxo], np.float32) / np.array(meta["cond_scale"], np.float32))[None]
    fim = _fim(geo)
    limiar = meta["front_threshold_c"]

    def uma(s):
        h, sai = h0, []
        for _ in range(passos):
            y = s.run(None, {"hist": h, "geo": g, "cond": c})[0]      # o onnxruntime solta o GIL aqui
            sai.append(y[0, 0, :H, :W])
            h = np.concatenate([h[:, 1:], y], 1)
        return np.stack(sai) * meta["t_scale"] + meta["t_ref"]

    # Um filme por vez. Quem espera mais que ESPERA_MAX desiste: a API em Go corta a chamada em 60 s, e uma thread
    # parada aqui é uma thread a menos para /embed e /health, que rodam no mesmo servidor.
    if not _trava.acquire(timeout=ESPERA_MAX):
        raise Ocupado()
    try:
        with ThreadPoolExecutor(max_workers=min(len(redes), os.cpu_count() or 1)) as ex:
            por_rede = list(ex.map(uma, redes))
    finally:
        _trava.release()
    v_rede = [_velocidade(_frente(np.concatenate([hist_c, p]), geo, limiar), fim) * mm * fps for p in por_rede]
    p = np.stack(por_rede)
    media, desvio = p.mean(0), p.std(0)
    # o desvio vai na metade da resolução: é um mapa de "onde as redes hesitam", e ruidoso demais para comprimir inteiro
    dh, dw = H // 2, W // 2
    desvio2 = desvio[:, :dh * 2, :dw * 2].reshape(passos, dh, 2, dw, 2).mean((2, 4))
    fr = _frente(np.concatenate([hist_c, media]), geo, limiar)
    v_rede = np.array(v_rede)
    out = {
        "w": int(W), "h": int(H), "steps": int(passos), "history": int(k), "fps": fps, "mm_px": mm,
        "t_max_c": T_MAX, "std_max_c": STD_MAX, "encoding": "uint8, row-major, zlib, base64",
        "o2_pct": o2, "pressure_kpa": kpa, "flow_cm_s": fluxo, "geometry": geometria,
        "history_b64": _b64(hist_c, T_MAX), "frames_b64": _b64(media, T_MAX), "std_b64": _b64(desvio2, STD_MAX), "std_w": int(dw), "std_h": int(dh),
        "pmma_b64": _b64(geo[0] * 255, 255), "metal_b64": _b64(geo[1] * 255, 255),
        "front_px": [_num(v, 1) for v in fr], "fuel_end_px": fim,
        "v_mm_s": _num(_velocidade(fr, fim) * mm * fps),
        "nets_mm_s": [_num(np.nanmin(v_rede)), _num(np.nanmax(v_rede))] if np.isfinite(v_rede).any() else None,
        "stalls": _parou(fr, fim), "nets": len(redes),
        "std_mean_c": [_num(x, 1) for x in desvio.mean((1, 2))],
        "model": meta["model"], "license": meta["license"], "attribution": meta["attribution"],
    }
    if medido:
        real = frames[k:k + passos]
        n = len(real)
        fr_real = _frente(frames[:k + n], geo, limiar)
        out.update({
            "measured_b64": _b64(real, T_MAX), "measured_steps": int(n),
            "measured_front_px": [_num(v, 1) for v in fr_real],
            "measured_v_mm_s": _num(_velocidade(fr_real, fim) * mm * fps),
            "mae_c": [_num(x, 1) for x in np.abs(media[:n] - real).mean((1, 2))],
            "sample": meta["sample"],
        })
    return out


def rollout(o2_pct=None, pressure_kpa=None, flow_cm_s=None, steps=63, sample=False):
    """Filme previsto. sample=True: o ensaio de exemplo, com a geometria dele e os quadros medidos."""
    meta = _carregar()[0]
    if sample:
        s = meta["sample"]
        o2_pct, pressure_kpa, flow_cm_s = s["o2_pct"], s["pressure_kpa"], s["flow_cm_s"]
        steps = min(int(steps), s["frames"] - meta["k"])
    passos = max(8, min(PASSOS_MAX, int(steps)))
    # arredonda para o cache: passos menores que isso não mudam o filme de forma visível
    return _rollout(round(float(o2_pct), 1), round(float(pressure_kpa), 0), round(float(flow_cm_s), 1), passos,
                    "original" if sample else "continuous", bool(sample))
