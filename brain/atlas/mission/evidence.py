"""Evidência experimental para um estado da cabine, a partir do dataset do CosmoEX Flame.

Duas fontes, sempre separadas na resposta:
- Ensaios tabulados (solid_tests.json): uma linha de tabela experimental = um ensaio, com material.
- Menções em documentos (conditions.sqlite): valores com unidade citados em relatórios, artigos NTRS e
  registros PSI, agrupados por página. Uma página que cita 30% de O₂ e 55 kPa não é um ensaio
  nessas condições; ela só indica onde ler.

A regra de nível repete o veredito do Atlas de Extinção (api/verdict.go) para os produtos da equipe
falarem a mesma língua: vizinhança de ±2 pontos de O₂, ±20% de pressão (log) e ±0,05 g.
"""
from collections import defaultdict
from dataclasses import dataclass
import json
import math
from pathlib import Path
import re
import sqlite3

import numpy as np
from .investigations import Investigations

TOL_O2_PP = 2.0
TOL_LNP = 0.20
TOL_G = 0.05

TIERS = {
    "medido": "Medido",
    "delimitado": "Delimitado",
    "parcial": "Só um limite",
    "sem_evidencia": "Terra incógnita",
}
TIER_TEXT = {
    "medido": "Há ensaio tabulado nesta atmosfera e nesta gravidade.",
    "delimitado": "Não há ensaio nesta gravidade, mas há em 0 g e em 1 g nesta atmosfera.",
    "parcial": "Há ensaio nesta atmosfera só de um lado (0 g ou 1 g).",
    "sem_evidencia": "Nenhum ensaio tabulado de sólidos nesta atmosfera.",
}

SOLID_FAMILY = re.compile(
    r"^(BASS|Saffire|WIF|Wire insulation|Lunar/Martian material|Lunar solid fuel|Low-pressure|"
    r"Partial-gravity flame spread|SoFIE|Barrier material|Spacecraft fire safety|Ignition)"
)
ROLE_ORDER = {"ensaio/medição": 0, "transcrição de imagem": 1, "menção": 2, "requisito/planejado": 3, "simulação/modelo": 4}


@dataclass
class State:
    oxygen_pct: float
    pressure_kpa: float
    gravity_g: float
    flow_cm_s: float | None = None


def classify(same: int, zero: int, one: int) -> str:
    if same > 0:
        return "medido"
    if zero > 0 and one > 0:
        return "delimitado"
    if zero > 0 or one > 0:
        return "parcial"
    return "sem_evidencia"


class EvidenceBase:
    def __init__(self, data_dir: Path):
        self.tests = json.loads((data_dir / "solid_tests.json").read_text(encoding="utf-8"))
        self.tests = [t for t in self.tests if t["oxygen_pct"] is not None]
        self.t_o2 = np.array([t["oxygen_pct"] for t in self.tests])
        self.t_p = np.array([t["pressure_kpa"] for t in self.tests])
        self.t_g = np.array([t["gravity_g"] for t in self.tests])
        self.manifest = json.loads((data_dir / "dataset_manifest.json").read_text(encoding="utf-8"))
        self._load_pages(data_dir / "conditions.sqlite")
        self.investigations = Investigations(data_dir, self.pages, self.tests)

    def _load_pages(self, path: Path) -> None:
        db = sqlite3.connect(path)
        db.row_factory = sqlite3.Row
        rows = db.execute(
            "select * from conditions where role != 'tabela experimental' "
            "and quantity in ('oxygen','pressure','gravity','flow_velocity')"
        ).fetchall()
        db.close()
        pages: dict[tuple, dict] = {}
        for r in rows:
            key = (r["document_id"], r["page"] if r["page"] is not None else r["row"])
            pg = pages.setdefault(key, {
                "solid": bool(SOLID_FAMILY.match(r["experiment"] or "")),
                "document_id": r["document_id"], "title": r["title"], "experiment": r["experiment"],
                "psi_id": r["psi_id"], "page": r["page"], "url": r["url"], "source_type": r["source_type"],
                "local_file": r["local_file"], "content_sha256": r["content_sha256"], "values": defaultdict(list),
            })
            pg["values"][r["quantity"]].append({
                "min": r["normalized_min"], "max": r["normalized_max"], "unit": r["normalized_unit"],
                "text": r["value_text"], "context": r["context"], "role": r["role"], "flags": r["flags"],
            })
        self.pages = list(pages.values())
        self.solid = np.array([pg["solid"] for pg in self.pages], bool)
        # Cada valor citado é um intervalo próprio: uma página que cita 15% e 40% não cobre 28%.
        self.iv = {q: self._intervals(q) for q in ("oxygen", "pressure", "gravity")}

    def _intervals(self, q: str) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
        lo, hi, idx = [], [], []
        for i, pg in enumerate(self.pages):
            for v in pg["values"].get(q, []):
                if v["min"] is not None:
                    lo.append(v["min"])
                    hi.append(v["max"] if v["max"] is not None else v["min"])
                    idx.append(i)
        return np.array(lo, float), np.array(hi, float), np.array(idx, int)

    def _any_hit(self, q: str, low: np.ndarray, high: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
        """(acerto por amostra × página, página tem a grandeza). low/high: (S, 1)."""
        lo, hi, idx = self.iv[q]
        out = np.zeros((low.shape[0], len(self.pages)), bool)
        has = np.zeros(len(self.pages), bool)
        if len(idx):
            hit = (hi[None] >= low) & (lo[None] <= high)
            starts = np.flatnonzero(np.r_[True, idx[1:] != idx[:-1]])
            out[:, idx[starts]] = np.logical_or.reduceat(hit, starts, axis=1)
            has[idx] = True
        return out, has

    # ---------- séries para a linha do tempo ----------
    def tier_series(self, o2: np.ndarray, p: np.ndarray, g: np.ndarray) -> dict:
        o2, p, g = (np.asarray(a, dtype=float)[:, None] for a in (o2, p, g))
        near = (np.abs(self.t_o2[None] - o2) <= TOL_O2_PP + 1e-9) & (np.abs(np.log(self.t_p[None] / p)) <= TOL_LNP + 1e-9)
        same = (near & (np.abs(self.t_g[None] - g) <= TOL_G)).sum(1)
        zero = (near & (self.t_g[None] <= TOL_G)).sum(1)
        one = (near & (self.t_g[None] >= 1 - TOL_G)).sum(1)
        tiers = [classify(int(s), int(z), int(n)) for s, z, n in zip(same, zero, one)]
        docs = self._page_match(o2, p, g).sum(1)
        return {"tier": tiers, "tests_same": same.tolist(), "docs_near": docs.tolist()}

    def _page_match(self, o2, p, g) -> np.ndarray:
        o2_hit, _ = self._any_hit("oxygen", o2 - TOL_O2_PP, o2 + TOL_O2_PP)
        p_hit, p_has = self._any_hit("pressure", p * math.exp(-TOL_LNP), p * math.exp(TOL_LNP))
        g_hit, g_has = self._any_hit("gravity", g - TOL_G, g + TOL_G)
        return self.solid[None] & o2_hit & (~p_has[None] | p_hit) & (~g_has[None] | g_hit)

    # ---------- detalhe de um estado ----------
    def detail(self, s: State, limit: int = 6) -> dict:
        series = self.tier_series([s.oxygen_pct], [s.pressure_kpa], [s.gravity_g])
        tier = series["tier"][0]
        d = np.hypot(np.hypot((self.t_o2 - s.oxygen_pct) / TOL_O2_PP, np.log(self.t_p / s.pressure_kpa) / TOL_LNP),
                     (self.t_g - s.gravity_g) / TOL_G)
        nearest = []
        for i in np.argsort(d)[:limit]:
            t = self.tests[i]
            nearest.append({
                "experiment": t["experiment"], "psi_id": t["psi_id"], "test_id": t["test_id"], "material": t["material"],
                "oxygen_pct": t["oxygen_pct"], "flow_cm_s": t["flow_cm_s"], "pressure_kpa": t["pressure_kpa"],
                "gravity_g": t["gravity_g"], "distance": round(float(d[i]), 2), "inside": bool(abs(t['oxygen_pct'] - s.oxygen_pct) <= TOL_O2_PP + 1e-9 and abs(math.log(t['pressure_kpa'] / s.pressure_kpa)) <= TOL_LNP + 1e-9 and abs(t['gravity_g'] - s.gravity_g) <= TOL_G),
                "d_o2_pp": round(t["oxygen_pct"] - s.oxygen_pct, 2),
                "title": t["title"], "row": t["row"], "url": t["url"], "content_sha256": t["content_sha256"],
                "assumptions": t["assumptions"], "values_text": t["values_text"],
            })

        match = self._page_match(np.array([[s.oxygen_pct]]), np.array([[s.pressure_kpa]]), np.array([[s.gravity_g]]))[0]
        docs = []
        for i in np.flatnonzero(match):
            pg = self.pages[i]
            vals = {q: sorted(v, key=lambda x: ROLE_ORDER.get(x["role"], 9))[:2] for q, v in pg["values"].items()}
            best = min((ROLE_ORDER.get(v["role"], 9) for vs in pg["values"].values() for v in vs), default=9)
            docs.append({k: pg[k] for k in ("document_id", "title", "experiment", "psi_id", "page", "url", "source_type")}
                        | {"values": vals, "best_role": best, "has_pressure": "pressure" in pg["values"],
                           "has_gravity": "gravity" in pg["values"]})
        docs.sort(key=lambda x: (x["best_role"], not x["has_pressure"], not x["has_gravity"]))

        gravity_docs = []
        if s.gravity_g > TOL_G:  # superfície: qualquer família que cite essa gravidade, em qualquer atmosfera
            g_hit, _ = self._any_hit("gravity", np.array([[s.gravity_g - TOL_G]]), np.array([[s.gravity_g + TOL_G]]))
            for i in np.flatnonzero(g_hit[0])[:limit]:
                pg = self.pages[i]
                gravity_docs.append({k: pg[k] for k in ("document_id", "title", "experiment", "page", "url")}
                                    | {"family": "sólidos" if pg["solid"] else "outra família",
                                       "values": pg["values"].get("gravity", [])[:2]})

        o2_tested = (float(self.t_o2.min()), float(self.t_o2.max()))
        return {
            **self.investigations.detail(s),
            "state": s.__dict__, "tier": tier, "tier_label": TIERS[tier], "tier_text": TIER_TEXT[tier],
            "tests_same": series["tests_same"][0], "nearest_tests": nearest,
            "documents": docs[:limit], "documents_total": len(docs), "gravity_documents": gravity_docs,
            "envelope": {"oxygen_pct": o2_tested, "pressure_kpa": "não tabelada (assumida ~101 kPa)", "gravity_g": "0 (ensaios em órbita)"},
            "caveats": [
                "Pressão e gravidade dos ensaios tabulados de sólidos não estão nas tabelas: assumidas ~1 atm e microgravidade.",
                "Menções em documentos são valores citados no texto; não são ensaios nessas condições.",
                "Extração automática não validada; confira o original antes de uso quantitativo.",
                "Saffire: resultados terrestres sem condições completas não elevam o nível de cobertura. 0 g e 1 g não garantem limites conservadores em gravidade parcial.",
            ],
            "rule": "Mesma regra do Atlas de Extinção: ±2 pp de O₂, ±20% de pressão, ±0,05 g.",
        }

    def summary(self) -> dict:
        by_exp: dict[str, int] = defaultdict(int)
        for t in self.tests:
            by_exp[t["experiment"]] += 1
        return {"tests": len(self.tests), "by_experiment": dict(by_exp), "pages_solids": int(self.solid.sum()),
                "pages_all": len(self.pages),
                "oxygen_tested_pct": [float(self.t_o2.min()), float(self.t_o2.max())],
                "dataset": self.manifest}
