# 🔥 Extinction Atlas — Backend

**NASA Space Apps Challenge 2026 · Flame in Freefall** | Go · Python · FastAPI · Postgres 16 + pgvector · OpenAI / Claude · ONNX · Docker

> An atlas of what NASA has already burned in microgravity — and of what it hasn't.

This is the **main backend** of the project: database, ingestion, embeddings, RAG, models and the API that does every
calculation. The interface only renders and lives in
[`atlas-fire-extintion-front`](https://github.com/stars-beyond/atlas-fire-extintion-front) (Cloudflare Workers & Pages).

## 🌠 Overview

The **Extinction Atlas** reads the combustion collection of [NASA PSI](https://psi.nasa.gov) (Physical Sciences
Informatics), places every test on a map of **oxygen × pressure × gravity**, and answers a single question for any
crewed environment:

> **What experimental evidence exists for this scenario?**

The answer always comes with the evidence level, the tests that support it, and the file and line each one came from.

Why it matters: the challenge statement says the lunar base will have **30% oxygen or more, at 10.2 psi or less**. The
solid cabin materials in the PSI collection were tested between **13.6% and 22.2% O₂**, all close to 1 atm, and PSI has
**no data at partial gravity**. The Atlas shows that gap instead of hiding it.

The platform allows engineers, researchers, students and space enthusiasts to:

🔍 Describe a habitat (ready-made preset, manual controls or a free-text sentence) and see the NASA tests closest to it

🧭 Get a fixed-rule **evidence verdict** (Measured / Bounded / One-sided / Terra incognita) — never a safety claim

🧠 Ask questions in natural language and get answers with numbered sources (RAG over NASA documents)

📊 Explore 15 datasets, rank the 24 investigations for a scenario, and see which tests would close the most gaps

## 🛰️ Features

✨ **Automated PSI ingestion** — Catalogs, experimental tables and PDFs fetched from PSI (no login), normalized into one
test table that keeps the source file, line and raw record of every row.
🧩 **Evidence verdict** — A fixed rule over the tests decides the evidence level; the language model can never change it.
📈 **Empirical rate, not probability** — Under "Measured", the Atlas counts what happened in the real tests
(e.g. *0 of 4 silicone samples sustained a flame on the station, Saffire-II*). Outside "Measured" there is no number.
🔎 **Hybrid search** — Vector similarity + keyword search in pgvector, fused by rank (RRF), with a PT→EN glossary.
💬 **RAG Q&A with sources** — The team's RAG (`rag/`) answers over 18,436 chunks of PSI and NTRS documents, with
conversation history and streaming (Server-Sent Events).
🌡️ **Predictive models** — FLEX droplet burn rate (CO₂ / helium), flame-spread speed and a "thermal camera" from the
team's neural networks, each one with an explicit validity range.
🚀 **Mission engine** — Hour-by-hour mission simulation (Mission Fire Engine) for the site's story mode.
🔄 **Incremental refresh** — Only rebuilds the collection when something changed in PSI, swapping it atomically while
the API keeps serving the old data.
⚙️ **Containerized infrastructure** — Everything runs with Docker Compose; production uses blue/green deploys behind nginx.

## 🧭 How It Works

**1. Collect (PSI)**
→ `brain/atlas/psi.py` lists the combustion investigations and downloads each catalog and experimental table.
Lookup order: local cache (catalog valid for 24 h; a file is valid while its size matches the catalog) → PSI →
repository seed (`data/seed/`). Offline, the cache is used even when expired.

**2. Normalize (adapters)**
→ `brain/atlas/adapters.py` has one adapter per table. Every row becomes a test with the same columns, keeping file,
line and raw record. Inferred values are flagged (e.g. the pressure of solid tests is assumed to be cabin pressure).

**3. Documents & embeddings**
→ `brain/atlas/docs.py` turns PSI PDFs and challenge resources (`data/resources/`) into page-referenced chunks of up to
~420 characters (the local embedding model only reads 128 tokens). Embeddings are generated locally with fastembed
(`paraphrase-multilingual-MiniLM-L12-v2`) and stored in pgvector.

**4. Models**
→ `brain/atlas/models.py` fits a linear regression of the FLEX droplet burn rate, with cross-validation split by test
day and the range in which the model is valid.

**5. Verdict, search & answer**
→ The Go API computes the verdict, ranks investigations, runs hybrid search and forwards questions to the RAG. The
language model receives the finished verdict, the neighbouring tests and the document excerpts, and only **writes**
the answer with citations. If the search finds nothing similar, the Atlas says the subject is outside the collection.

### The verdict

A fixed rule in `api/domain/evidence/verdict.go` decides the evidence level from the tests:

| Level | When |
|---|---|
| **Measured** | There is a test in this atmosphere (±2 points of O₂, ±20% pressure) and at this gravity |
| **Bounded** | No test at this gravity, but there are tests at 0 g **and** at 1 g in this atmosphere |
| **One-sided** | There are tests on only one side (0 g or 1 g) |
| **Terra incognita** | No test in this atmosphere |

"Bounded" follows the challenge statement's own suggestion: microgravity and normal-gravity results bound what happens
at intermediate gravity. The verdict speaks about the **existence of evidence**, never about safety.

### Question answering

The Ask tab uses the first engine available, in this order:

| Engine | When | What it delivers |
|---|---|---|
| **Team RAG** (`rag/`) | `OPENAI_API_KEY` set | Answers over the `flame_freefall_2026_v6` collection in pgvector, with numbered sources, human-reviewed formulas, a table of conditions extracted from the texts, and conversation history |
| **Claude** | `ANTHROPIC_API_KEY` set | Answer written over the local pgvector search, via the official Go SDK, model `claude-opus-5-5` (`ATLAS_MODEL`) |
| **OpenAI-compatible endpoint** | `OPENAI_API_KEY`, with the RAG down | Same as above, with the model in `CHAT_MODEL` |
| **Extractive mode** | No key | Verdict, neighbouring tests and excerpts, with no generated text |

## 🔬 Core Components

| Component | Description |
|---|---|
| `db` | Postgres 16 + pgvector: tests, investigations and vector index (schemas `atlas` and `rag`) |
| `ingest` | Python: fetches PSI, normalizes the tables, reads the PDFs, generates embeddings and fits the models. Only rebuilds when something changed |
| `embedder` | Python (FastAPI + fastembed): embeds the question; also serves the thermal-camera ONNX networks and the mission engine |
| `rag-index` | Python: indexes the RAG corpus into pgvector (team corpus + PSI PDFs only the Atlas has). Only embeds what is new |
| `rag` | Python, FastAPI, OpenAI: the team's RAG ([rag-Carlin](https://github.com/stars-beyond/rag-Carlin)), reading from pgvector |
| `api` | Go: verdict, ranking, hybrid search, predictions, datasets, charts and CSV; forwards questions to `rag` |

There is no external vector database: the Atlas collection and the RAG collection live in the same Postgres. Qdrant
was removed from the project.

# Requirements & Startup

## Repository layout (quick map)

* `api/` → Go API (module `atlas-api`): domain, use cases, gateways and HTTP handlers
* `brain/` → Python ingestion, embedder service, thermal camera (ONNX) and mission engine (`brain/atlas/mission`)
* `rag/` → the team's RAG backend (rag-Carlin, copied unchanged; `scripts/sync_rag.sh` updates it) + `atlas_pg/` bridge to pgvector
* `ml/` → data manifest, registry and model scripts (see [ml/README.md](ml/README.md) and [ml/PIPELINE.md](ml/PIPELINE.md))
* `data/seed/` → offline seed: lean catalogs, the 22 experimental tables, simulator config, external (ZARM) tests
* `scripts/` → seed, external-tests and outcome extraction utilities
* `deploy/` → production: blue/green scripts, nginx, systemd timers (see [deploy/README.md](deploy/README.md))
* `docs/PITCH.md` → project pitch

## Requirements

### System

* Docker & Docker Compose (runs everything)
* Optional, for local development without containers: Go 1.25+ (CI uses 1.26) and Python 3.12

### Environment variables (`.env`, copied from `.env.example`)

All AI keys are **optional**. Without any key the Atlas answers in extractive mode.

```
# Claude (optional)
ANTHROPIC_API_KEY=
ATLAS_MODEL=claude-opus-5-5

# OpenAI-compatible endpoint (used only if there is no Anthropic key) — also used by the team RAG
OPENAI_API_KEY=
OPENAI_BASE_URL=
CHAT_MODEL=

# Team RAG
EMBEDDING_MODEL=text-embedding-3-large
RAG_COLLECTION=flame_freefall_2026_v6

# Host ports
API_PORT=8787
DB_PORT=54329

# Ingestion
ATLAS_NETWORK=auto      # auto | off
ATLAS_DOCS=fetch        # fetch | cache | off
ATLAS_DOC_BUDGET_MB=700
ATLAS_FORCE=0           # 1 rebuilds the ingestion from scratch
```

## Running locally (backend + frontend, step by step)

Two repositories, two terminals. The backend must be up **before** the frontend: every screen reads from the API.

```
~/…/atlas-fire-extintion-backend   →  Docker: db :54329, api :8787 (embedder, ingest, rag inside the network)
~/…/atlas-fire-extintion-front     →  Vite :5173, proxies /api → http://localhost:8787
```

### 0) Prerequisites

* Docker Desktop **running** (`docker ps` must answer without error)
* Node.js 22.13+ for the frontend
* Both repositories cloned side by side (same parent folder)
* Free ports: `8787` (API), `54329` (Postgres), `5173` (frontend). Change the first two in `.env` (`API_PORT`, `DB_PORT`)

### 1) Backend (terminal 1, in `atlas-fire-extintion-backend/`)

```bash
cp .env.example .env              # keys are optional; without them the Atlas answers in extractive mode
docker compose up --build -d      # or: make up
docker compose logs -f ingest     # wait for the ingestion to finish (first run: a few minutes)
```

The startup order is automatic: `db` → `embedder` + `ingest` → `rag-index` → `rag`, and the `api` only starts after
`ingest` finishes successfully. Check that everything is up:

```bash
docker compose ps                         # ingest and rag-index show "Exited (0)": that is expected
curl http://localhost:8787/api/health     # must answer
curl http://localhost:8787/api/meta       # counts > 0 means the collection was loaded
```

### 2) Frontend (terminal 2, in `atlas-fire-extintion-front/`)

```bash
npm install
npm run dev                       # http://localhost:5173
```

Do **not** set `VITE_API_URL` for local testing: left empty, the app calls `/api` and Vite forwards it to
`http://localhost:8787`, so there is no CORS to configure. Open <http://localhost:5173>; the health check through the
proxy is <http://localhost:5173/api/health>.

### 3) Day-to-day commands

| Command | What it does |
|---|---|
| `docker compose down` (`make down`) | Stops everything; the database stays in the `pgdata` volume, so the next start is fast |
| `make logs` | Follows the logs of every service |
| `make refresh` | Looks for news in PSI; only rebuilds the collection (and restarts the API) if something changed |
| `make reingest` | Rebuilds the collection from scratch and restarts the API |
| `docker compose down -v` | Also **erases** the database and the RAG state (the next start redoes ingestion and embeddings) |

### Variant: API outside Docker (to debug the Go code)

The `embedder` does not publish its port, so start it with one, and run the rest of the stack without the containerized API:

```bash
docker compose up -d db ingest                                          # wait for ingest to finish (logs -f ingest)
docker compose run -d --rm --name atlas-embedder -p 8000:8000 embedder  # embedder on localhost:8000
cd api && go run ./service                                              # API on :8787 (Go 1.25+)
```

Run `go run` from `api/`: the defaults (`DATABASE_URL` on `localhost:54329`, `EMBEDDER_URL` on `localhost:8000`,
`SIM_CONFIG` and `EXTERNAL_TESTS` under `../data/seed/`) assume that folder. The team RAG stays off in this mode
(`RAG_URL` empty), so the Ask tab uses local search or the Claude/OpenAI key from your shell. If the containerized
`api` is still running, stop it first (`docker compose stop api`) to free port 8787.

### Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `Cannot connect to the Docker daemon` | Docker Desktop is not open |
| `port is already allocated` | Something else uses 8787/54329: stop it or change `API_PORT`/`DB_PORT` in `.env` (and use `ATLAS_API=http://localhost:<port> npm run dev` in the frontend) |
| `api` never starts | `ingest` failed: `docker compose logs ingest`. Without network it falls back to `data/seed/`; force it with `ATLAS_NETWORK=off` |
| Frontend shows "did not answer with data" | The API is not up on 8787, or `VITE_API_URL` is set in a `.env.local` of the frontend |
| Ask tab answers without generated text | No AI key in `.env` (expected: extractive mode). Add `OPENAI_API_KEY` and run `docker compose up -d` again |

`scripts/make_seed.py` rebuilds the seed from a previous PSI download.

---

## Data refresh (optional)

Each run, ingestion renews PSI catalogs older than 24 h (`ATLAS_CATALOG_TTL_H`), downloads new or replaced files (the
size in the catalog changes) and computes a **fingerprint** of everything that goes into the collection: catalogs,
tables, documents, seed, resources and the ingestion code itself.

* Same fingerprint as the published collection → nothing is redone.
* Different → the new collection is built in schema `atlas_build` and swapped with the published one (`atlas`) in a
  single transaction. The API keeps answering with the old data while the new one is generated.

In production a timer runs this daily ([deploy/refresh.sh](deploy/refresh.sh)); locally, `make refresh`.

Collection used during development (2026-11-14): **24 investigations, 1,972 tests (1,153 with O₂ and pressure), 9,468
indexed chunks.**

---

## 🌐 API

| Route | Purpose |
|---|---|
| `GET /api/health` | Health check |
| `GET /api/meta` | Counts, habitats, tolerances, AI provider |
| `GET /api/investigations`, `/api/investigations/{id}` | Investigations and full record |
| `GET /api/tests`, `/api/tests/{id}` | Tests (compact) and one test with its origin and raw record |
| `GET /api/external` | Non-NASA tests: 53 ZARM drop-tower tests (thin PMMA film at 0 g). Never counted in the verdict |
| `POST /api/verdict` | Evidence level of a scenario, neighbouring tests and matrix per fuel family |
| `POST /api/rank` | Ranking of the investigations for the scenario, with the score broken into four parts |
| `POST /api/next-tests` | Tests that would most improve the verdicts of the exploration habitats |
| `POST /api/graph` | Relationship graph between the scenario and the most similar tests |
| `GET /api/insights` | Findings computed over the tests |
| `POST /api/search` | Hybrid search |
| `POST /api/ask` | Natural-language question, answer with citations |
| `POST /api/ask/stream` | Same question over Server-Sent Events: verdict first, text chunks while the RAG writes, validated answer at the end |
| `POST /api/scenario/infer` | Free sentence → scenario (habitat, material, O₂, pressure, gravity) |
| `GET /api/flammability`, `POST /api/flammability/assess`, `GET /api/flammability/queue` | NASA flammability curve, scenario assessment and the "Next fire" test queue |
| `POST /api/predict/burn-rate` | FLEX droplet burn rate with CO₂ or helium, and whether it is in range |
| `POST /api/predict/spread` | Flame-spread speed on a thin acrylic film at 0 g (team model [Atlas-MachineL](https://github.com/stars-beyond/Atlas-MachineL)); no number outside the model grid |
| `POST /api/predict/thermal` | Thermal camera: predicted surface-temperature film (63 frames, 7.6 s) for the scenario's air |
| `GET /api/sim/config`, `GET /api/sim/validation` | Physical parameters for the 3D flame lab and its validation against ZARM |
| `GET /api/datasets`, `/api/datasets/{id}/records` | Datasets and records for the chart builder, with source file and line |
| `POST /api/datasets/{id}/chart`, `GET /api/datasets/{id}/csv` | Chart data and CSV export |
| `GET /api/missions/presets`, `POST /api/missions/simulations` | Mission engine: presets and hour-by-hour simulation (422 if the mission is impossible, 503 if the engine does not respond) |

## 🗂️ API code organization

The Go API follows layers: the domain knows neither HTTP nor the database.

```
api/
├─ service/
│  ├─ main.go            config → gateways → use cases → handlers → server (graceful shutdown on SIGTERM)
│  ├─ configs/           environment variables
│  └─ api/v1/            api.go (all routes) and one handler package per feature
├─ domain/
│  ├─ entities/          types: scenario, collection, verdict, questions, datasets, flammability…
│  ├─ evidence/          verdict rule, empirical rate, ranking, next tests, graph, findings
│  ├─ ask/               questions (with and without streaming), scenario inference, citations
│  ├─ search/            hybrid search in pgvector (vector + keyword)
│  ├─ flammability/      NASA curve, scenario assessment, Next-fire queue
│  ├─ datasets/          chart builder and CSV
│  ├─ catalog/ sim/ predict/
│  └─ ptbr/              Portuguese numbers and labels
├─ gateways/             postgres, embedder, rag, llm, simfile
└─ extensions/           zweb (JSON, errors, CORS, SSE), zhttp, zpostgres
```

Each use case declares the interfaces it needs (repository, language model, RAG…) and `service/main.go` wires the
implementations from `gateways/`.

---

## 🚢 CI/CD & production

```
atlas-stars-beyond.tech (Cloudflare Worker, static files only)
        │ the browser calls the API directly (CORS)
        ▼
https://api.atlas-stars-beyond.tech (Cloudflare)
        │ only Cloudflare IPs get through
        ▼
VPS :443 nginx ─► api-blue | api-green ─► db, embedder, rag
```

* **CI** ([.github/workflows/ci.yml](.github/workflows/ci.yml)): on every PR and push to `main`/`dev` it builds and
  tests the Go API, checks that the Python code compiles, and validates the deploy (shell scripts, compose files and
  `nginx -t`). A separate `scan` job (formatting, `go test -race`, govulncheck, gitleaks, trivy) warns but does not
  block.
* **Gate**: when `api`, `python` and `deploy` pass on a push to `main`, the `release` job advances the `release` branch.
* **CD**: a systemd timer on the VPS runs [deploy/deploy.sh](deploy/deploy.sh) every 30 s and deploys `release` with
  blue/green, checking `/api/health` and `/api/meta` before switching nginx and rolling back automatically on failure.

Full details in [deploy/README.md](deploy/README.md).

---

## ⚠️ Known limitations

- **Solid outcomes.** The BASS and BASS-II tables do not say whether the sample burned or went out (that is in free
  text and in the Summary Report). These tests appear as "not tabulated".
- **Solid pressure.** Assumed to be cabin pressure (1 atm) and flagged as inferred.
- **Heptane model.** R² of 0.30 in cross-validation — a trend, not a prediction. The methanol model has 0.86.
- **Documents.** 94 of the listed PDFs have no extractable text and 7 fail on PSI itself. Ten investigations (including
  FLEX and Saffire-II) have no PDF in the catalog.
- **Non-NASA evidence.** The 53 ZARM tests (`data/seed/external/zarm.json`, CC-BY-NC-4.0) are kept in a separate,
  always-labelled list. Seal, ranking, findings and counts only speak about PSI.
- **Spread speed & thermal camera.** Trained on 51 ZARM drop-tower recordings: one material (0.075 mm PMMA film), 0 g,
  opposed flow, 21–36% O₂, 60–103 kPa, 9–20 cm/s. Gravity is not an input and the model does not predict extinction,
  so the API returns no number far from the tests. The verdict never depends on it.
- **3D simulator (lab3d).** A study model with provisional parameters, not calibrated against PSI tests; it runs 1.2 to
  6.2 times faster than the ZARM measurements. Always shown with that warning; the verdict never depends on it.
- **RAG.** Depends on OpenAI (question embedding and writing); without network or key the Ask tab falls back to local
  search.
- **Claude.** The Anthropic path compiles and follows the SDK documentation but was not exercised in the development
  environment, which had no key. The RAG and OpenAI paths were tested.
- **No login.** Everything is read-only public data.

---

## 📍 Ports & URLs (default)

* API: `http://localhost:8787` (health `/api/health`)
* DB: Postgres + pgvector on `localhost:54329` (user/password/db `atlas`)
* Frontend: `http://localhost:5173` (from `atlas-fire-extintion-front`, `npm run dev`)

---

Data: NASA Physical Sciences Informatics (CC0). ZARM drop-tower data: CC-BY-NC-4.0. This project is not affiliated with NASA.
