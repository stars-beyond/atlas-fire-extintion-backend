"""Propagação de chama prevista pelo Atlas-MachineL (Motor Neural de Combustão da equipe).

O motor não executa as redes: lê a tabela pré-calculada por tools/build_ml_spread.py (8 U-Nets treinadas com
ensaios infravermelhos do ZARM) e interpola linearmente em O₂ × pressão × fluxo.

O que o número significa: a velocidade da frente de chama sobre a amostra de referência (filme de PMMA de
0,075 mm, com barreiras metálicas) em microgravidade. É um índice de inflamabilidade do material de referência
sob a atmosfera da cabine, não a propagação no componente da missão nem o crescimento de um incêndio, e não é
probabilidade de incêndio. Fora do domínio de treino o motor não devolve número.
"""
import base64
import json
from pathlib import Path

import numpy as np
from scipy.interpolate import RegularGridInterpolator

MICROGRAVITY_MAX_G = 1e-3
TOLERANCE = 0.05          # mesma tolerância do aviso de prever.py (±5 % da faixa de treino)
FAST_RATIO = 1.5          # limiar de evento: propagação prevista 1,5× a da atmosfera nominal

DOMAIN_TEXT = {
    "ok": "dentro do domínio de treino",
    "borda": "na borda do domínio de treino (até 5 % além da faixa medida)",
    "gravidade": "fora do domínio: o modelo foi treinado só em microgravidade",
    "o2": "fora do domínio: O₂ fora da faixa de treino",
    "pressao": "fora do domínio: pressão fora da faixa de treino",
    "fluxo": "fora do domínio: fluxo de ar fora da faixa de treino",
}


class SpreadModel:
    def __init__(self, table_path: Path, frames_path: Path | None = None):
        self.table = json.loads(Path(table_path).read_text(encoding="utf-8"))
        grid = self.table["grid"]
        self.axes = [np.asarray(grid["oxygen_pct"], float), np.asarray(grid["pressure_kpa"], float), np.asarray(grid["flow_cm_s"], float)]
        self.range = self.table["training_range"]
        self.nominal_v = float(self.table["nominal"]["v_mm_s"])

        def interp(name: str) -> RegularGridInterpolator:
            values = np.asarray(self.table[name], float)
            if np.isnan(values).any():                      # frente não detectada: usa a média dos vizinhos válidos
                values = np.where(np.isnan(values), np.nanmean(values), values)
            return RegularGridInterpolator(self.axes, values, method="linear")

        self._v, self._sd, self._stop = interp("v_mm_s"), interp("v_sd_mm_s"), interp("stop_fraction")
        self._frames = None
        if frames_path and Path(frames_path).exists():
            self._frames = dict(np.load(frames_path))

    # ---------- domínio ----------
    def domain(self, o2: float, p: float, flow: float, g: float) -> str:
        if g > MICROGRAVITY_MAX_G:
            return "gravidade"
        edge = False
        for code, v, key in (("o2", o2, "o2_pct"), ("pressao", p, "pressure_kpa"), ("fluxo", flow, "flow_cm_s")):
            lo, hi = self.range[key]
            if not (lo * (1 - TOLERANCE) <= v <= hi * (1 + TOLERANCE)):
                return code
            edge |= not (lo <= v <= hi)
        return "borda" if edge else "ok"

    def _point(self, o2: float, p: float, flow: float) -> np.ndarray:
        return np.array([[float(np.clip(x, ax[0], ax[-1])) for x, ax in zip((o2, p, flow), self.axes)]])

    # ---------- previsão ----------
    def predict(self, o2: float, p: float, flow: float | None, g: float) -> dict:
        flow = 0.0 if flow is None else float(flow)
        code = self.domain(o2, p, flow, g)
        out = {"domain": code, "domain_text": DOMAIN_TEXT[code], "in_domain": code in ("ok", "borda"),
               "v_mm_s": None, "sd_mm_s": None, "ratio": None, "stop_fraction": None,
               "nominal_v_mm_s": self.nominal_v}
        if out["in_domain"]:
            x = self._point(o2, p, flow)
            v = float(self._v(x)[0])
            out.update(v_mm_s=round(v, 3), sd_mm_s=round(float(self._sd(x)[0]), 3),
                       ratio=round(v / self.nominal_v, 3), stop_fraction=round(float(self._stop(x)[0]), 3))
        return out

    def series(self, o2: np.ndarray, p: np.ndarray, flow: np.ndarray, g: np.ndarray) -> dict:
        codes = [self.domain(float(a), float(b), float(c), float(d)) for a, b, c, d in zip(o2, p, flow, g)]
        ok = np.array([c in ("ok", "borda") for c in codes])
        pts = np.column_stack([np.clip(np.asarray(x, float), ax[0], ax[-1]) for x, ax in zip((o2, p, flow), self.axes)])
        v, sd = self._v(pts), self._sd(pts)
        nan = np.full(len(codes), np.nan)
        v = np.where(ok, v, nan)
        return {"v_mm_s": v, "sd_mm_s": np.where(ok, sd, nan), "ratio": v / self.nominal_v, "domain": codes}

    # ---------- quadros previstos (animação) ----------
    def frames(self, o2: float, p: float, flow: float) -> dict | None:
        """Sequência pré-calculada mais próxima (distância normalizada pela faixa de cada eixo)."""
        if self._frames is None:
            return None
        conds = self._frames["conds"]
        span = np.array([self.axes[i][-1] - self.axes[i][0] for i in range(3)])
        k = int(np.argmin((((conds - np.array([o2, p, flow])) / span) ** 2).sum(1)))
        seq = self._frames["frames"][k]
        n, h, w = seq.shape
        return {"cond": {"oxygen_pct": float(conds[k][0]), "pressure_kpa": float(conds[k][1]), "flow_cm_s": float(conds[k][2])},
                "n": n, "h": h, "w": w, "fps": float(self._frames["fps"]), "history": int(self._frames["history"]),
                "t_lo": float(self._frames["t_lo"]), "t_hi": float(self._frames["t_hi"]),
                "frames_b64": base64.b64encode(seq.tobytes()).decode("ascii"),
                "metal_b64": base64.b64encode(np.ascontiguousarray(self._frames["geo"][1]).tobytes()).decode("ascii")}

    def info(self) -> dict:
        t = self.table
        return {k: t[k] for k in ("model", "repository", "reference_sample", "training_range", "gravity", "nominal",
                                   "validation", "license", "attribution")} | {"fast_ratio": FAST_RATIO,
                                                                               "frames": self._frames is not None}


def load(data_dir: Path) -> SpreadModel | None:
    table = Path(data_dir) / "ml_spread_table.json"
    return SpreadModel(table, Path(data_dir) / "ml_flame_frames.npz") if table.exists() else None
