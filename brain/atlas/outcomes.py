"""Desfechos tirados dos relatórios da NASA, para as tabelas do PSI que não dizem como o teste terminou.

Os arquivos `data/seed/outcomes/PSI-*.json` são gerados por `scripts/extract_outcomes.py`
(regras de texto + correções à mão, cada linha com a frase e a página do relatório).
Aqui eles só são juntados aos testes: nada é classificado de novo.

  python -m atlas.outcomes     # aplica num banco já carregado, sem refazer a ingestão
"""
import glob
import json
import os

from . import config

# Como cada fim de teste entra no vocabulário do Atlas. "Queimou" quer dizer que a chama se sustentou
# na condição de partida; o que a apagou depois (pouco ar, vento) fica em `outcome_raw` e nas métricas.
OUTCOME = {
    "quench": "queimou", "blowoff": "queimou", "fan_off": "queimou", "burnout": "queimou", "burned": "queimou",
    "self": "extinguiu", "not_sustained": "extinguiu", "not_ignited": "nao_acendeu", "unknown": "desconhecido",
}


def load():
    """{investigação: documento de desfechos}"""
    out = {}
    for path in sorted(glob.glob(os.path.join(config.SEED, "outcomes", "PSI-*.json"))):
        if path.endswith(".overrides.json"):
            continue
        doc = json.load(open(path, encoding="utf-8"))
        out[doc["investigation"]] = doc
    return out


def apply(tests, docs=None):
    """Junta os desfechos aos testes (dicionários do adaptador) pelo rótulo do teste.
    Devolve os documentos, com o id do teste no Atlas anotado em cada linha."""
    docs = load() if docs is None else docs
    for acc, doc in docs.items():
        by_label = {r["test"]: r for r in doc["tests"]}
        ref = doc["report"]["number"]
        for t in tests:
            r = by_label.get(t["label"]) if t["investigation_id"] == acc else None
            if not r:
                continue
            r["atlas_id"] = t["id"]
            form = (r.get("form") or "").lower()
            if form.endswith("acrílico") or "acrylic" in form:  # o seed pode estar em PT ou EN
                t["material"] = "pmma"
            if r["end"] == "unknown":
                continue
            t["outcome"] = OUTCOME[r["end"]]
            t["outcome_raw"] = r["end_label"]
            start = next((p["flow_cm_s"] for p in r["points"] if p["state"] in ("queimou", "nao_sustentou") and any(w in p["source"].lower() for w in ("partida", "start"))), None)
            if start is not None:
                t["flow_cm_s"] = start
            lim = r.get("limit") or {}
            t["metrics"].update({k: v for k, v in {
                "end": r["end"], "limit_flow_cm_s": lim.get("flow_cm_s"), "geometry": r.get("geometry"), "form": r.get("form"),
                "outcome_source": f"{ref}, p. {r['report_page']}" if r.get("report_page") else "PSI table",
                "comments": r.get("observation"),
            }.items() if v not in (None, "")})
    return docs


def main():
    from pgvector.psycopg import register_vector
    from psycopg.types.json import Jsonb

    from . import adapters
    from .embed import embed
    from .ingest import log, stats_for

    docs = load()
    if not docs:
        log("nenhum arquivo em data/seed/outcomes")
        return
    cols = ["id", "investigation_id", "label", "family", "fuel", "material", "o2", "pressure_atm", "gravity", "flow_cm_s",
            "co2", "he", "outcome", "outcome_raw", "metrics", "assumed", "source_file", "source_row", "raw"]
    with config.connect(autocommit=True) as conn:
        register_vector(conn)
        for acc, doc in docs.items():
            rows = conn.execute(f"SELECT {','.join(cols)} FROM tests WHERE investigation_id=%s ORDER BY source_row", (acc,)).fetchall()
            tests = [dict(zip(cols, r)) for r in rows]
            for t in tests:
                t["metrics"], t["assumed"], t["raw"] = t["metrics"] or {}, t["assumed"] or [], t["raw"] or {}
            apply(tests, {acc: doc})
            name = conn.execute("SELECT acronym FROM investigations WHERE id=%s", (acc,)).fetchone()[0] or acc
            cards = [adapters.card(t, name) for t in tests]
            vecs = embed(cards)
            with conn.cursor() as cur:
                cur.executemany("UPDATE tests SET outcome=%s, outcome_raw=%s, flow_cm_s=%s, material=%s, metrics=%s WHERE id=%s",
                                [(t["outcome"], t["outcome_raw"], t["flow_cm_s"], t["material"], Jsonb(t["metrics"]), t["id"]) for t in tests])
                cur.executemany("UPDATE chunks SET text=%s, embedding=%s WHERE kind='teste' AND test_id=%s",
                                [(c, v, t["id"]) for c, v, t in zip(cards, vecs, tests)])
            conn.execute("UPDATE investigations SET stats=%s WHERE id=%s", (Jsonb(stats_for(acc, tests)), acc))
            conn.execute("INSERT INTO meta VALUES (%s, %s) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value",
                         (f"outcomes:{acc}", Jsonb(doc)))
            known = sum(1 for t in tests if t["outcome"] != "desconhecido")
            log(f"{acc}: {known} de {len(tests)} testes com desfecho; {len(doc['audit'])} divergências na auditoria")


if __name__ == "__main__":
    main()
