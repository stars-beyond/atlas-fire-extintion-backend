"""Fuga térmica de bateria na cabine: calor medido no Battery Failure Databank (NASA/NREL) → modelo da cabine.

Cada ensaio do databank mede, num calorímetro de fuga térmica fracionada (S-FTRC), a energia total liberada
pela célula e quanto dela saiu pelo corpo (carcaça) e pelo material ejetado (sólidos, líquidos e gases).
Aqui essas energias viram fontes de calor no modelo concentrado da cabine (cabin_fire):

- Material ejetado: chega ao ar da cabine durante a ejeção, em poucos segundos (eject_s, hipótese).
- Corpo da célula: esfria devagar (body_tau_s, hipótese). Quanto desse calor vai para o ar e quanto vai para a
  caixa da bateria e a estrutura não está no databank; por isso o resultado vem em dois limites: só o ejetado
  no ar, ou ejetado e corpo no ar.

Para pulsos de segundos, o pico de pressão fica perto do adiabático e quase não depende de eject_s.
O gás liberado pela célula (CO₂, CO, H₂, hidrocarbonetos) não está na planilha: gas_mol_per_cell é zero por
padrão, o que subestima a pressão. A energia medida no calorímetro também pode não incluir a queima completa
desses gases no ar da cabine. Não é um limite superior.
"""
import json
import math
from pathlib import Path

from .cabin_fire import FireCase, run

LEVELS = {"median": "mediana", "max": "máximo"}
PATHS = {"ejecta": "só o ejetado aquece o ar", "ejecta_body": "ejetado e corpo aquecem o ar"}


class BatteryBank:
    def __init__(self, path: Path):
        data = json.loads(path.read_text(encoding="utf-8"))
        self.source = {k: v for k, v in data["source"].items() if k != "notice"}
        self.notice = data["source"]["notice"]
        self.cells = {c["cell"]: c for c in data["cells"]}
        self.tests = data["tests"]
        self.default = "KULR 21700-K500" if "KULR 21700-K500" in self.cells else next(iter(self.cells))

    def cell(self, name: str | None) -> dict:
        name = name or self.default
        if name not in self.cells:
            raise ValueError(f"Célula desconhecida no Battery Failure Databank: {name}")
        return self.cells[name]

    def tests_for(self, name: str) -> list[dict]:
        keep = ("test_id", "trigger", "failure", "ocv_v", "total_kj", "body_kj", "ejecta_kj", "sheet_row")
        return [{k: t[k] for k in keep} for t in self.tests if t["cell"] == name and t["failure"] != "No Ejection"]

    def summary(self) -> dict:
        return {"default": self.default, "source": self.source, "cells": list(self.cells.values())}


def runaway(bank: BatteryBank, cell: str | None, cells: int, *, volume_m3: float, wall_area_m2: float, p0_kpa: float,
            t0_k: float, air_velocity_m_s: float, relief_kpa: float | None, stagger_s: float = 0.0, eject_s: float = 5.0,
            body_tau_s: float = 600.0, gas_mol_per_cell: float = 0.0) -> dict:
    c = bank.cell(cell)
    body_frac = c["body_fraction_median"] or 0.0
    n = max(int(cells), 1)
    starts = [k * stagger_s for k in range(n)]
    span = starts[-1]

    def case_for(e_cell_j: float, with_body: bool) -> dict:
        e_ej, e_body = e_cell_j * (1 - body_frac), e_cell_j * body_frac

        def heat(t: float) -> float:
            q = 0.0
            for s in starts:
                dt = t - s
                if dt < 0:
                    continue
                if dt < eject_s:
                    q += e_ej / eject_s
                if with_body:
                    q += e_body / body_tau_s * math.exp(-dt / body_tau_s)
            return q

        def gas(t: float) -> float:
            return sum(gas_mol_per_cell / eject_s for s in starts if 0 <= t - s < eject_s)

        energy = n * (e_ej + (e_body if with_body else 0.0))
        duration = span + (4 * body_tau_s if with_body else max(120.0, 20 * eject_s))
        fc = FireCase(volume_m3=volume_m3, wall_area_m2=wall_area_m2, char_length_m=volume_m3 ** (1 / 3), p0_kpa=p0_kpa,
                      t0_k=t0_k, air_velocity_m_s=air_velocity_m_s, fire_power_w=0.0, burn_time_s=eject_s,
                      relief_kpa=relief_kpa, duration_s=duration, heat_w=heat, gas_mol_s=gas, energy_j=energy)
        return run(fc, samples=241, dense_until_s=span + 6 * eject_s)

    rows, curves = [], {}
    for level in LEVELS:
        for path in PATHS:
            r = case_for(c["total_kj"][level] * 1e3, path == "ejecta_body")
            rows.append({"level": level, "level_label": LEVELS[level], "path": path, "path_label": PATHS[path],
                         "energy_kj": r["energy_kj"], "p_max_kpa": r["p_max_kpa"], "dp_kpa": r["p_max_kpa"] - p0_kpa,
                         "t_max_k": r["t_max_k"], "t_at_p_max_s": r["t_at_p_max_s"], "relief_opened": r["relief_opened"],
                         "vented_mol": r["vented_mol"]})
            if (level, path) in (("median", "ejecta"), ("max", "ejecta_body")):
                curves["tipico" if level == "median" else "conservador"] = {"t_s": r["t_s"], "p_kpa": r["p_kpa"], "t_gas_k": r["t_gas_k"]}

    worst = max(rows, key=lambda x: x["p_max_kpa"])
    return {
        "cell": {k: c[k] for k in ("cell", "tests", "tests_with_ejection", "formats", "capacity_ah", "energy_wh", "mass_g",
                                   "total_kj", "body_fraction_median", "yield_over_stored_median", "trigger_temp_c_median", "notes")},
        "cells_in_runaway": n, "stagger_s": stagger_s,
        "inputs": {"volume_m3": volume_m3, "wall_area_m2": wall_area_m2, "p0_kpa": p0_kpa, "t0_k": t0_k,
                   "air_velocity_cm_s": air_velocity_m_s * 100, "relief_kpa": relief_kpa, "eject_s": eject_s,
                   "body_tau_s": body_tau_s, "gas_mol_per_cell": gas_mol_per_cell},
        "cases": rows, "curves": curves, "worst": worst,
        "tests": bank.tests_for(c["cell"]),
        "source": bank.source,
        "credit": "Calor por célula: Battery Failure Databank (DOE/NREL/Alliance e NASA). Pressão: modelo da cabine da NASA Glenn (NASA/TM-2012-217712).",
        "caveats": [
            "Energias medidas em calorímetro (S-FTRC), em ensaios com ejeção; ensaios sem ejeção ficam fora.",
            "Fração do corpo da célula: mediana dos ensaios do modelo, aplicada também ao máximo.",
            f"Tempo de ejeção ({eject_s:g} s) e de resfriamento do corpo ({body_tau_s:g} s) são hipóteses; o databank não os traz.",
            "Gás liberado pela célula fora do cálculo (não está na planilha), o que subestima a pressão." if gas_mol_per_cell == 0
            else f"Gás liberado: {gas_mol_per_cell:g} mol por célula (valor informado pelo usuário).",
            "A queima dos gases ventilados no ar da cabine pode liberar energia além da medida no calorímetro.",
            ("Todas as células entram em fuga térmica ao mesmo tempo (conservador)." if stagger_s == 0
             else f"Propagação célula a célula a cada {stagger_s:g} s."),
            "Válvula de alívio modelada como proporcional, sem limite de vazão; com pulsos rápidos a pressão passa um pouco do ajuste.",
        ],
    }
