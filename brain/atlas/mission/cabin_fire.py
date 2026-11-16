"""Aumento de pressão e temperatura numa cabine selada durante um incêndio.

Modelo de parâmetros concentrados de Gokoglu et al., "Prevention of Over-Pressurization During Combustion
in a Sealed Chamber", NASA/TM-2012-217712 (AIAA-2012-3511), NASA Glenn, 2012:

    Cv,g d(ng Tg)/dt = q_comb - q_rad - q_conv                      (Eq. 1 e 2; q_cond desprezado)
    q_conv = hw Aw (Tg - Tw),   Nu_L = hw L / λg = 0,664 Pr^(1/3) Re_L^(1/2)   (Eq. 4 e 5)
    q_rad  ≈ aP Vt σ (Tg^4 - Tw^4)                                   (Eq. 7)
    p = ng R Tg / Vt                                                  (gás ideal, volume constante)

O calor liberado segue o fator de Huggett, 13.100 kJ por kg de O₂, e a estequiometria da celulose
(C6H10O5 + 6 O2 → 6 CO2 + 5 H2O), como no artigo.

Calibração: com a Eq. 5 literal (propriedades do ar a 293 K) o caso-base dá 1,28 atm, contra 1,22 atm
publicados; o adiabático bate (1,327 vs 1,33 atm). O artigo também ancora o modelo em experimentos
ajustando parâmetros. Aqui hw é multiplicado por 2,02 e aP = 0,30 1/m (o artigo calcula aP pelas frações
de CO₂/H₂O), o que reproduz as três referências do caso-base: 1,22 atm, 376 K e perdas de 76% por
convecção e 24% por radiação. Fora desse caso, os dois fatores são hipóteses.
A extensão com válvula de alívio segue a ideia do trabalho de 2014 dos mesmos autores: acima do
ajuste, o gás sai e a pressão fica no limite.
"""
from collections.abc import Callable
from dataclasses import dataclass, field
import math

import numpy as np
from scipy.integrate import solve_ivp

from .constants import ATM_KPA, CV_AIR, R_GAS, SIGMA

# o motor foi escrito para numpy 2 (np.trapezoid); o brain fixa numpy 1.26, onde a mesma regra se chama np.trapz
_trapezoid = getattr(np, "trapezoid", None) or np.trapz

HUGGETT_J_PER_KG_O2 = 13.1e6
O2_G_PER_G_CELLULOSE = 6 * 31.998 / 162.14   # g de O₂ por g de C6H10O5
NET_GAS_MOL_PER_MOL_O2 = (11 - 6) / 6         # 6 O₂ viram 6 CO₂ + 5 H₂O: +5 mol de gás a cada 6 de O₂
AIR_MOLAR_MASS = 0.02897
AIR_MU = 1.81e-5        # Pa s, ar a ~293 K
AIR_K = 0.0257          # W/(m K), ar a ~293 K
AIR_PR = 0.71
DEFAULT_AP = 0.30            # 1/m, calibrado no caso-base VF-13 (ver docstring)
DEFAULT_H_MULTIPLIER = 2.02  # sobre a Eq. 5, calibrado no mesmo caso

REFERENCE = ("Gokoglu, Niehaus, Olson, Dietrich, Ruff, Ferkul, Johnston. Prevention of Over-Pressurization During "
             "Combustion in a Sealed Chamber. NASA/TM-2012-217712, 2012. https://ntrs.nasa.gov/citations/20120014384")


@dataclass
class FireCase:
    volume_m3: float
    wall_area_m2: float
    char_length_m: float
    p0_kpa: float
    t0_k: float
    air_velocity_m_s: float
    fire_power_w: float
    burn_time_s: float
    planck_abs_m: float = DEFAULT_AP
    h_multiplier: float = DEFAULT_H_MULTIPLIER
    relief_kpa: float | None = None
    duration_s: float | None = None
    adiabatic: bool = False
    notes: list[str] = field(default_factory=list)
    # Fonte de calor que não é combustão de material celulósico (fuga térmica de bateria, por exemplo):
    # heat_w(t) em W substitui o incêndio de potência constante, gas_mol_s(t) dá os mols de gás liberados
    # (sem a estequiometria de Huggett) e energy_j o total, para o relatório. burn_time_s vira a escala
    # de tempo da liberação mais rápida, usada no passo de integração.
    heat_w: Callable[[float], float] | None = None
    gas_mol_s: Callable[[float], float] | None = None
    energy_j: float | None = None


def wall_h(case: FireCase) -> float:
    rho = case.p0_kpa * 1e3 * AIR_MOLAR_MASS / (R_GAS * case.t0_k)
    re = rho * case.air_velocity_m_s * case.char_length_m / AIR_MU
    nu = 0.664 * AIR_PR ** (1 / 3) * math.sqrt(max(re, 0.0))
    return case.h_multiplier * nu * AIR_K / case.char_length_m


def run(case: FireCase, samples: int = 241, dense_until_s: float | None = None) -> dict:
    """dense_until_s: adensa a amostragem até esse instante, para não perder um pico curto numa simulação longa."""
    h = 0.0 if case.adiabatic else wall_h(case)
    ap = 0.0 if case.adiabatic else case.planck_abs_m
    n0 = case.p0_kpa * 1e3 * case.volume_m3 / (R_GAS * case.t0_k)
    tw = case.t0_k
    end = case.duration_s or max(case.burn_time_s * 4, case.burn_time_s + 120)
    k_relief = 2e-2  # 1/(s kPa): ganho do alívio, rápido frente à escala do incêndio

    def q_comb(t: float) -> float:
        if case.heat_w is not None:
            return case.heat_w(t)
        return case.fire_power_w if t <= case.burn_time_s else 0.0

    def gas_in(t: float, q: float) -> float:
        if case.heat_w is not None:
            return case.gas_mol_s(t) if case.gas_mol_s else 0.0
        return NET_GAS_MOL_PER_MOL_O2 * q / (HUGGETT_J_PER_KG_O2 * 0.031998)

    def rhs(t, y):
        n, u, _ = y                     # u = n·Tg; o terceiro estado acumula os mols ventilados pelo alívio
        tg = u / n
        p_kpa = n * R_GAS * tg / case.volume_m3 / 1e3
        q = q_comb(t)
        q_conv = h * case.wall_area_m2 * (tg - tw)
        q_rad = ap * case.volume_m3 * SIGMA * (tg ** 4 - tw ** 4)
        added = gas_in(t, q)
        vent = k_relief * (p_kpa - case.relief_kpa) * n if case.relief_kpa is not None and p_kpa > case.relief_kpa else 0.0
        # o gás que sai leva a própria entalpia sensível; o que entra chega à temperatura da parede
        du = (q - q_conv - q_rad) / CV_AIR - vent * tg + added * tw
        return [added - vent, du, vent]

    t_eval = np.linspace(0, end, samples)
    if dense_until_s:
        t_eval = np.unique(np.r_[t_eval, np.linspace(0, min(dense_until_s, end), samples)])
    sol = solve_ivp(rhs, (0, end), [n0, n0 * case.t0_k, 0.0], t_eval=t_eval, method="LSODA",
                    max_step=max(case.burn_time_s / 50, 0.05), rtol=1e-8, atol=1e-6)
    if not sol.success:
        raise RuntimeError(sol.message)
    n, u, vented_cum = sol.y
    tg = u / n
    p = n * R_GAS * tg / case.volume_m3 / 1e3
    i = int(np.argmax(p))
    energy = case.energy_j if case.heat_w is not None else case.fire_power_w * case.burn_time_s
    upto = sol.t <= (end if case.heat_w is not None else case.burn_time_s)
    lost_conv = float(_trapezoid(h * case.wall_area_m2 * (tg - tw)[upto], sol.t[upto]))
    lost_rad = float(_trapezoid(ap * case.volume_m3 * SIGMA * (tg ** 4 - tw ** 4)[upto], sol.t[upto]))
    vented = float(vented_cum[-1])
    return {
        "t_s": sol.t.tolist(), "p_kpa": p.tolist(), "t_gas_k": tg.tolist(),
        "p_max_kpa": float(p[i]), "p_max_atm": float(p[i] / ATM_KPA), "t_max_k": float(tg.max()), "t_at_p_max_s": float(sol.t[i]),
        "energy_kj": energy / 1e3, "lost_during_burn_kj": {"convection": lost_conv / 1e3, "radiation": lost_rad / 1e3},
        "wall_h_w_m2k": h, "planck_abs_m": ap, "relief_kpa": case.relief_kpa,
        "relief_opened": bool(case.relief_kpa is not None and p.max() >= case.relief_kpa - 0.05),
        "vented_mol": vented, "model": "lumped-cabin-fire", "reference": REFERENCE,
    }


def fuel_to_fire(fuel_g: float, burn_time_s: float) -> float:
    """Potência média (W) de queimar fuel_g de material celulósico em burn_time_s (fator de Huggett)."""
    return fuel_g * O2_G_PER_G_CELLULOSE / 1e3 * HUGGETT_J_PER_KG_O2 / burn_time_s


# Casos do artigo, para validação e como cenários de incêndio prontos.
VF13_BASELINE = dict(volume_m3=6.35, wall_area_m2=18.73, char_length_m=1.5, p0_kpa=0.95 * ATM_KPA, t0_k=293.0,
                     air_velocity_m_s=0.15, fire_power_w=10_000.0, burn_time_s=60.0)
VF13_PUBLISHED = {"p_max_atm": 1.22, "t_max_k": 376.0, "adiabatic_p_max_atm": 1.33, "adiabatic_t_max_k": 408.0}

FIRE_PRESETS = {
    "fs1-grande": {"name": "Amostra grande do Fire Safety-1 (50×100 cm, algodão-fibra de vidro)",
                   "fuel_g": 22.5, "burn_time_s": 105.0,
                   "note": "45 g de amostra, metade algodão (combustível) e metade fibra de vidro; tempo de queima estimado no artigo para 15 cm/s, 21% O₂ e 1 atm."},
    "vf13-base": {"name": "Caso-base do artigo: 10 kW por 60 s (≈ 8 folhas de papel por minuto)",
                  "fuel_g": None, "fire_power_w": 10_000.0, "burn_time_s": 60.0, "note": "600 kJ no total."},
    "pequeno": {"name": "Pequeno: 5 g de material celulósico em 30 s", "fuel_g": 5.0, "burn_time_s": 30.0,
                "note": "Cenário ilustrativo da equipe."},
}
