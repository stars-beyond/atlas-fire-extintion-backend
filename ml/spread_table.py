"""Tabela de velocidade de propagação prevista pelo Atlas-MachineL (etapa 6, térmico 2D do ZARM).

Roda as redes do repositório stars-beyond/Atlas-MachineL numa grade de O2 x pressão x fluxo, sobre uma
amostra contínua de filme de PMMA, e grava data/seed/sim/spread.json. A API Go só lê esse arquivo e
interpola (POST /api/predict/spread): nenhuma rede roda no servidor.

A amostra: os 4 primeiros quadros do ensaio de exemplo do repositório (zarm18479713:147, chama já acesa
na borda direita), com as barreiras de metal trocadas por PMMA na máscara de geometria. A velocidade é a
da frente térmica (250 °C) ao longo do rollout, medida pelo mesmo código do repositório (thermal.py).

O arquivo também guarda a conferência contra os ensaios contínuos medidos (registry.csv) e a faixa das
condições de treino, inclusive a pressão parcial de O2, porque no ZARM O2 e pressão variam juntos.

Uso (precisa de numpy e torch; na máquina de treino):
  git clone https://github.com/stars-beyond/Atlas-MachineL
  python ml/spread_table.py --repo ../Atlas-MachineL
  python ml/spread_table.py --repo ../Atlas-MachineL --device mps --lote 32
"""
import argparse
import csv
import datetime
import glob
import json
import os
import subprocess
import sys

import numpy as np
import torch

AQUI = os.path.dirname(os.path.abspath(__file__))
SAIDA = os.path.join(AQUI, "..", "data", "seed", "sim", "spread.json")   # a pasta que a API já monta

# Grade: a faixa de treino (O2 21-36 %, 60-103 kPa, 8,75-20 cm/s) com uma margem pequena, para a API
# poder mostrar o valor com o aviso de extrapolação logo fora dela (o rover fica em 56,5 kPa). A margem
# é curta de propósito: longe dos ensaios o modelo não apaga a chama e a frente da média do conjunto
# fica instável (numa grade que ia a 50 kPa e 6 cm/s, um canto deu 3,5 mm/s entre vizinhos de 7,5).
O2 = [20, 22, 24, 27, 30, 33, 36, 38]
KPA = [54, 62, 70, 78, 86, 94, 102, 110]
FLUXO = [8, 10, 14, 18, 22]
PASSOS = 63
EXEMPLO = "zarm18479713_147.npz"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--repo", required=True, help="pasta do checkout do Atlas-MachineL")
    ap.add_argument("--registry", default=os.path.join(AQUI, "registry.csv"))
    ap.add_argument("--saida", default=SAIDA)
    ap.add_argument("--device", default="cpu")
    ap.add_argument("--lote", type=int, default=16)
    a = ap.parse_args()

    sys.path.insert(0, os.path.join(a.repo, "atlas_ml"))
    import thermal
    import thermal_net as tn

    def carregar(c):
        d = torch.load(c, map_location="cpu", weights_only=True)
        m = tn.UNet(k=d["k"])
        m.load_state_dict(d["estado"])
        return m.eval()

    caminhos = sorted(glob.glob(os.path.join(a.repo, "modelos", "*.pt")))
    if not caminhos:
        sys.exit(f"nenhum .pt em {a.repo}/modelos")
    info = json.load(open(os.path.join(a.repo, "modelos", "modelos.json"), encoding="utf-8"))
    mm, fps, k = info["mm_px"], info["fps"], info["k"]

    d = np.load(os.path.join(a.repo, "exemplos", EXEMPLO))
    hist = d["frames"][:k].astype(np.float32)
    geo = d["geo"].astype(np.float32)
    metal = np.where(geo[1].any(0))[0]
    cont = geo.copy()
    if len(metal):                                   # barreiras viram PMMA: amostra contínua
        e, f = metal.min(), metal.max()
        cont[1] = 0
        cont[0][:, e:f + 1] = geo[0][:, [f + 1]]
    fim = thermal.fim_do_combustivel(cont)
    cols = np.where(cont[0].any(0))[0]

    # condições: a grade e, depois dela, os ensaios contínuos medidos
    linhas = [r for r in csv.DictReader(open(a.registry, encoding="utf-8")) if r["trial_id"].startswith("zarm")]
    medidos = [r for r in linhas if r["spread_rate_mm_s"] and not r["interruption"]]
    grade = [(o, p, u) for o in O2 for p in KPA for u in FLUXO]
    conds = grade + [(float(r["o2_pct"]), float(r["pressure_kpa"]), float(r["flow_cm_s"])) for r in medidos]
    n = len(conds)

    def vel(pred):
        fr = thermal.frente(np.concatenate([hist, pred]), cont)
        return thermal.velocidade(fr, fim)[0] * mm * fps, thermal.parou(fr, fim)

    h1 = torch.from_numpy(thermal.norm(hist))[None]
    g1 = torch.from_numpy(cont)[None]
    ph, pw = (-h1.shape[-2]) % 4, (-h1.shape[-1]) % 4
    h1 = torch.nn.functional.pad(h1, (0, pw, 0, ph), mode="replicate")
    g1 = torch.nn.functional.pad(g1, (0, pw, 0, ph), mode="replicate")
    H, W = hist.shape[-2:]

    soma = np.zeros((n, PASSOS, H, W), np.float32)      # para a média do conjunto
    v_rede = np.zeros((len(caminhos), n), np.float32)
    for i, c in enumerate(caminhos):
        m = carregar(c).to(a.device)
        for s in range(0, n, a.lote):
            lote = conds[s:s + a.lote]
            b = len(lote)
            cn = torch.from_numpy(np.stack([thermal.cond_norm(x) for x in lote])).to(a.device)
            with torch.no_grad():
                y = tn.passo_a_passo(m, h1.repeat(b, 1, 1, 1).to(a.device), g1.repeat(b, 1, 1, 1).to(a.device), cn, PASSOS)
            y = thermal.desnorm(y[:, :, :H, :W].cpu().numpy())
            soma[s:s + b] += y
            for j in range(b):
                v_rede[i, s + j] = vel(y[j])[0]
        print(f"rede {i + 1}/{len(caminhos)} ({os.path.basename(c)}) pronta", flush=True)
    media = soma / len(caminhos)
    v, parou = zip(*[vel(media[j]) for j in range(n)])
    v = np.array(v, np.float32)
    lo, hi = np.nanmin(v_rede, 0), np.nanmax(v_rede, 0)
    if not (np.isfinite(v).all() and np.isfinite(lo).all() and np.isfinite(hi).all()):
        sys.exit("há pontos sem frente detectada; reduza a grade")

    ng = len(grade)
    # saltos: ponto que foge mais de 25 % da média dos dois vizinhos na pressão (a curva é suave nela)
    cubo = v[:ng].reshape(len(O2), len(KPA), len(FLUXO))
    meio = (cubo[:, :-2] + cubo[:, 2:]) / 2
    saltos = np.argwhere(np.abs(cubo[:, 1:-1] - meio) > 0.25 * meio)
    for io, ip, iu in saltos:
        print(f"salto: O2 {O2[io]} %, {KPA[ip + 1]} kPa, {FLUXO[iu]} cm/s -> {cubo[io, ip + 1, iu]:.2f} mm/s (vizinhos {meio[io, ip, iu]:.2f})")
    confere = []
    for j, r in enumerate(medidos):
        confere.append({
            "trial": r["trial_id"], "o2_pct": conds[ng + j][0], "pressure_kpa": conds[ng + j][1], "flow_cm_s": conds[ng + j][2],
            "measured_mm_s": float(r["spread_rate_mm_s"]), "predicted_mm_s": round(float(v[ng + j]), 3),
            "nets_mm_s": [round(float(lo[ng + j]), 3), round(float(hi[ng + j]), 3)],
        })
    erro = np.array([c["predicted_mm_s"] - c["measured_mm_s"] for c in confere])
    pp = [float(r["o2_pct"]) * float(r["pressure_kpa"]) / 100 for r in linhas if r["o2_pct"] and r["pressure_kpa"]]
    faixa = dict(info["faixa_treino"])
    faixa["ppo2_kpa"] = [round(min(pp), 2), round(max(pp), 2)]

    try:
        commit = subprocess.run(["git", "-C", a.repo, "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip()
    except OSError:
        commit = ""
    r3 = lambda x: [round(float(t), 3) for t in x]
    doc = {
        "schema": "atlas.spread/1",
        "date": datetime.date.today().isoformat(),
        "model": {
            "name": "Atlas-MachineL", "url": "https://github.com/stars-beyond/Atlas-MachineL", "commit": commit,
            "nets": len(caminhos), "params_per_net": sum(p.numel() for p in carregar(caminhos[0]).parameters()),
            "trials": 51, "history_frames": k, "mm_px": mm, "fps": fps,
        },
        "sample": {
            "material": "pmma", "form": "thin film", "thickness_mm": 0.075, "gravity": 0, "flow": "opposed",
            "length_mm": round(float((cols.max() - cols.min()) * mm), 1), "steps": PASSOS,
            "history": f"first {k} frames of {EXEMPLO.replace('.npz', '').replace('_', ':')}, metal barriers replaced by PMMA in the geometry mask",
            "front_threshold_c": thermal.LIMIAR_FRENTE,
        },
        "train_range": faixa,
        "tolerance": 0.05,                      # a mesma folga do prever.py do repositório
        "axes": {"o2_pct": O2, "pressure_kpa": KPA, "flow_cm_s": FLUXO},
        "order": "o2_pct, pressure_kpa, flow_cm_s (last index runs fastest)",
        "v_mm_s": r3(v[:ng]), "v_lo_mm_s": r3(lo[:ng]), "v_hi_mm_s": r3(hi[:ng]),
        "stalls": [int(bool(x)) for x in parou[:ng]],
        "check": {
            "what": "model on the continuous sample at the conditions of the continuous ZARM runs, against the spread rate measured in each run; most nets saw these runs in training",
            "rows": confere,
            "mae_mm_s": round(float(np.abs(erro).mean()), 3), "max_mm_s": round(float(np.abs(erro).max()), 3),
        },
        "reported": {
            "source": "Atlas-MachineL README, cross-validation over 51 runs",
            "heldout_mae_barriers_mm_s": 0.51, "heldout_mae_gaps_mm_s": 1.14,
            "external_mae_mm_s": 0.41, "external_r2": 0.90, "external_runs": 14, "external_source": "Zenodo 19594886",
        },
        "license": "CC-BY-NC-4.0",
        "attribution": [
            "Ries, Krieger, Meyer et al., Flame Spread and Heat Transfer over Discrete Fuels separated by Metal Barriers under Microgravity, Zenodo 10.5281/zenodo.18479713",
            "Ries, Krieger, Meyer et al., Infra Red Recordings of Opposed-Flow Flame Propagation on Discrete PMMA Sheets, Zenodo 14959942",
        ],
    }
    os.makedirs(os.path.dirname(a.saida), exist_ok=True)
    with open(a.saida, "w", encoding="utf-8") as f:
        json.dump(doc, f, ensure_ascii=False, indent=1)
        f.write("\n")
    print(f"gravado {a.saida}: {ng} pontos, {sum(doc['stalls'])} com a frente parada, {len(saltos)} saltos; "
          f"conferência em {len(confere)} ensaios: erro absoluto médio {doc['check']['mae_mm_s']} mm/s")


if __name__ == "__main__":
    main()
