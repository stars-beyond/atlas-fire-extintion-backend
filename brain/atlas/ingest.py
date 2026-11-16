"""Ingestão do Atlas: PSI -> testes normalizados -> índice vetorial.

  python -m atlas.ingest

Idempotente: busca o que há de novo no PSI (catálogos vencidos, arquivos novos ou substituídos) e só refaz o
acervo quando a impressão digital das entradas muda. O acervo novo é montado no schema atlas_build e trocado
com o publicado (atlas) numa transação: a API não fica sem dados durante a ingestão. ATLAS_FORCE=1 refaz sempre.
"""
import datetime
import glob
import hashlib
import json
import os
import sys
import time
from collections import Counter

import psycopg
from pgvector.psycopg import register_vector
from psycopg.types.json import Jsonb

from . import adapters, config, docs, models, outcomes, psi
from .embed import embed

TABLES = f"""
CREATE TABLE investigations (
  id text PRIMARY KEY, acronym text, title text, kind text, area text, hardware text, hardware_family text,
  family text, role text, priority real, priority_why text, start_year int, end_year int, total_gb real,
  n_publications int, n_files int, objective text, approach text, hypothesis text, impacts text, doi text,
  stats jsonb, embedding vector({config.EMBED_DIM})
);
CREATE TABLE publications (id serial PRIMARY KEY, investigation_id text REFERENCES investigations, title text, authors text, doi text);
CREATE TABLE tests (
  id text PRIMARY KEY, investigation_id text REFERENCES investigations, label text, family text, fuel text,
  material text, o2 real, pressure_atm real, gravity real, flow_cm_s real, co2 real, he real,
  outcome text, outcome_raw text, metrics jsonb, assumed text[], source_file text, source_row int, raw jsonb
);
CREATE INDEX ON tests (investigation_id);
CREATE INDEX ON tests (family, o2, pressure_atm);
CREATE TABLE chunks (
  id bigserial PRIMARY KEY, kind text, investigation_id text, test_id text, title text, category text,
  source_file text, page int, text text,
  tsv tsvector GENERATED ALWAYS AS (to_tsvector('english', coalesce(title, '') || ' ' || text)) STORED,
  embedding vector({config.EMBED_DIM})
);
CREATE TABLE models (id text PRIMARY KEY, spec jsonb);
CREATE TABLE meta (key text PRIMARY KEY, value jsonb);
"""

# Papel de cada investigação na segurança contra incêndio, como o enunciado do desafio descreve:
# prevenção (inflamabilidade de materiais), detecção (fumaça) e supressão (diluição com CO₂ e He).
META = {
    "PSI-25": ("solido", "MSG · flow duct", "prevencao", 1.00, "Flammability of cabin solids in low flow; cited in the challenge statement"),
    "PSI-26": ("solido", "MSG · flow duct", "prevencao", 0.95, "Burning and suppression of solids; cited in the challenge statement"),
    "PSI-98": ("solido", "Cygnus · Saffire", "prevencao", 1.00, "Full-scale fire aboard a spacecraft; cited in the challenge statement"),
    "PSI-99": ("solido", "Cygnus · Saffire", "prevencao", 1.00, "Nine materials, compared between 0 g and 1 g; cited in the challenge statement"),
    "PSI-100": ("solido", "Cygnus · Saffire", "prevencao", 0.95, "Full-scale fire with higher flow; cited in the challenge statement"),
    "PSI-102": ("fumaca", "Detection · aerosols", "deteccao", 0.90, "Smoke for detectors in microgravity; cited in the challenge statement"),
    "PSI-101": ("fumaca", "Detection · aerosols", "deteccao", 0.90, "Smoke for detectors, second campaign; cited in the challenge statement"),
    "PSI-47": ("fumaca", "Detection · aerosols", "deteccao", 0.55, "Same instrument as SAME, measuring dust"),
    "PSI-115": ("fumaca", "Ground · laboratory", "deteccao", 0.50, "Ground-based use of the SAME smoke data"),
    "PSI-69": ("gota", "CIR · MDCA", "supressao", 0.90, "Suppression by dilution with CO₂ and helium; cited in the challenge statement"),
    "PSI-68": ("gota", "CIR · MDCA", "supressao", 0.75, "Continuation of FLEX with more fuels and flow"),
    "PSI-39": ("gota", "CIR · MDCA", "fundamento", 0.50, "Cool flames in droplets: reignition after the visible flame"),
    "PSI-117": ("gota", "Ground · laboratory", "fundamento", 0.35, "Droplets under thermal perturbation, on the ground"),
    "PSI-20": ("gas", "CIR · ACME", "prevencao", 0.80, "Emulates material flammability in a quiescent atmosphere; cited in the challenge statement"),
    "PSI-159": ("gas", "CIR · ACME", "fundamento", 0.70, "Gas flames in atmospheres up to 40% O₂ (its inverse flames feed up to 85% O₂ from the burner); cited in the challenge statement"),
    "PSI-22": ("gas", "CIR · ACME", "fundamento", 0.60, "Electric field mimics partial gravity; cited in the challenge statement"),
    "PSI-10": ("gas", "CIR · ACME", "fundamento", 0.45, "Structure of spherical flames and soot limits"),
    "PSI-21": ("gas", "CIR · ACME", "fundamento", 0.45, "Extinction of diluted coflow flames"),
    "PSI-23": ("gas", "CIR · ACME", "fundamento", 0.45, "Spherical flames and radiative extinction"),
    "PSI-107": ("gas", "MSG · flow duct", "deteccao", 0.60, "Smoke point: when the flame starts to release smoke"),
    "PSI-106": ("gas", "MSG · flow duct", "fundamento", 0.40, "Liftoff and structure of coflow flames"),
    "PSI-60": ("gas", "Ground · laboratory", "fundamento", 0.35, "Computational analysis of the SPICE data"),
    "PSI-62": ("solido", "Ground · laboratory", "prevencao", 0.55, "Modeling of flame spread over solids"),
    "PSI-142": ("gas", "Ground · laboratory", "fundamento", 0.40, "Cool flame transitions, on the ground"),
}
DECLARED = {  # faixas descritas na tabela quando ela não lista testes individuais
    "PSI-68": {"o2": [0.17, 0.30], "pressure_atm": [0.5, 3.0], "flow_cm_s": [0.1, 5.0]},
}


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def year(s):
    s = (s or "")[-4:]
    return int(s) if s.isdigit() else None


def stats_for(acc, tests):
    plot = [t for t in tests if t["o2"] is not None and t["pressure_atm"] is not None]
    o2 = [t["o2"] for t in tests if t["o2"] is not None]
    pr = [t["pressure_atm"] for t in tests if t["pressure_atm"] is not None and "pressure_atm" not in t["assumed"]]
    fl = [t["flow_cm_s"] for t in tests if t["flow_cm_s"]]
    varies, constant = [], []
    keys = Counter(k for t in tests for k in t["raw"])
    for k in keys:
        vals = {t["raw"].get(k, "") for t in tests if t["raw"].get(k, "") != ""}
        if not k or k.lower().startswith(("notes", "camera", "comments")):
            continue
        if len(vals) > 1 and len(vals) < max(3, 0.9 * len(tests)):
            varies.append(k)
        elif len(vals) == 1 and len(tests) > 1:
            constant.append(f"{k}: {next(iter(vals))}"[:80])
    outcomes = Counter(t["outcome"] for t in tests)
    s = {
        "n_tests": len(tests), "n_plot": len(plot),
        "o2": [min(o2), max(o2)] if o2 else None,
        "pressure_atm": [min(pr), max(pr)] if pr else None,
        "flow_cm_s": [min(fl), max(fl)] if fl else None,
        "gravity": sorted({t["gravity"] for t in tests}),
        "fuels": [f for f, _ in Counter(t["fuel"] for t in tests if t["fuel"]).most_common(8)],
        "materials": dict(Counter(t["material"] for t in tests if t["material"])),
        "outcomes": dict(outcomes),
        "has_o2": bool(o2), "has_pressure": bool(pr), "has_outcome": any(k != "desconhecido" for k in outcomes),
        "has_flow": bool(fl), "varies": varies[:10], "constant": constant[:8],
        "measurements": sorted({k for t in tests for k in t["metrics"]})[:14],
        "pressure_assumed": any("pressure_atm" in t["assumed"] for t in tests),
    }
    if acc in DECLARED:
        s["declared"] = DECLARED[acc]
    return s


def _sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def fingerprint(cats, tables, doc_files, resources):
    """Impressão digital de tudo o que entra no acervo: código da ingestão, configuração, catálogos, tabelas,
    documentos (nome e tamanho), recursos e semente. Igual à da última ingestão = nada a refazer."""
    h = hashlib.sha256()

    def add(x):
        h.update(json.dumps(x, sort_keys=True, ensure_ascii=False, default=str).encode())
        h.update(b"\0")

    here = os.path.dirname(os.path.abspath(__file__))
    add([(os.path.basename(p), _sha(p)) for p in sorted(glob.glob(os.path.join(here, "*.py")))
         if os.path.basename(p) != "server.py"])
    add([config.EMBED_MODEL, config.DOCS, config.DOC_CATEGORIES, config.DOC_MAX_MB, config.DOC_MAX_PAGES])
    add(cats)
    add([(acc, name, _sha(path)) for acc, name, path in tables])
    add([(acc, f["file_name"], os.path.getsize(path)) for acc, f, path in doc_files])
    add([(os.path.basename(p), os.path.getsize(p)) for p in resources])
    add([(os.path.relpath(p, config.SEED), _sha(p)) for p in sorted(glob.glob(os.path.join(config.SEED, "**", "*"), recursive=True))
         if os.path.isfile(p)])
    return h.hexdigest()


def _key(text):
    return hashlib.sha1(text.encode()).digest()


def reusable_vectors(conn, prev):
    """Vetores dos trechos do acervo publicado, por hash do texto embutido. Vazio se o modelo mudou."""
    if not prev or prev.get("embed_model") != config.EMBED_MODEL or config.FORCE:
        return {}
    rows = conn.execute(f"SELECT title || '. ' || text, embedding FROM {config.SCHEMA}.chunks").fetchall()
    return {_key(t): v for t, v in rows}


def published(conn):
    """Valor de meta.ingest no acervo publicado, ou None."""
    if not conn.execute(f"SELECT to_regclass('{config.SCHEMA}.meta')").fetchone()[0]:
        return None
    row = conn.execute(f"SELECT value FROM {config.SCHEMA}.meta WHERE key='ingest'").fetchone()
    return row[0] if row else None


def swap(conn):
    """Publica o acervo montado em BUILD_SCHEMA no lugar do atual, numa transação só."""
    live, build, old = config.SCHEMA, config.BUILD_SCHEMA, config.SCHEMA + "_old"
    with conn.transaction():
        conn.execute("SET LOCAL lock_timeout = '60s'")
        conn.execute(f"DROP SCHEMA IF EXISTS {old} CASCADE")
        if conn.execute("SELECT 1 FROM pg_namespace WHERE nspname = %s", (live,)).fetchone():
            conn.execute(f"ALTER SCHEMA {live} RENAME TO {old}")
        conn.execute(f"ALTER SCHEMA {build} RENAME TO {live}")
        # versões antigas gravavam as tabelas em public
        conn.execute("DROP TABLE IF EXISTS public.chunks, public.tests, public.publications, public.investigations, "
                     "public.models, public.meta CASCADE")
    # fora da transação: apagar o acervo antigo trava as tabelas dele, e dentro da troca isso enfileiraria as consultas
    # da API (até o lock_timeout). Depois do commit ninguém mais lê o antigo.
    conn.execute(f"DROP SCHEMA IF EXISTS {old} CASCADE")


# Sai com este código quando o acervo novo encolhe demais: o publicado fica no ar (deploy.sh e refresh.sh sabem disso).
SHRUNK_EXIT = 4


def shrunk(prev, info):
    """O acervo novo perdeu mais de 20% das investigações ou dos testes do publicado? (lista parcial do PSI, falha de rede)"""
    if not prev or config.FORCE:
        return None
    for key in ("investigations", "tests"):
        before, now = prev.get(key) or 0, info.get(key) or 0
        if before and now < 0.8 * before:
            return f"{key}: {now} agora contra {before} no publicado"
    return None


def collect():
    """Fase 1: descobre e baixa as entradas (catálogos, tabelas, documentos). Nada é gravado no banco."""
    accs, origin = psi.list_combustion()
    log(f"{len(accs)} investigações de combustão (lista: {origin})")
    cats, sources = {}, Counter()
    for acc in accs:
        cat, src = psi.catalog(acc)
        if cat:
            cats[acc] = cat
            sources[src] += 1
    log(f"catálogos: {dict(sources)}")

    tables, table_src, seen = [], Counter(), set()  # tables: (acc, nome, caminho)
    for acc, cat in cats.items():
        for f in cat.get("files", []):
            name = f["file_name"]
            if not name.lower().endswith(".csv") or "Experimental table" not in name or (acc, name) in seen:
                continue
            seen.add((acc, name))
            path, src = psi.table_path(acc, name, f.get("file_size"))
            table_src[src] += 1
            if path:
                tables.append((acc, name, path))
    if not table_src:  # catálogo sem lista de arquivos: usa as tabelas da semente
        for path in sorted(glob.glob(os.path.join(config.SEED, "tables", "*.csv"))):
            name = os.path.basename(path)
            tables.append((name.split("_")[0], name, path))
            table_src["semente"] += 1

    doc_files, doc_stats = [], Counter()  # doc_files: (acc, arquivo do catálogo, caminho)
    resources = sorted(glob.glob(os.path.join(config.RESOURCES, "*.pdf"))) if config.DOCS != "off" else []
    if config.DOCS != "off":
        budget = config.DOC_BUDGET_MB
        for acc, cat in cats.items():
            for f in cat.get("files", []):
                name, mb, size = f["file_name"], (f.get("file_size") or 0) / 1e6, f.get("file_size")
                if not name.lower().endswith(".pdf") or f.get("category") not in config.DOC_CATEGORIES or mb > config.DOC_MAX_MB:
                    continue
                if (acc, name) in seen:
                    continue
                seen.add((acc, name))
                ddir = os.path.join(config.CACHE, "docs", acc)
                path = os.path.join(ddir, psi.safe(name))
                if not psi.cached_ok(path, size):
                    if config.DOCS != "fetch" or budget < mb:
                        if not psi.cached_ok(path):
                            doc_stats["fora do cache"] += 1
                            continue
                        doc_stats["versão antiga em cache"] += 1
                    else:
                        got = psi.download(acc, name, ddir, config.DOC_MAX_MB, size=size)
                        if not got:
                            doc_stats["falhou no PSI"] += 1
                            continue
                        if psi.cached_ok(got, size):
                            budget -= mb
                            doc_stats["baixados"] += 1
                        else:
                            doc_stats["versão antiga em cache"] += 1
                        path = got
                doc_files.append((acc, f, path))
    return cats, sources, tables, table_src, doc_files, doc_stats, resources


def main():
    t0 = time.time()
    with psycopg.connect(config.DSN, autocommit=True) as conn:
        conn.execute("CREATE EXTENSION IF NOT EXISTS vector SCHEMA public")
        register_vector(conn)
        # uma ingestão por vez (deploy e atualização agendada podem coincidir); a segunda espera e confere
        conn.execute("SELECT pg_advisory_lock(hashtext('atlas-ingest'))")

        cats, sources, tables, table_src, doc_files, doc_stats, resources = collect()
        fp = fingerprint(cats, tables, doc_files, resources)
        prev = published(conn)
        if prev and prev.get("fingerprint") == fp and not config.FORCE:
            log(f"nada mudou desde {prev.get('at')} (impressão {fp[:12]}); acervo mantido. ATLAS_FORCE=1 refaz assim mesmo.")
            if config.UNCHANGED_EXIT:
                sys.exit(config.UNCHANGED_EXIT)
            return
        log(f"impressão {fp[:12]} ≠ {(prev or {}).get('fingerprint', 'nenhuma')[:12]}: montando o acervo em {config.BUILD_SCHEMA}")

        conn.execute(f"DROP SCHEMA IF EXISTS {config.BUILD_SCHEMA} CASCADE")
        conn.execute(f"CREATE SCHEMA {config.BUILD_SCHEMA}")
        conn.execute(f"SET search_path = {config.BUILD_SCHEMA}, public")
        conn.execute(TABLES)

        # 2. tabelas experimentais -> testes ------------------------------------------------
        tests, no_adapter, generic = [], [], []
        for acc, name, path in tables:
            parsed = adapters.parse(acc, name, path)
            if adapters.adapter_for(name) is None:  # tabela nova no PSI: lida pelo adaptador genérico, se der
                if parsed:
                    generic.append(f"{acc}: {name}")
                    log(f"  AVISO: {acc} · {name}: {len(parsed)} testes lidos pelo adaptador genérico "
                        f"({parsed[0]['metrics'].get('columns') or 'sem O₂/pressão'}); vale escrever um adaptador próprio")
                else:
                    no_adapter.append(f"{acc}: {name}")
                    log(f"  AVISO: {acc} · {name}: nenhum teste lido (o genérico não reconheceu as colunas)")
            tests += parsed
        ids = set()
        for t in tests:  # o mesmo rótulo pode aparecer em duas tabelas da mesma investigação
            while t["id"] in ids:
                t["id"] += "b"
            ids.add(t["id"])
        # desfechos lidos dos relatórios, para as tabelas que não dizem como o teste terminou
        outcome_docs = outcomes.apply(tests)
        by_inv = {}
        for t in tests:
            by_inv.setdefault(t["investigation_id"], []).append(t)
        log(f"{len(tests)} testes de {sum(table_src.values())} tabelas ({dict(table_src)})")

        # 3. grava investigações, publicações e testes -------------------------------------
        inv_text, inv_rows = [], []
        for acc, cat in cats.items():
            fam, hw, role, prio, why = META.get(acc, ("gas", "Other", "fundamento", 0.3, "General combustion"))
            name = cat.get("investigationAcronym") or (cat.get("title") or "")[:40]   # o catálogo traz None, não a chave ausente
            inv_text.append(" ".join(str(cat.get(k) or "") for k in ("title", "investigationAcronym", "objective", "approach", "researchImpacts"))[:1800])
            inv_rows.append((
                acc, cat.get("investigationAcronym") or "", cat.get("title") or "",
                "voo" if (cat.get("projectType") or "").startswith("Micro") else "solo",
                cat.get("subResearchArea") or "", cat.get("experimentHardware") or "", hw, fam, role, prio, why,
                year(cat.get("investigationStartDate")), year(cat.get("investigationEndDate")),
                round((cat.get("totalFileSize") or 0) / 1e9, 2), len(cat.get("publications") or []),
                cat.get("file_count") or 0, cat.get("objective") or "", cat.get("approach") or "",
                cat.get("hypothesis") or "", cat.get("researchImpacts") or "", cat.get("doi") or "",
                Jsonb(stats_for(acc, by_inv.get(acc, []))),
            ))
            cat["_name"] = name
        vec = embed(inv_text)
        with conn.cursor() as cur:
            cur.executemany(
                "INSERT INTO investigations VALUES (" + ",".join(["%s"] * 23) + ")",
                [r + (v,) for r, v in zip(inv_rows, vec)])
            cur.executemany("INSERT INTO publications (investigation_id,title,authors,doi) VALUES (%s,%s,%s,%s)",
                            [(acc, p.get("title", ""), p.get("authorList", ""), p.get("doi", ""))
                             for acc, cat in cats.items() for p in cat.get("publications") or []])
            cur.executemany(
                "INSERT INTO tests VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)",
                [(t["id"], t["investigation_id"], t["label"], t["family"], t["fuel"], t["material"], t["o2"],
                  t["pressure_atm"], t["gravity"], t["flow_cm_s"], t["co2"], t["he"], t["outcome"], t["outcome_raw"],
                  Jsonb(t["metrics"]), t["assumed"], t["source_file"], t["source_row"], Jsonb(t["raw"])) for t in tests
                 if t["investigation_id"] in cats])

        # 4. trechos: catálogo, fichas de teste, documentos e recursos ---------------------
        chunks = []  # (kind, inv, test_id, title, category, file, page, text)
        for acc, cat in cats.items():
            for field, label in (("objective", "Objective"), ("approach", "Approach"), ("hypothesis", "Hypothesis"),
                                 ("researchImpacts", "Impacts"), ("description", "Description")):
                text = (cat.get(field) or "").strip()
                for piece in docs.split(text) if len(text) > docs.CHUNK else ([text] if len(text) > 80 else []):
                    chunks.append(("catalogo", acc, None, f"{cat['_name']} · {label}", "PSI catalog", "PSI catalog", None, piece))
            pubs = "; ".join(p.get("title", "") for p in cat.get("publications") or [] if p.get("title"))
            for piece in docs.split("Publications: " + pubs) if len(pubs) > docs.CHUNK else ([("Publications: " + pubs)] if pubs else []):
                chunks.append(("catalogo", acc, None, f"{cat['_name']} · Publications", "PSI catalog", "PSI catalog", None, piece))
        for t in tests:
            if t["investigation_id"] in cats:
                chunks.append(("teste", t["investigation_id"], t["id"], f"{cats[t['investigation_id']]['_name']} · test {t['label']}",
                               "Experimental table", t["source_file"], t["source_row"], adapters.card(t, cats[t["investigation_id"]]["_name"])))
        n_struct = len(chunks)

        for acc, f, path in doc_files:
            title, category = docs.title_of(f["file_name"], acc)
            n0 = len(chunks)
            for page, piece in docs.pdf_chunks(path):
                chunks.append(("documento", acc, None, f"{cats[acc]['_name']} · {title}", f.get("category") or category, f["file_name"], page, piece))
            doc_stats["lidos" if len(chunks) > n0 else "sem texto"] += 1
        for path in resources:
            title, category = docs.title_of(os.path.basename(path))
            for page, piece in docs.pdf_chunks(path, max_pages=120):
                chunks.append(("recurso", None, None, title, category, os.path.basename(path), page, piece))
            doc_stats["recursos"] += 1
        log(f"{len(chunks)} trechos ({n_struct} de catálogo e fichas, {len(chunks) - n_struct} de documentos; {dict(doc_stats)})")

        # trechos iguais aos do acervo publicado (mesmo título e texto, mesmo modelo) reaproveitam o vetor:
        # uma novidade no PSI custa só os embeddings dela, não os ~20 mil do acervo inteiro
        known = reusable_vectors(conn, prev)
        B = 256
        computed = 0
        with conn.cursor() as cur:
            for i in range(0, len(chunks), B):
                part = chunks[i:i + B]
                texts = [f"{c[3]}. {c[7]}" for c in part]
                vecs = [known.get(_key(t)) for t in texts]
                todo = [j for j, v in enumerate(vecs) if v is None]
                if todo:
                    for j, v in zip(todo, embed([texts[j] for j in todo])):
                        vecs[j] = v
                    computed += len(todo)
                cur.executemany(
                    "INSERT INTO chunks (kind,investigation_id,test_id,title,category,source_file,page,text,embedding) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s)",
                    [c + (v,) for c, v in zip(part, vecs)])
                if (i // B) % 10 == 0:
                    log(f"  embeddings {min(i + B, len(chunks))}/{len(chunks)} ({computed} calculados, o resto reaproveitado)")
        log(f"embeddings: {computed} calculados, {len(chunks) - computed} reaproveitados do acervo publicado")
        conn.execute("CREATE INDEX ON chunks USING hnsw (embedding vector_cosine_ops)")
        conn.execute("CREATE INDEX ON chunks USING gin (tsv)")
        conn.execute("CREATE INDEX ON chunks (kind, investigation_id)")

        # 5. modelos ---------------------------------------------------------------------------
        fitted = models.fit_flex(tests)
        with conn.cursor() as cur:
            cur.executemany("INSERT INTO models VALUES (%s,%s)", [(m["id"], Jsonb(m["spec"])) for m in fitted])
        for m in fitted:
            log(f"modelo {m['id']}: n={m['spec']['n']} R²(cv)={m['spec']['r2_cv']}")

        info = {
            "at": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
            "investigations": len(cats), "tests": len(tests), "chunks": len(chunks),
            "catalog_sources": dict(sources), "table_sources": dict(table_src), "documents": dict(doc_stats),
            "embed_model": config.EMBED_MODEL, "seconds": round(time.time() - t0), "fingerprint": fp,
            "embeddings_computed": computed,
            "chunk_chars": docs.CHUNK, "tables_without_adapter": no_adapter, "tables_generic_adapter": generic,
            "investigations_without_profile": sorted(set(cats) - set(META)),
        }
        for acc, doc in outcome_docs.items():
            conn.execute("INSERT INTO meta VALUES (%s, %s)", (f"outcomes:{acc}", Jsonb(doc)))
        conn.execute("INSERT INTO meta VALUES ('ingest', %s)", (Jsonb(info),))
        diff = changes(conn, prev, cats, tests, doc_files)
        conn.execute("INSERT INTO meta VALUES ('changes', %s)", (Jsonb(diff),))
        if diff.get("since"):
            log(f"novidades desde {diff['since']}: {len(diff['new_investigations'])} investigações, "
                f"{sum(diff['new_tests'].values())} testes, {len(diff['new_documents'])} documentos")
        why = shrunk(prev, info)
        if why:
            log(f"RECUSADO: o acervo novo encolheu demais ({why}); o publicado continua no ar. "
                "Se a queda for real, rode com ATLAS_FORCE=1.")
            sys.exit(SHRUNK_EXIT)
        swap(conn)
        write_rag_manifest(cats, doc_files)
        log(f"acervo publicado em {config.SCHEMA} em {round(time.time() - t0)} s: {info}")


def changes(conn, prev, cats, tests, doc_files):
    """O que entrou desde o acervo publicado (para o cartão "Novidades do PSI")."""
    if not prev:
        return {"since": None}
    live = config.SCHEMA
    old_invs = {r[0] for r in conn.execute(f"SELECT id FROM {live}.investigations")}
    old_tests = Counter(r[0] for r in conn.execute(f"SELECT investigation_id FROM {live}.tests"))
    old_docs = {(r[0], r[1]) for r in conn.execute(
        f"SELECT DISTINCT investigation_id, source_file FROM {live}.chunks WHERE kind = 'documento'")}
    new_tests = Counter(t["investigation_id"] for t in tests if t["investigation_id"] in cats)
    # documento novo = gerou trechos agora e não tinha antes (PDF só de desenho, sem texto, não conta)
    now_docs = {(r[0], r[1]) for r in conn.execute(
        f"SELECT DISTINCT investigation_id, source_file FROM {config.BUILD_SCHEMA}.chunks WHERE kind = 'documento'")}
    name = lambda acc: cats[acc].get("investigationAcronym") or (cats[acc].get("title") or acc)[:40]
    return {
        "since": prev.get("at"),
        "new_investigations": [{"id": acc, "name": name(acc), "title": cats[acc].get("title") or ""}
                               for acc in sorted(set(cats) - old_invs)],
        "removed_investigations": sorted(old_invs - set(cats)),
        "new_tests": {acc: n - old_tests.get(acc, 0) for acc, n in sorted(new_tests.items()) if n > old_tests.get(acc, 0)},
        "new_documents": [{"inv": acc, "name": name(acc), "file": f["file_name"], "category": f.get("category") or ""}
                          for acc, f, _ in doc_files if (acc, f["file_name"]) in now_docs - old_docs],
    }


def write_rag_manifest(cats, doc_files):
    """Lista os PDFs do PSI que o Atlas tem em cache, para o RAG da equipe somar ao corpus dele os que faltam
    (rag/atlas_pg/bridge.py). Caminhos relativos a DATA; o hash identifica o arquivo nos dois lados."""
    out = []
    for acc, f, path in doc_files:
        cat = cats[acc]
        out.append({"psi_id": acc, "acronym": cat.get("investigationAcronym") or "", "investigation_title": cat.get("title") or "",
                    "file_name": f["file_name"], "category": f.get("category") or "", "file_size": os.path.getsize(path),
                    "path": os.path.relpath(path, config.DATA), "sha256": _sha(path),
                    "mtime": datetime.datetime.fromtimestamp(os.path.getmtime(path), datetime.timezone.utc).isoformat(timespec="seconds")})
    os.makedirs(os.path.join(config.DATA, "rag"), exist_ok=True)
    dest = os.path.join(config.DATA, "rag", "psi_docs.json")
    with open(dest + ".part", "w") as fh:
        json.dump(sorted(out, key=lambda d: (d["psi_id"], d["file_name"])), fh, ensure_ascii=False, indent=1)
    os.replace(dest + ".part", dest)
    log(f"manifesto do RAG: {len(out)} PDFs do PSI em data/rag/psi_docs.json")


if __name__ == "__main__":
    main()
