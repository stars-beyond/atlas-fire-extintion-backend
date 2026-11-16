"""O que o servidor do brain chama: as missões de referência e a simulação de uma missão.

O acervo do motor (5,7 MB de SQLite e as tabelas) só é carregado no primeiro pedido, para não atrasar a subida
do serviço de embeddings nem gastar memória se ninguém abrir o modo história.
"""
import os
from functools import lru_cache
from pathlib import Path
import uuid

from . import ml_spread
from .battery import BatteryBank
from .evidence import EvidenceBase
from .presets import presets as engine_presets
from .schemas import MissionConfig
from .simulate import simulate as engine_simulate

DATA_DIR = Path(os.environ.get("MISSION_DATA_DIR", Path(__file__).resolve().parent / "data"))


@lru_cache(maxsize=1)
def _engine():
    battery_file = DATA_DIR / "battery_cells.json"
    return {
        "evidence": EvidenceBase(DATA_DIR),
        "battery": BatteryBank(battery_file) if battery_file.exists() else None,
        "spread": ml_spread.load(DATA_DIR),
    }


def presets() -> dict:
    """Perfis, atmosferas, falhas, materiais e as missões de referência (scenarios), como o motor os define."""
    return engine_presets(_engine()["battery"])


class SimulationError(Exception):
    def __init__(self, status: int, message: str):
        super().__init__(message)
        self.status = status


def simulate(cfg: MissionConfig) -> dict:
    """Roda a missão. Configuração impossível vira 422; integração que não converge, 500 (como na API do motor)."""
    e = _engine()
    try:
        result = engine_simulate(cfg, e["evidence"], e["battery"], e["spread"])
    except ValueError as err:
        raise SimulationError(422, str(err)) from err
    except RuntimeError as err:
        raise SimulationError(500, f"The simulation did not converge: {err}") from err
    return {"simulation_id": f"sim-{uuid.uuid4().hex[:10]}", "status": "completed", **result}
