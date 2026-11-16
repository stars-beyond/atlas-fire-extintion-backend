"""Linha do tempo da missão: geometria → irradiância → temperaturas → atmosfera → eventos → evidência.

Modelo concentrado de três nós térmicos e uma atmosfera bem misturada:

  radiador   C_r dT_r/dt = Q_loop + α S(r) A_r cosθ − ε σ A_r (T_r⁴ − T_esp⁴)
  cabine     C_c dT_c/dt = Q_int + Q_comp→cab − Q_loop
  componente C_k dT_k/dt = Q_k − [G_estr + G_placa·f_bomba + h A] (T_k − T_c) − ε_k σ A (T_k⁴ − T_c⁴)

  Q_loop = UA_max · f_bomba · u (T_c − T_r), com u ∈ [0,05; 1] o bypass que segura a cabine no ajuste.
  h = (h_f³ + h_n³)^(1/3): convecção forçada pela ventilação (h_f ∝ √v) e natural (h_n ∝ (ΔT/L)^¼ g^¼),
  ambas ∝ √(p/p_ref). Em queda livre h_n ≈ 0: sem ventilador, só sobram condução e radiação.

  atmosfera  dn_O2/dt = suprimento − consumo − vazamento − alívio;  dn_N2/dt = reposição − vazamento − alívio
             p = (n_O2 + n_N2) R T_c / V

Parâmetros ilustrativos. Serve para comparar cenários e mostrar a cadeia causal, não para prever
temperaturas de uma nave real nem para prever ignição.
"""
from dataclasses import dataclass, replace
import math
import re

import numpy as np
from scipy.integrate import solve_ivp

from .battery import BatteryBank, runaway
from .combustion import LAB_MATERIALS, boundary, lab3d_message
from .constants import DAY_S, M_O2, MICROGRAVITY_G, O2_KG_PER_CREW_DAY, R_GAS, S_1AU, SIGMA, SURFACE_G, T_SPACE_K
from .evidence import TIERS, EvidenceBase
from .ml_spread import FAST_RATIO, SpreadModel
from .orbit import PROFILES, geometry
from .schemas import Failure, MissionConfig

MODEL = {"name": "mission-lumped-thermal-atmosphere", "version": "0.1.0"}

FAILURES = {
    "radiator_degradation": {"title": "Degradação do radiador", "unit": "fração da área efetiva perdida (0–1)"},
    "pump_degradation": {"title": "Degradação da bomba do circuito térmico", "unit": "fração da capacidade perdida (0–1)"},
    "fan_failure": {"title": "Falha de ventilação", "unit": "fração da vazão de ar perdida (0–1)"},
    "o2_regulator_stuck": {"title": "Regulador de O₂ travado aberto", "unit": "vazão extra, em múltiplos do consumo da tripulação"},
    "o2_regulator_isolated": {"title": "Tripulação isola o regulador de O₂", "unit": "sem magnitude"},
    "cabin_leak": {"title": "Vazamento na cabine", "unit": "% do gás perdido por dia"},
    "power_change": {"title": "Mudança de potência do componente", "unit": "W somados à dissipação (negativo reduz)"},
    "attitude_change": {"title": "Mudança de atitude da nave", "unit": "novo cos do ângulo radiador–Sol (0–1)"},
    "battery_thermal_runaway": {"title": "Fuga térmica de bateria", "unit": "número de células em fuga térmica"},
}
H_FORCED_REF = 6.0      # W/(m² K) a 15 cm/s e 1 atm: ordem de grandeza para ar em baixa velocidade
V_REF = 15.0
H_NATURAL_COEF = 1.42   # placa vertical laminar em ar a 1 g: h ≈ 1,42 (ΔT/L)^¼
P_REF = 101.325
COMFORT_K = 300.15


@dataclass(frozen=True)
class Segment:
    radiator_area: float = 1.0
    pump: float = 1.0
    fan: float = 1.0
    o2_stuck: float = 0.0
    leak_per_day: float = 0.0
    extra_w: float = 0.0
    sun_cos: float = 0.15
    pressure_kpa: float = 101.3
    oxygen_pct: float = 21.0
    relief_margin_kpa: float = 3.5


def apply(seg: Segment, f: Failure) -> Segment:
    m = f.magnitude
    clip01 = lambda x: min(max(x, 0.0), 1.0)
    match f.type:
        case "radiator_degradation": return replace(seg, radiator_area=seg.radiator_area * (1 - clip01(m)))
        case "pump_degradation": return replace(seg, pump=seg.pump * (1 - clip01(m)))
        case "fan_failure": return replace(seg, fan=seg.fan * (1 - clip01(m)))
        case "o2_regulator_stuck": return replace(seg, o2_stuck=max(m, 0.0))
        case "o2_regulator_isolated": return replace(seg, o2_stuck=0.0)
        case "cabin_leak": return replace(seg, leak_per_day=max(m, 0.0) / 100)
        case "power_change": return replace(seg, extra_w=seg.extra_w + m)
        case "attitude_change": return replace(seg, sun_cos=clip01(m))
    return seg


def pt(text: str) -> str:
    """Decimais em pt-BR nos textos para a interface: 24.4% → 24,4%."""
    return re.sub(r"(?<=\d)\.(?=\d)", ",", text)


def convection_h(flow_cm_s: float, gravity_g: float, dT: float, length_m: float, p_kpa: float) -> float:
    pf = math.sqrt(max(p_kpa, 0.0) / P_REF)
    hf = H_FORCED_REF * math.sqrt(max(flow_cm_s, 0.0) / V_REF) * pf
    hn = H_NATURAL_COEF * (abs(dT) / length_m) ** 0.25 * max(gravity_g, 0.0) ** 0.25 * pf
    return (hf ** 3 + hn ** 3) ** (1 / 3)


def simulate(cfg: MissionConfig, evidence: EvidenceBase, battery: BatteryBank | None = None,
             spread: SpreadModel | None = None) -> dict:
    if cfg.profile not in PROFILES:
        raise ValueError(f"Perfil de missão desconhecido: {cfg.profile}")
    for f in cfg.failures:
        if f.type == "battery_thermal_runaway":
            if battery is None:
                raise ValueError("Fuga térmica de bateria exige o Battery Failure Databank (data/battery_cells.json).")
            battery.cell(f.cell)  # valida o modelo de célula antes de integrar
    profile = PROFILES[cfg.profile]
    sc, comp, atm = cfg.spacecraft, cfg.spacecraft.component, cfg.atmosphere
    dur = profile.duration_days
    transit = profile.transit_days
    stages = sorted(cfg.habitats, key=lambda h: h.day)
    if len({h.day for h in stages}) != len(stages) or any(h.day >= dur or h.day < transit for h in stages):
        raise ValueError("Ambientes devem ter dias distintos, após a chegada e antes do fim da missão.")
    if any(f.day >= dur for f in cfg.failures):
        raise ValueError("A falha deve ocorrer antes do fim da missão.")
    g_surface = SURFACE_G.get(profile.destination or "", MICROGRAVITY_G) if profile.surface_days else MICROGRAVITY_G

    step = cfg.sample_hours / 24
    t_out = np.unique(np.r_[np.arange(0, dur, step), dur, transit, [h.day for h in stages], [f.day for f in cfg.failures]])
    t_dense = np.linspace(0, dur, max(2000, int(dur * 24) + 1))
    r_dense = geometry(profile, t_dense)["r_au"]

    V = sc.cabin_volume_m3
    c_o2 = sc.crew * O2_KG_PER_CREW_DAY / M_O2 / DAY_S
    p0, x0 = atm.pressure_kpa, atm.oxygen_pct / 100
    n2_max = 5e-3
    k_relief = 1e-3
    C_k = comp.mass_kg * comp.heat_capacity_j_kg_k
    A_k = comp.area_m2

    def gravity(day: float) -> float:
        return MICROGRAVITY_G if day < transit else g_surface

    def fluxes(day: float, y, seg: Segment) -> dict:
        T_r, T_c, T_k, nO2, nN2 = y
        r = float(np.interp(day, t_dense, r_dense))
        S = S_1AU / r ** 2
        g = gravity(day)
        n = nO2 + nN2
        p = n * R_GAS * T_c / V / 1e3
        A_r = sc.radiator_area_m2 * seg.radiator_area
        u = min(max(0.5 + (T_c - sc.cabin_setpoint_k) / 4.0, 0.05), 1.0)
        q_loop = sc.loop_ua_max_w_k * seg.pump * u * (T_c - T_r)
        q_sol = sc.radiator_alpha * S * A_r * seg.sun_cos
        q_emit = sc.radiator_epsilon * SIGMA * A_r * (T_r ** 4 - T_SPACE_K ** 4)
        v = sc.ventilation_cm_s * seg.fan
        h = convection_h(v, g, T_k - T_c, comp.char_length_m, p)
        g_cond = comp.structure_conductance_w_k + comp.coldplate_conductance_w_k * seg.pump
        q_out_k = (g_cond + h * A_k) * (T_k - T_c) + comp.emissivity * SIGMA * A_k * (T_k ** 4 - T_c ** 4)
        po2 = nO2 / n * p
        po2_target = seg.pressure_kpa * seg.oxygen_pct / 100
        supply = 4 * c_o2 * min(max((po2_target - po2) / 0.5 + 0.25, 0.0), 1.0)
        makeup = n2_max * min(max((seg.pressure_kpa - 0.5 - p) / 0.5, 0.0), 1.0)
        relief = k_relief * max(p - seg.pressure_kpa - seg.relief_margin_kpa, 0.0)
        leak = seg.leak_per_day / DAY_S
        return dict(r=r, S=S, g=g, p=p, u=u, q_loop=q_loop, q_sol=q_sol, q_emit=q_emit, v=v, h=h, q_out_k=q_out_k,
                    po2=po2, supply=supply, makeup=makeup, relief=relief, leak=leak, n=n)

    def rhs(t_s, y, seg: Segment, frozen_day: float | None = None):
        day = frozen_day if frozen_day is not None else t_s / DAY_S
        f = fluxes(day, y, seg)
        nO2, nN2 = y[3], y[4]
        out_rate = f["leak"] + f["relief"]
        return [
            (f["q_loop"] + f["q_sol"] - f["q_emit"]) / sc.radiator_heat_capacity_j_k,
            (sc.cabin_internal_heat_w + f["q_out_k"] - f["q_loop"]) / sc.cabin_heat_capacity_j_k,
            (comp.internal_heat_w + seg.extra_w - f["q_out_k"]) / C_k,
            f["supply"] + seg.o2_stuck * c_o2 - c_o2 - out_rate * nO2,
            f["makeup"] - out_rate * nN2,
        ]

    # Falhas no dia 0 entram antes do equilíbrio inicial? Não: o equilíbrio é da nave nominal na partida.
    seg = Segment(sun_cos=sc.radiator_sun_cos, pressure_kpa=p0, oxygen_pct=atm.oxygen_pct, relief_margin_kpa=atm.relief_margin_kpa)
    n_tot = p0 * 1e3 * V / (R_GAS * sc.cabin_setpoint_k)
    y0 = [250.0, sc.cabin_setpoint_k, sc.cabin_setpoint_k + 10, x0 * n_tot, (1 - x0) * n_tot]
    warm = solve_ivp(lambda t, y: rhs(t, y, seg, frozen_day=0.0), (0, 60 * DAY_S), y0, method="LSODA",
                     rtol=1e-7, atol=1e-6)
    T_r0, T_c0, T_k0 = warm.y[:3, -1]
    n_tot = p0 * 1e3 * V / (R_GAS * T_c0)
    y = np.array([T_r0, T_c0, T_k0, x0 * n_tot, (1 - x0) * n_tot])

    failures = sorted(cfg.failures, key=lambda f: f.day)
    bounds = sorted({0.0, dur} | {f.day for f in failures if 0 < f.day < dur} | {h.day for h in stages} | ({transit} if profile.surface_days else set()))
    Y = np.zeros((5, t_out.size))
    seg_at = [None] * t_out.size
    for a, b in zip(bounds[:-1], bounds[1:]):
        for habitat in stages:
            if habitat.day == a:
                # Entrada em outro ambiente: troca idealizada, não uma despressurização da cabine.
                target = habitat.atmosphere
                seg = Segment(sun_cos=sc.radiator_sun_cos, pressure_kpa=target.pressure_kpa, oxygen_pct=target.oxygen_pct, relief_margin_kpa=target.relief_margin_kpa)
                n_total = target.pressure_kpa * 1e3 * V / (R_GAS * y[1])
                y = y.copy()
                y[3:] = [target.oxygen_pct / 100 * n_total, (1 - target.oxygen_pct / 100) * n_total]
        for f in failures:
            if f.day == a or (a == 0.0 and f.day <= 0):
                seg = apply(seg, f)
                if f.type == "battery_thermal_runaway":
                    # o calor total (mediana, corpo + ejetado) acaba na cabine; na escala de horas é um degrau
                    e = battery.cell(f.cell)["total_kj"]["median"] * 1e3 * max(int(f.magnitude), 1)
                    y = y.copy()
                    y[1] += e / sc.cabin_heat_capacity_j_k
        last = b == bounds[-1]
        mask = (t_out >= a) & ((t_out <= b) if last else (t_out < b))
        sol = solve_ivp(lambda t, yy: rhs(t, yy, seg), (a * DAY_S, b * DAY_S), y, method="LSODA",
                        t_eval=t_out[mask] * DAY_S, max_step=3 * 3600, rtol=1e-7, atol=1e-6)
        if not sol.success:
            raise RuntimeError(f"Integração falhou entre os dias {a:.1f} e {b:.1f}: {sol.message}")
        Y[:, mask] = sol.y
        for i in np.flatnonzero(mask):
            seg_at[i] = seg
        end = solve_ivp(lambda t, yy: rhs(t, yy, seg), (a * DAY_S, b * DAY_S), y, method="LSODA",
                        max_step=3 * 3600, rtol=1e-7, atol=1e-6) if not mask.any() or sol.t[-1] < b * DAY_S else sol
        y = end.y[:, -1]

    # ---------- grandezas derivadas ----------
    geo = geometry(profile, t_out)
    F = [fluxes(t, Y[:, i], seg_at[i]) for i, t in enumerate(t_out)]
    col = lambda k: np.array([f[k] for f in F])
    T_r, T_c, T_k, nO2, nN2 = Y
    p = col("p")
    x_o2 = nO2 / (nO2 + nN2) * 100
    vent = col("v")
    g = col("g")
    lab = [boundary(comp.material, vent[i], g[i], x_o2[i]) for i in range(t_out.size)]
    ev = evidence.tier_series(x_o2, p, g)
    limit = comp.operational_limit_k

    series = {
        "t_day": t_out, "r_au": geo["r_au"], "x_au": geo["x_au"], "y_au": geo["y_au"],
        "earth_x": geo["earth_x"], "earth_y": geo["earth_y"], "dest_x": geo["dest_x"], "dest_y": geo["dest_y"],
        "solar_flux_w_m2": col("S"), "gravity_g": g, "radiator_k": T_r, "cabin_k": T_c, "component_k": T_k,
        "margin_k": limit - T_k, "pressure_kpa": p, "oxygen_pct": x_o2, "po2_kpa": col("po2"),
        "ventilation_cm_s": vent, "convection_h_w_m2k": col("h"), "loop_w": col("q_loop"), "bypass_u": col("u"),
        "radiator_solar_w": col("q_sol"),
        "lab3d_x_min_pct": np.array([b["x_min_pct"] for b in lab]), "lab3d_margin_pp": np.array([b["margin_pp"] for b in lab]),
    }
    out = {k: [round(float(v), 5) for v in arr] for k, arr in series.items()}
    out["phase"] = [str(s) for s in geo["phase"]]
    out["habitat"] = [next((h.name for h in reversed(stages) if day >= h.day), "Nave em trânsito" if day < transit else "Habitat de destino") for day in t_out]
    out["relief_kpa"] = [seg.pressure_kpa + seg.relief_margin_kpa for seg in seg_at]
    out["evidence_tier"] = ev["tier"]
    out["evidence_tests"] = ev["tests_same"]
    out["evidence_docs"] = ev["docs_near"]
    # propagação prevista pelo modelo treinado (Atlas): só onde a condição está no domínio de treino
    ml = spread.series(x_o2, p, vent, g) if spread else None
    if ml:
        opt = lambda arr: [None if not np.isfinite(v) else round(float(v), 4) for v in arr]
        out["ml_spread_mm_s"], out["ml_spread_sd_mm_s"], out["ml_spread_ratio"] = opt(ml["v_mm_s"]), opt(ml["sd_mm_s"]), opt(ml["ratio"])
        out["ml_domain"] = ml["domain"]

    def snapshot(i: int) -> dict:
        return {"day": float(t_out[i]), "r_au": float(geo["r_au"][i]), "solar_flux_w_m2": float(series["solar_flux_w_m2"][i]),
                "gravity_g": float(g[i]), "phase": str(geo["phase"][i]), "radiator_k": float(T_r[i]), "cabin_k": float(T_c[i]),
                "component_k": float(T_k[i]), "margin_k": float(limit - T_k[i]), "pressure_kpa": float(p[i]),
                "oxygen_pct": float(x_o2[i]), "ventilation_cm_s": float(vent[i]), "bypass_u": float(series["bypass_u"][i]),
                "evidence_tier": ev["tier"][i], "lab3d_margin_pp": float(lab[i]["margin_pp"]), "lab3d_mode": lab[i]["mode"],
                "lab3d": lab3d_message(comp.material, float(x_o2[i]), float(p[i]), float(g[i]), float(vent[i])),
                "ml_spread": spread.predict(float(x_o2[i]), float(p[i]), float(vent[i]), float(g[i])) if spread else None}

    def drivers(i: int) -> list[str]:
        """O que mudou entre a partida e o instante i: a cadeia causal do evento."""
        d = []
        S0, S1 = series["solar_flux_w_m2"][0], series["solar_flux_w_m2"][i]
        if abs(S1 / S0 - 1) > 0.05:
            d.append(f"Fluxo solar {S0:.0f} → {S1:.0f} W/m² ({S1 / S0 - 1:+.0%}), a {geo['r_au'][i]:.2f} UA")
        for f in failures:
            if f.day <= t_out[i]:
                d.append(f"{FAILURES[f.type]['title']} no dia {f.day:g} (magnitude {f.magnitude:g}: {FAILURES[f.type]['unit']})")
        if g[i] != g[0]:
            d.append(f"Gravidade efetiva ≈ 1 µg (queda livre) → {g[i]:.3f} g (fase: {geo['phase'][i]})")
        if abs(vent[i] - vent[0]) > 0.5:
            d.append(f"Ventilação {vent[0]:.0f} → {vent[i]:.1f} cm/s; convecção no componente {series['convection_h_w_m2k'][0]:.1f} → {series['convection_h_w_m2k'][i]:.1f} W/m²K")
        if abs(x_o2[i] - x_o2[0]) > 0.3:
            d.append(f"O₂ {x_o2[0]:.1f}% → {x_o2[i]:.1f}%")
        if abs(p[i] - p[0]) > 0.5:
            d.append(f"Pressão {p[0]:.1f} → {p[i]:.1f} kPa")
        if series["bypass_u"][i] >= 0.999:
            d.append("Circuito de resfriamento no máximo (bypass fechado): a cabine não tem mais folga de rejeição")
        return [pt(x) for x in d]

    events = []

    def add(i, kind, severity, title, description, classification, models):
        events.append({"id": f"ev{len(events) + 1}", "day": round(float(t_out[i]), 3), "kind": kind, "severity": severity,
                       "title": title, "description": pt(description), "classification": classification,
                       "models": models, "drivers": drivers(i), "state": snapshot(i)})

    def add_battery(i: int, f: Failure) -> None:
        n = max(int(f.magnitude), 1)
        r = runaway(battery, f.cell, n, volume_m3=V, wall_area_m2=sc.cabin_wall_area_m2, p0_kpa=float(p[i]),
                    t0_k=float(T_c[i]), air_velocity_m_s=float(vent[i]) / 100, relief_kpa=out['relief_kpa'][i])
        c, w = r["cell"], r["worst"]
        typical = next(x for x in r["cases"] if x["level"] == "median" and x["path"] == "ejecta")
        trig = c["trigger_temp_c_median"]
        text = (f"Hipótese do cenário: {n} célula(s) {c['cell']} em fuga térmica (defeito interno declarado, o motor não "
                f"prevê o disparo). Calor por célula no Battery Failure Databank: mediana {c['total_kj']['median']:.1f} kJ, "
                f"máximo {c['total_kj']['max']:.1f} kJ ({c['tests_with_ejection']} ensaios com ejeção). "
                f"Pico de pressão na cabine: {typical['p_max_kpa']:.1f} a {w['p_max_kpa']:.1f} kPa "
                f"(+{typical['dp_kpa']:.1f} a +{w['dp_kpa']:.1f} kPa), gás a até {w['t_max_k'] - 273.15:.0f} °C; "
                + ("a válvula de alívio abre." if w["relief_opened"] else "a válvula de alívio não abre."))
        if trig:
            text += (f" Nos ensaios com aquecedor, a célula disparou com cerca de {trig:.0f} °C (rampa rápida, não é um "
                     f"limiar para aquecimento lento); o componente simulado estava a {T_k[i] - 273.15:.0f} °C.")
        add(i, "bateria", "critico", f"Fuga térmica de bateria: {n} × {c['cell']}", text, "hipotese_do_cenario",
            ["cabine_incendio", "gas_ideal"])
        events[-1]["battery"] = r

    add(0, "fase", "info", "Partida", f"{profile.name}. Estado inicial em equilíbrio térmico a {geo['r_au'][0]:.2f} UA.",
        "configuracao", ["kepler", "irradiancia", "balanco"])
    if profile.surface_days:
        i = int(np.searchsorted(t_out, transit))
        add(min(i, t_out.size - 1), "fase", "info", f"Chegada: {profile.destination}",
            f"Fim do trânsito em queda livre. Gravidade efetiva passa a {g_surface:.3f} g; a convecção natural volta a existir.",
            "configuracao", ["conveccao", "empuxo"])
    for f in failures:
        i = min(int(np.searchsorted(t_out, f.day)), t_out.size - 1)
        if f.type == "battery_thermal_runaway":
            add_battery(i, f)
            continue
        add(i, "falha", "atencao", FAILURES[f.type]["title"],
            f"Hipótese do cenário: {FAILURES[f.type]['unit']} = {f.magnitude:g}." + (f" {f.note}" if f.note else ""),
            "hipotese_do_cenario", [])

    for habitat in stages:
        i = int(np.searchsorted(t_out, habitat.day))
        add(i, "fase", "info", f"Entrada: {habitat.name}",
            f"Outro ambiente a {habitat.atmosphere.oxygen_pct:g}% O₂ e {habitat.atmosphere.pressure_kpa:g} kPa. "
            "Troca idealizada: controles restaurados; parâmetros térmicos e volume ilustrativos comuns. Não modela a transição de pressão.",
            "configuracao", ["gas_ideal"])

    def onsets(cond: np.ndarray, maxn: int = 3) -> list[int]:
        idx = np.flatnonzero(cond[1:] & ~cond[:-1]) + 1
        return ([0] if cond[0] else []) + idx.tolist()[:maxn]

    for i in onsets(T_k >= limit - 10):
        add(i, "termico", "atencao", "Margem térmica abaixo de 10 K",
            f"{comp.name} a {T_k[i] - 273.15:.1f} °C; limite operacional declarado {limit - 273.15:.0f} °C.", "calculado",
            ["balanco", "radiacao", "conveccao"])
    for i in onsets(T_k >= limit):
        add(i, "termico", "critico", "Limite operacional excedido",
            f"{comp.name} a {T_k[i] - 273.15:.1f} °C. O limite é uma hipótese do usuário: perder margem não significa ignição.",
            "calculado", ["balanco", "radiacao", "conveccao"])
    for i in onsets(series["bypass_u"] >= 0.999):
        add(i, "termico", "atencao", "Controle térmico saturado",
            f"O circuito já rejeita o máximo possível; a cabine sobe acima do ajuste ({T_c[i] - 273.15:.1f} °C).", "calculado",
            ["balanco", "radiacao", "irradiancia"])
    for i in onsets(T_c >= COMFORT_K):
        add(i, "termico", "atencao", "Cabine acima de 27 °C",
            f"Cabine a {T_c[i] - 273.15:.1f} °C (limiar de conforto ilustrativo).", "calculado", ["balanco"])
    for i in onsets(p >= np.array(out['relief_kpa']) - 0.05):
        add(i, "atmosfera", "atencao", "Válvula de alívio aberta",
            f"Pressão chegou a {p[i]:.1f} kPa: o excesso de gás é ventilado; a fração de O₂ pode continuar subindo.",
            "calculado", ["gas_ideal"])

    for i in onsets(vent < sc.ventilation_cm_s * 0.5):
        add(i, "deteccao", "atencao", "Ventilação reduzida: investigar transporte até o detector",
            f"Fluxo caiu para {vent[i]:.1f} cm/s. Em microgravidade, a fumaça depende do escoamento para chegar ao sensor. "
            "SAME/SAME-R estudam partículas e resposta de detectores. Menor fluxo pode atrasar o transporte; não foi calculado tempo de alarme.",
            "calculado", ["conveccao"])

    tiers = np.array(ev["tier"])
    changes = np.flatnonzero(tiers[1:] != tiers[:-1]) + 1
    for i in changes[:6]:
        before, now = TIERS[tiers[i - 1]], TIERS[tiers[i]]
        sev = "critico" if tiers[i] == "sem_evidencia" else "atencao" if tiers[i] != "medido" else "info"
        add(int(i), "evidencia", sev, f"Evidência: {before} → {now}",
            f"Ensaios tabulados de sólidos nesta condição: {ev['tests_same'][i]}; páginas que citam condição próxima: {ev['docs_near'][i]}.",
            "evidencia", ["gas_ideal"])

    burns = np.array([b["burns"] for b in lab])
    for i in (np.flatnonzero(burns[1:] != burns[:-1]) + 1)[:4]:
        add(int(i), "chama", "atencao" if burns[i] else "info",
            "Protótipo lab3d: chama passa a se sustentar" if burns[i] else "Protótipo lab3d: chama deixa de se sustentar",
            f"Fronteira do protótipo para {LAB_MATERIALS[comp.material]['name']}: O₂ mínimo {lab[i]['x_min_pct']:.1f}% "
            f"com escoamento efetivo de {lab[i]['ue_cm_s']:.1f} cm/s; cabine a {x_o2[i]:.1f}%. Protótipo não calibrado.",
            "prototipo", ["fronteira_lab3d", "empuxo"])
    if ml:
        ratio = np.nan_to_num(ml["ratio"], nan=0.0)
        for i in onsets(ratio >= FAST_RATIO):
            add(i, "modelo", "atencao", f"Modelo treinado: propagação {ratio[i]:.1f}× a nominal",
                f"O Atlas prevê a frente de chama a {ml['v_mm_s'][i]:.2f} mm/s (±{ml['sd_mm_s'][i]:.2f} entre as redes) na amostra de "
                f"referência de PMMA fino em microgravidade, contra {spread.nominal_v:.2f} mm/s na atmosfera nominal da ISS. "
                "Índice de inflamabilidade do material de referência, não a propagação no componente nem probabilidade de incêndio.",
                "modelo", ["atlas_ml"])
        transit_out = np.array([d not in ("ok", "borda", "gravidade") for d in ml["domain"]])
        for i in onsets(transit_out, maxn=2):
            add(i, "modelo", "info", "Fora do domínio do modelo treinado",
                f"Condição na cabine: {x_o2[i]:.1f}% O₂, {p[i]:.1f} kPa, {vent[i]:.1f} cm/s. O Atlas foi treinado com "
                "21–36 % de O₂, 60–103 kPa e 9–20 cm/s; aqui ele não devolve número em vez de extrapolar.",
                "modelo", ["atlas_ml"])
    first = {"fase": 0, "falha": 1, "bateria": 1}  # no mesmo dia, a causa antes da consequência
    events.sort(key=lambda e: (e["day"], first.get(e["kind"], 2), int(e["id"][2:])))

    weights = np.diff(t_out) * 24
    over = T_k >= limit
    first_over = float(t_out[np.argmax(over)]) if over.any() else None
    duration = lambda mask: float(np.sum(weights * np.asarray(mask)[:-1]))
    tier_share = {t: round(duration(tiers == t) / (dur * 24), 4) for t in TIERS}
    summary = {
        "duration_days": dur, "transit_days": transit,
        "component_max_k": float(T_k.max()), "component_max_day": float(t_out[int(np.argmax(T_k))]),
        "min_margin_k": float((limit - T_k).min()), "hours_over_limit": duration(over),
        "first_over_limit_day": first_over, "cabin_max_k": float(T_c.max()),
        "hours_control_saturated": duration(series['bypass_u'] >= 0.999),
        "oxygen_max_pct": float(x_o2.max()), "pressure_max_kpa": float(p.max()), "pressure_min_kpa": float(p.min()),
        "solar_flux_max_w_m2": float(series["solar_flux_w_m2"].max()), "r_min_au": float(geo["r_au"].min()),
        "evidence_share": tier_share, "hours_lab3d_burns": duration(burns),
        "hours_outside_tabulated": duration(tiers != 'medido'),
        "ml_spread_max_ratio": None, "ml_spread_max_day": None, "hours_ml_in_domain": None, "hours_ml_fast_spread": None,
    }
    if ml:
        r = ml["ratio"]
        if np.isfinite(r).any():
            k = int(np.nanargmax(r))
            summary.update(ml_spread_max_ratio=round(float(r[k]), 3), ml_spread_max_day=float(t_out[k]))
        summary.update(hours_ml_in_domain=duration(np.isfinite(r)), hours_ml_fast_spread=duration(np.nan_to_num(r) >= FAST_RATIO))
    return {
        "label": cfg.label, "profile": {"id": profile.id, "name": profile.name, "description": profile.description,
                                        "destination": profile.destination, "transit_days": transit,
                                        "surface_days": profile.surface_days},
        "model": MODEL, "units": {"time": "dia", "temperature": "K", "pressure": "kPa", "solar_flux": "W/m²",
                                  "oxygen": "% vol", "ventilation": "cm/s", "gravity": "g"},
        "series": out, "events": events, "summary": summary, "config": cfg.model_dump(),
        "validation": {
            "status": "not_validated_for_flight_safety",
            "notes": [
                "Mudanças de habitat são trocas idealizadas de ambiente com o mesmo volume e parâmetros térmicos; não modelam transferência, pré-respiração ou traje.",
                "Trajetória: transferência kepleriana idealizada (órbitas circulares e coplanares), não efemérides reais.",
                "Térmico: três nós concentrados com parâmetros ilustrativos; não é um modelo térmico de nave real.",
                "Convecção natural ∝ g^¼ e forçada ∝ √v: correlações de ordem de grandeza, não calibradas.",
                "O limite operacional é declarado pelo usuário; perder margem não prevê ignição.",
                "A fronteira de chama é a do protótipo lab3d da equipe, não calibrada com o PSI.",
                "Evidência: ensaios tabulados de sólidos com pressão e gravidade assumidas; menções em documentos não são ensaios.",
            ] + ([
                "Propagação prevista: Atlas-MachineL (8 U-Nets treinadas com ensaios infravermelhos do ZARM), interpolado numa tabela "
                "pré-calculada. Vale para a amostra de referência (filme de PMMA de 0,075 mm) em microgravidade, com 21–36 % de O₂, "
                "60–103 kPa e 9–20 cm/s; fora disso não há número. Não é a propagação no componente nem probabilidade de incêndio.",
            ] if ml else []),
        },
    }


def compare(a: dict, b: dict) -> dict:
    keys = [("component_max_k", "Temperatura máxima do componente", "K", "menor"),
            ("min_margin_k", "Menor margem térmica", "K", "maior"),
            ("hours_over_limit", "Horas acima do limite operacional", "h", "menor"),
            ("hours_control_saturated", "Horas com controle térmico saturado", "h", "menor"),
            ("oxygen_max_pct", "O₂ máximo na cabine", "%", "menor"),
            ("pressure_max_kpa", "Pressão máxima", "kPa", "menor"),
            ("hours_outside_tabulated", "Horas fora dos ensaios tabulados", "h", "menor"),
            ("hours_lab3d_burns", "Horas com chama sustentada no protótipo lab3d", "h", "menor"),
            ("ml_spread_max_ratio", "Propagação máxima prevista pelo modelo treinado (× nominal)", "×", "menor"),
            ("hours_ml_fast_spread", f"Horas com propagação prevista ≥ {FAST_RATIO:g}× a nominal", "h", "menor")]
    rows = []
    for k, label, unit, better in keys:
        va, vb = a["summary"].get(k), b["summary"].get(k)
        if va is None or vb is None:
            continue
        delta = vb - va
        win = None if abs(delta) < 1e-6 else ("b" if (delta < 0) == (better == "menor") else "a")
        rows.append({"key": k, "label": label, "unit": unit, "a": va, "b": vb, "delta": delta, "better": better, "winner": win})
    return {"a": a["label"], "b": b["label"], "rows": rows,
            "note": "Comparação entre hipóteses do mesmo modelo simplificado; mostra a direção do efeito, não valores de voo."}
