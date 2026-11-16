"""Cenários prontos para a demonstração. Dias e falhas são inventados para a UX, não previsões."""
from .cabin_fire import FIRE_PRESETS
from .combustion import LAB_MATERIALS
from .orbit import PROFILES
from .schemas import Atmosphere, Failure, MissionConfig, Spacecraft
from .simulate import FAILURES

ATMOSPHERES = {
    "iss": {"name": "Estação espacial: 21% O₂ a 101,3 kPa", "pressure_kpa": 101.3, "oxygen_pct": 21.0},
    "lua": {"name": "Base lunar: 30% O₂ a 70,3 kPa (10,2 psi)", "pressure_kpa": 70.3, "oxygen_pct": 30.0,
            "note": "Valores citados no enunciado do desafio: 30% de O₂ ou mais, 10,2 psi ou menos."},
    "exploracao": {"name": "Atmosfera de exploração: 34% O₂ a 56,5 kPa (8,2 psi)", "pressure_kpa": 56.5, "oxygen_pct": 34.0,
                   "note": "Atmosfera proposta para reduzir a pré-respiração antes de atividades extraveiculares."},
}

# O traje é referência de evidência, não uma cabine de 100 m³ no modelo térmico.
SUIT_REFERENCE = {"name": "Traje EVA · referência", "oxygen_pct": 100.0, "pressure_kpa": 29.65,
                  "gravity_g": 0.165, "flow_cm_s": None, "psi": 4.3,
                  "note": "100% O₂ a 4,3 psi, conforme o enunciado. Sem modelo térmico ou de ventilação do traje."}


def scenario(label, profile, failures, atmosphere="iss", **spacecraft) -> dict:
    atm = ATMOSPHERES[atmosphere]
    return MissionConfig(label=label, profile=profile, failures=[Failure(**f) for f in failures],
                         atmosphere=Atmosphere(pressure_kpa=atm["pressure_kpa"], oxygen_pct=atm["oxygen_pct"]),
                         spacecraft=Spacecraft(**spacecraft)).model_dump()


SCENARIOS = {
    "demo-lunar": {
        "name": "Jornada lunar · nave, base e rover",
        "description": "Trânsito a 21%/101,3 kPa; base e rover a 30%/70,3 kPa. Uma falha opcional de ventilação no trânsito ilustra a detecção.",
        "config": {**scenario("Jornada lunar · segurança por ambiente", "terra-lua", [
            {"type": "fan_failure", "day": 1.5, "magnitude": 0.8, "note": "Perda hipotética de 80% do fluxo; remova para uma missão nominal."},
        ]), "sample_hours": 2, "habitats": [
            {"day": 3, "name": "Base lunar", "atmosphere": Atmosphere(oxygen_pct=30, pressure_kpa=70.3).model_dump()},
            {"day": 12, "name": "Rover pressurizado", "atmosphere": Atmosphere(oxygen_pct=30, pressure_kpa=70.3).model_dump()},
        ]},
    },
    "demo-marte": {
        "name": "Terra → Marte com falhas encadeadas",
        "description": "Regulador de O₂ travado (dia 120), bomba degradada (dia 180) e falha de ventilação (dia 200).",
        "config": scenario("Terra → Marte com falhas", "terra-marte", [
            {"type": "o2_regulator_stuck", "day": 120, "magnitude": 1.5, "note": "Válvula de suprimento presa aberta."},
            {"type": "o2_regulator_isolated", "day": 130, "magnitude": 0, "note": "A tripulação fecha a válvula manual."},
            {"type": "pump_degradation", "day": 180, "magnitude": 0.5},
            {"type": "fan_failure", "day": 200, "magnitude": 1.0},
        ]),
    },
    "mergulho-solar": {
        "name": "Mergulho a 0,5 UA, sem falhas",
        "description": "Só a trajetória: a irradiância quadruplica e o controle térmico satura.",
        "config": scenario("Mergulho a 0,5 UA", "mergulho-solar", []),
    },
    "lua-exploracao": {
        "name": "Lua com atmosfera de 30% O₂",
        "description": "Base lunar na atmosfera do enunciado, com falha de ventilação no dia 10.",
        "config": scenario("Lua, 30% O₂", "terra-lua", [{"type": "fan_failure", "day": 10, "magnitude": 1.0}], atmosphere="lua"),
    },
    "marte-nominal": {
        "name": "Terra → Marte nominal",
        "description": "Linha de base, sem falhas.",
        "config": scenario("Terra → Marte nominal", "terra-marte", []),
    },
    "bateria-modulo": {
        "name": "Fuga térmica de bateria num módulo de 29,7 m³",
        "description": "Oito células KULR 21700-K500 em fuga térmica no dia 150, num módulo com o volume livre do ATV do artigo da NASA.",
        "config": scenario("Bateria em fuga térmica, módulo de 29,7 m³", "terra-marte", [
            {"type": "battery_thermal_runaway", "day": 150, "magnitude": 8, "cell": "KULR 21700-K500",
             "note": "Defeito interno declarado; o motor não prevê o disparo."},
        ], cabin_volume_m3=29.7, cabin_wall_area_m2=52.4),
    },
}

# Cabines de referência para o cálculo de pressão. ATV e VF-13: volumes do artigo NASA/TM-2012-217712.
CABINS = {
    "atv": {"name": "ATV do artigo da NASA: 29,7 m³, 102,3 kPa", "volume_m3": 29.7, "wall_area_m2": 52.4, "p0_kpa": 102.3,
            "relief_kpa": 104.8, "note": "Volume livre e pressão inicial do artigo; limite de contingência 1048 hPa. A área de parede "
                                         "é estimada com a mesma proporção área/volume da câmara VF-13 (hipótese)."},
    "vf13": {"name": "Câmara VF-13 da NASA Glenn: 6,35 m³", "volume_m3": 6.35, "wall_area_m2": 18.73, "p0_kpa": 96.3,
             "relief_kpa": None, "note": "Câmara de ensaio selada do artigo (sem válvula de alívio)."},
}


def presets(battery=None) -> dict:
    return {
        "battery": battery.summary() if battery else None,
        "cabins": CABINS,
        "profiles": [{"id": p.id, "name": p.name, "description": p.description, "duration_days": round(p.duration_days, 1),
                      "transit_days": round(p.transit_days, 1), "destination": p.destination} for p in PROFILES.values()],
        "atmospheres": ATMOSPHERES,
        "suit_reference": SUIT_REFERENCE,
        "failures": FAILURES,
        "materials": {k: v["name"] for k, v in LAB_MATERIALS.items()},
        "scenarios": SCENARIOS,
        "fires": FIRE_PRESETS,
        "defaults": MissionConfig().model_dump(),
    }
