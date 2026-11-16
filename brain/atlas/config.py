import os

DATA = os.environ.get("ATLAS_DATA", "/data")
SEED = os.path.join(DATA, "seed")
CACHE = os.path.join(DATA, "cache")
RESOURCES = os.path.join(DATA, "resources")
DSN = os.environ.get("DATABASE_URL", "postgresql://atlas:atlas@db:5432/atlas")
EMBED_MODEL = os.environ.get("EMBED_MODEL", "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2")
EMBED_DIM = 384

PSI = "https://psi.nasa.gov/geode-py/ws"
# rede: "auto" tenta o PSI e cai para cache/semente; "off" nunca sai para a rede
NETWORK = os.environ.get("ATLAS_NETWORK", "auto")
# documentos: "fetch" baixa o que faltar; "cache" usa só o que já está em disco; "off" ignora PDFs
DOCS = os.environ.get("ATLAS_DOCS", "fetch")
DOC_CATEGORIES = tuple(
    c.strip() for c in os.environ.get(
        "ATLAS_DOC_CATEGORIES", "Reports,Science Documents,Presentations,Reference Data,Analyzed Data,Engineering Documents"
    ).split(",")
)
DOC_MAX_MB = float(os.environ.get("ATLAS_DOC_MAX_MB", "30"))
DOC_BUDGET_MB = float(os.environ.get("ATLAS_DOC_BUDGET_MB", "700"))
DOC_MAX_PAGES = int(os.environ.get("ATLAS_DOC_MAX_PAGES", "80"))
FORCE = os.environ.get("ATLAS_FORCE", "0") == "1"
# catálogo do PSI em cache vale por estas horas; depois é buscado de novo (arquivos novos entram)
CATALOG_TTL_H = float(os.environ.get("ATLAS_CATALOG_TTL_H", "24"))
# código de saída quando nada mudou desde a última ingestão (o refresh agendado usa 3; o compose usa 0)
UNCHANGED_EXIT = int(os.environ.get("ATLAS_UNCHANGED_EXIT", "0"))

# O acervo vive no schema SCHEMA. A ingestão monta tudo em BUILD_SCHEMA e troca os dois numa transação:
# a API segue respondendo com os dados antigos enquanto os novos são gerados.
SCHEMA = "atlas"
BUILD_SCHEMA = "atlas_build"
SEARCH_PATH = f"{SCHEMA},public"


def connect(**kw):
    """Conexão com o acervo publicado (schema atlas; public para o tipo vector)."""
    import psycopg
    return psycopg.connect(DSN, options=f"-c search_path={SEARCH_PATH}", **kw)
