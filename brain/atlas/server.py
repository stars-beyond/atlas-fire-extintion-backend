"""Serviço Python da API em Go: embeddings na hora da pergunta, o filme da câmera térmica e o motor de missão."""
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

from . import config
from . import thermalcam as thermal
from .embed import embed

app = FastAPI(title="Atlas · embeddings")


class EmbedIn(BaseModel):
    texts: list[str]


@app.on_event("startup")
def warm():
    embed(["warm up"])


@app.get("/health")
def health():
    return {"ok": True, "model": config.EMBED_MODEL, "dim": config.EMBED_DIM}


@app.post("/embed")
def do_embed(body: EmbedIn):
    return {"model": config.EMBED_MODEL, "vectors": embed(body.texts[:64]).tolist()}


class ThermalIn(BaseModel):
    # Faixas largas de propósito: barram NaN, infinito e valores sem sentido físico (que rodariam as redes à toa e
    # ficariam no cache). Quem decide se o ar está dentro da faixa do modelo é a API em Go, antes de chamar aqui.
    o2_pct: float | None = Field(None, ge=0, le=100, allow_inf_nan=False)
    pressure_kpa: float | None = Field(None, gt=0, le=1000, allow_inf_nan=False)
    flow_cm_s: float | None = Field(None, ge=0, le=1000, allow_inf_nan=False)
    steps: int = Field(63, ge=1, le=1000)
    sample: bool = False      # o ensaio de exemplo, com os quadros medidos, em vez do ar do cenário


@app.post("/thermal")
def do_thermal(body: ThermalIn):
    """Filme de temperatura previsto pelas redes do Atlas-MachineL (ver atlas/thermalcam)."""
    if not thermal.disponivel():
        raise HTTPException(503, "thermal models not installed (run ml/export_thermal.py)")
    if not body.sample and None in (body.o2_pct, body.pressure_kpa, body.flow_cm_s):
        raise HTTPException(400, "o2_pct, pressure_kpa and flow_cm_s are required")
    try:
        return thermal.rollout(body.o2_pct, body.pressure_kpa, body.flow_cm_s, body.steps, body.sample)
    except thermal.Ocupado:
        # a fila de filmes está cheia: responde logo, em vez de prender uma thread que também serve /embed
        raise HTTPException(503, "the thermal model is busy; try again in a moment", headers={"Retry-After": "10"})


# --- motor de missão (modo história do site; ver atlas/mission) ---
# O pacote só é importado no primeiro pedido: se faltar algo dele, os embeddings e a câmera térmica seguem no ar.

@app.get("/mission/presets")
def mission_presets():
    """As missões de referência e as opções do motor (perfis, atmosferas, falhas, materiais)."""
    from .mission import service as mission
    return mission.presets()


@app.post("/mission/simulate")
def mission_simulate(cfg: dict):
    """Simula a missão hora a hora: série temporal, eventos, resumo e nível de evidência em cada instante."""
    from pydantic import ValidationError
    from .mission import service as mission
    from .mission.schemas import MissionConfig
    try:
        config = MissionConfig.model_validate(cfg)
    except ValidationError as e:
        raise HTTPException(422, e.errors(include_url=False, include_context=False))
    try:
        return mission.simulate(config)
    except mission.SimulationError as e:
        raise HTTPException(e.status, str(e))
