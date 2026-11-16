"""Shared configuration; all paths are independent of the current directory."""
import os
from pathlib import Path
from dotenv import load_dotenv

ROOT = Path(__file__).resolve().parents[2]
ENV_FILE = ROOT / "backend/aplication/.env"
load_dotenv(ENV_FILE)

def data_path(value):
    path = Path(value).expanduser()
    return path if path.is_absolute() else ROOT / path

def settings():
    return {
        "api_key": os.getenv("OPENAI_API_KEY", "").strip(),
        "base_url": os.getenv("OPENAI_BASE_URL", "").strip() or None,
        "embedding_model": os.getenv("EMBEDDING_MODEL", "text-embedding-3-large"),
        "chat_model": os.getenv("CHAT_MODEL", "gpt-6-luna"),
        "collection": os.getenv("QDRANT_COLLECTION_NAME", "flame_freefall_2026_local_v1"),
        "qdrant_url": os.getenv("QDRANT_URL") or None,
        "qdrant_api_key": os.getenv("QDRANT_API_KEY") or None,
        "qdrant_path": data_path(os.getenv("QDRANT_PATH", "data/qdrant")),
        "corpus": data_path(os.getenv("FLAME_CORPUS", "data/flame_local/corpus.json")),
        "threshold": float(os.getenv("RETRIEVAL_MIN_SCORE", "0.2")),
        "formulas": data_path(os.getenv("FLAME_FORMULAS", "data/formulas_v2/full/catalog.jsonl")),
    }

def vector_client(config):
    from qdrant_client import QdrantClient
    if config["qdrant_url"]:
        return QdrantClient(url=config["qdrant_url"], api_key=config["qdrant_api_key"], timeout=30)
    config["qdrant_path"].mkdir(parents=True, exist_ok=True)
    # API queries run in FastAPI worker threads; indexing is a separate process.
    return QdrantClient(path=str(config["qdrant_path"]), force_disable_check_same_thread=True)

def openai_client(config):
    from openai import OpenAI
    # Passing None lets the SDK read OPENAI_BASE_URL again, including an empty
    # value from our .env template. Supply the official endpoint explicitly.
    return OpenAI(api_key=config["api_key"], base_url=config["base_url"] or "https://api.openai.com/v1", timeout=40, max_retries=0)
