"""Adaptador para o Laboratório de chama 3D da equipe (lab3d, no repositório atlas-fire-extintion).

O motor não simula combustão. Ele traduz o estado da cabine para o protocolo de mensagens do lab3d
(o2 em %, press em psi, g em g terrestres, mat como índice, vent ligado/desligado) e repete, em Python,
a fronteira analítica que o próprio lab3d usa para dizer se a chama se sustenta (FL.judge em model.js):

    X_min = X0 + A/Ue + B·Ue,   Ue = sqrt(U² + Ub²),   Ub = 30·g^(1/3) cm/s

Os parâmetros são os do lab3d, descritos lá como "protótipo 2D, ajustado ao BASS-II", sem calibração
com os dados do PSI. A folga X - X_min é saída de um protótipo, não probabilidade de incêndio.
"""
import math

from .constants import PSI_PER_KPA

LAB_MATERIALS = {
    "pmma": {"index": 0, "name": "PMMA (acrílico)", "X0": 16.1, "A": 4.0, "B": 0.05},
    "algodao": {"index": 1, "name": "Tecido de algodão com fibra de vidro", "X0": 15.3, "A": 5.0, "B": 0.06},
    "retardante": {"index": 2, "name": "Tecido retardante (hipotético)", "X0": 23.0, "A": 6.0, "B": 0.08},
}
LAB_WIND_CM_S = 10.0  # o bridge.js do lab3d só liga/desliga a ventilação, sempre a 10 cm/s
SOURCE = "lab3d · web/public/lab3d/model.js (FL.judge) · stars-beyond/atlas-fire-extintion"


def boundary(material: str, flow_cm_s: float, gravity_g: float, oxygen_pct: float) -> dict:
    m = LAB_MATERIALS[material]
    ub = 30 * max(gravity_g, 0.0) ** (1 / 3)
    ue = max(math.hypot(flow_cm_s, ub), 0.05)
    x_min = m["X0"] + m["A"] / ue + m["B"] * ue
    us = math.sqrt(m["A"] / m["B"])
    burns = oxygen_pct > x_min
    mode = "queima" if burns else ("radiacao" if ue < us else "sopro")
    return {"x_min_pct": x_min, "margin_pp": oxygen_pct - x_min, "burns": burns, "mode": mode, "ue_cm_s": ue}


def lab3d_message(material: str, oxygen_pct: float, pressure_kpa: float, gravity_g: float, flow_cm_s: float) -> dict:
    """Mensagem 'atlas:set' pronta para o iframe do lab3d."""
    return {"type": "atlas:set", "o2": round(oxygen_pct, 2), "press": round(pressure_kpa * PSI_PER_KPA, 3),
            "g": round(gravity_g, 4), "mat": LAB_MATERIALS[material]["index"], "vent": flow_cm_s >= 1.0,
            "lit": True, "agent": None, "frac": 0.0, "seq": 1}
