"""Exporta as redes do Atlas-MachineL para ONNX, para o serviço Python (brain) rodá-las sem torch.

Grava em brain/atlas/thermalcam/models/:
  f0_s0.onnx ...   uma rede por arquivo: (hist, geo, cond) -> próximo quadro, tudo normalizado
  sample.npz       o ensaio de exemplo do repositório (quadros medidos, geometria, condições)
  meta.json        convenções (normalização, escala, quadros/s, faixa de treino, de onde veio)

O serviço usa isso em /thermal (brain/atlas/thermalcam), e a API Go repassa em /api/predict/thermal.
Depois de exportar, cada rede ONNX é conferida contra a de torch num passo e num rollout de 20 passos.

Uso (precisa de numpy, torch, onnx e onnxruntime):
  python ml/export_thermal.py --repo ../Atlas-MachineL
"""
import argparse
import glob
import json
import os
import shutil
import subprocess
import sys

import numpy as np
import torch

AQUI = os.path.dirname(os.path.abspath(__file__))
SAIDA = os.path.join(AQUI, "..", "brain", "atlas", "thermalcam", "models")
EXEMPLO = "zarm18479713_147.npz"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--repo", required=True, help="pasta do checkout do Atlas-MachineL")
    ap.add_argument("--saida", default=SAIDA)
    a = ap.parse_args()

    sys.path.insert(0, os.path.join(a.repo, "atlas_ml"))
    import thermal
    import thermal_net as tn
    import onnxruntime as ort

    def carregar(c):
        d = torch.load(c, map_location="cpu", weights_only=True)
        m = tn.UNet(k=d["k"])
        m.load_state_dict(d["estado"])
        return m.eval()

    os.makedirs(a.saida, exist_ok=True)
    info = json.load(open(os.path.join(a.repo, "modelos", "modelos.json"), encoding="utf-8"))
    k = info["k"]
    d = np.load(os.path.join(a.repo, "exemplos", EXEMPLO))
    frames, geo, cond = d["frames"].astype(np.float32), d["geo"].astype(np.float32), d["cond"].astype(np.float32)
    H, W = frames.shape[1:]
    ph, pw = (-H) % 4, (-W) % 4
    hist = np.pad(thermal.norm(frames[:k]), ((0, 0), (0, ph), (0, pw)), mode="edge")[None]
    g = np.pad(geo, ((0, 0), (0, ph), (0, pw)), mode="edge")[None]
    c = thermal.cond_norm(cond)[None]

    nomes = []
    for caminho in sorted(glob.glob(os.path.join(a.repo, "modelos", "*.pt"))):
        m = carregar(caminho)
        nome = os.path.basename(caminho).replace(".pt", ".onnx")
        destino = os.path.join(a.saida, nome)
        torch.onnx.export(
            m, (torch.from_numpy(hist), torch.from_numpy(g), torch.from_numpy(c)), destino,
            input_names=["hist", "geo", "cond"], output_names=["next"], opset_version=17, dynamo=False,
            dynamic_axes={"hist": {2: "h", 3: "w"}, "geo": {2: "h", 3: "w"}, "next": {2: "h", 3: "w"}},
        )
        # confere: um passo e um rollout de 20 passos contra o torch
        s = ort.InferenceSession(destino, providers=["CPUExecutionProvider"])
        h_t, h_o = torch.from_numpy(hist), hist.copy()
        pior = 0.0
        with torch.no_grad():
            for _ in range(20):
                y_t = m(h_t, torch.from_numpy(g), torch.from_numpy(c))
                y_o = s.run(None, {"hist": h_o, "geo": g, "cond": c})[0]
                pior = max(pior, float(np.abs(y_t.numpy() - y_o).max()) * thermal.T_ESCALA)
                h_t = torch.cat([h_t[:, 1:], y_t], 1)
                h_o = np.concatenate([h_o[:, 1:], y_o], 1)
        if pior > 0.5:
            sys.exit(f"{nome}: ONNX difere do torch em {pior:.2f} °C no rollout de 20 passos")
        print(f"{nome}: {os.path.getsize(destino) / 1e6:.2f} MB, diferença máxima contra o torch em 20 passos {pior:.4f} °C")
        nomes.append(nome)

    shutil.copyfile(os.path.join(a.repo, "exemplos", EXEMPLO), os.path.join(a.saida, "sample.npz"))
    try:
        commit = subprocess.run(["git", "-C", a.repo, "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip()
    except OSError:
        commit = ""
    meta = {
        "schema": "atlas.thermal/1",
        "model": {"name": "Atlas-MachineL", "url": "https://github.com/stars-beyond/Atlas-MachineL", "commit": commit, "trials": 51},
        "nets": nomes, "k": k, "mm_px": info["mm_px"], "fps": info["fps"],
        "t_ref": thermal.T_REF, "t_scale": thermal.T_ESCALA, "cond_scale": [30.0, 100.0, 15.0],
        "front_threshold_c": thermal.LIMIAR_FRENTE,
        "train_range": info["faixa_treino"],
        "sample": {"trial": EXEMPLO.replace(".npz", "").replace("_", ":"), "o2_pct": float(cond[0]), "pressure_kpa": float(cond[1]),
                   "flow_cm_s": float(cond[2]), "frames": int(len(frames)), "h": int(H), "w": int(W),
                   "note": "part of the networks saw this run in training"},
        "license": "CC-BY-NC-4.0",
        "attribution": [
            "Ries, Krieger, Meyer et al., Flame Spread and Heat Transfer over Discrete Fuels separated by Metal Barriers under Microgravity, Zenodo 10.5281/zenodo.18479713",
            "Ries, Krieger, Meyer et al., Infra Red Recordings of Opposed-Flow Flame Propagation on Discrete PMMA Sheets, Zenodo 14959942",
        ],
    }
    with open(os.path.join(a.saida, "meta.json"), "w", encoding="utf-8") as f:
        json.dump(meta, f, ensure_ascii=False, indent=1)
        f.write("\n")
    print(f"gravado {a.saida}: {len(nomes)} redes, sample.npz e meta.json")


if __name__ == "__main__":
    main()
