"""Contrato de entrada do motor. Todos os valores padrão são ilustrativos, não de uma nave real."""
from typing import Literal

from pydantic import BaseModel, Field

LabMaterial = Literal["pmma", "algodao", "retardante"]
FailureType = Literal[
    "radiator_degradation", "pump_degradation", "fan_failure", "o2_regulator_stuck",
    "o2_regulator_isolated", "cabin_leak", "power_change", "attitude_change", "battery_thermal_runaway",
]


class Component(BaseModel):
    name: str = "Rack de aviônica"
    mass_kg: float = Field(8.0, gt=0)
    heat_capacity_j_kg_k: float = Field(900.0, gt=0)
    internal_heat_w: float = Field(120.0, ge=0)
    area_m2: float = Field(0.6, gt=0, description="Área exposta ao ar da cabine")
    emissivity: float = Field(0.85, ge=0, le=1)
    char_length_m: float = Field(0.3, gt=0, description="Comprimento característico para convecção natural")
    coldplate_conductance_w_k: float = Field(1.7, ge=0, description="Placa fria ligada ao circuito de resfriamento")
    structure_conductance_w_k: float = Field(0.3, ge=0, description="Condução residual pela estrutura")
    operational_limit_k: float = Field(333.15, gt=0, description="Limite operacional declarado pelo usuário (hipótese)")
    material: LabMaterial = Field("pmma", description="Material combustível próximo, no simulador de chama da equipe")


class Spacecraft(BaseModel):
    radiator_area_m2: float = Field(12.0, gt=0)
    radiator_alpha: float = Field(0.20, ge=0, le=1, description="Absortividade solar")
    radiator_epsilon: float = Field(0.85, ge=0, le=1, description="Emissividade infravermelha")
    radiator_heat_capacity_j_k: float = Field(1.1e5, gt=0)
    radiator_sun_cos: float = Field(0.15, ge=0, le=1, description="cos do ângulo entre a normal do radiador e o Sol (atitude)")
    loop_ua_max_w_k: float = Field(60.0, gt=0, description="Condutância máxima cabine → radiador do circuito ativo")
    cabin_setpoint_k: float = Field(295.15, gt=0)
    cabin_heat_capacity_j_k: float = Field(6.0e6, gt=0, description="Ar + estrutura + equipamentos")
    cabin_internal_heat_w: float = Field(1800.0, ge=0, description="Equipamentos e tripulação, fora o componente")
    cabin_volume_m3: float = Field(100.0, gt=0)
    cabin_wall_area_m2: float = Field(150.0, gt=0)
    crew: int = Field(4, ge=0, le=12)
    ventilation_cm_s: float = Field(15.0, ge=0, le=200)
    component: Component = Component()


class Atmosphere(BaseModel):
    pressure_kpa: float = Field(101.3, gt=5, lt=200)
    oxygen_pct: float = Field(21.0, gt=5, le=100)
    relief_margin_kpa: float = Field(3.5, gt=0, description="Válvula de alívio abre em p0 + margem")


class Failure(BaseModel):
    type: FailureType
    day: float = Field(ge=0)
    magnitude: float
    note: str | None = None
    cell: str | None = Field(None, description="Modelo de célula do Battery Failure Databank (só para fuga térmica)")


class HabitatStage(BaseModel):
    day: float = Field(gt=0)
    name: str = Field(min_length=1, max_length=80)
    atmosphere: Atmosphere


class MissionConfig(BaseModel):
    label: str = "Cenário A"
    profile: str = "terra-marte"
    sample_hours: float = Field(6.0, ge=0.5, le=48)
    spacecraft: Spacecraft = Spacecraft()
    atmosphere: Atmosphere = Atmosphere()
    failures: list[Failure] = []
    habitats: list[HabitatStage] = []


class StateIn(BaseModel):
    oxygen_pct: float = Field(gt=0, le=100)
    pressure_kpa: float = Field(gt=0, lt=500)
    gravity_g: float = Field(ge=0, le=3)
    flow_cm_s: float | None = Field(None, ge=0, le=200)


class ExplainIn(StateIn):
    question: str | None = Field(None, max_length=600)
    event_title: str | None = None
    material: str | None = None


class FireIn(BaseModel):
    volume_m3: float = Field(100.0, gt=0)
    wall_area_m2: float = Field(150.0, gt=0)
    char_length_m: float = Field(3.0, gt=0)
    p0_kpa: float = Field(101.3, gt=0)
    t0_k: float = Field(295.0, gt=0)
    air_velocity_cm_s: float = Field(15.0, ge=0)
    preset: str | None = "fs1-grande"
    fuel_g: float | None = Field(None, gt=0)
    fire_power_w: float | None = Field(None, gt=0)
    burn_time_s: float | None = Field(None, gt=0)
    relief_kpa: float | None = None


class BatteryIn(BaseModel):
    cell: str | None = None
    cells: int = Field(1, ge=1, le=500, description="Células em fuga térmica")
    stagger_s: float = Field(0.0, ge=0, le=3600, description="Intervalo entre células na propagação; 0 = todas juntas")
    eject_s: float = Field(5.0, gt=0, le=120, description="Duração da ejeção (hipótese)")
    body_tau_s: float = Field(600.0, gt=0, le=7200, description="Constante de resfriamento do corpo da célula (hipótese)")
    gas_mol_per_cell: float = Field(0.0, ge=0, le=10, description="Gás liberado por célula; não está no databank")
    volume_m3: float = Field(100.0, gt=0)
    wall_area_m2: float = Field(150.0, gt=0)
    p0_kpa: float = Field(101.3, gt=0)
    t0_k: float = Field(295.0, gt=0)
    air_velocity_cm_s: float = Field(15.0, ge=0)
    relief_kpa: float | None = None


class CompareIn(BaseModel):
    a: MissionConfig
    b: MissionConfig
