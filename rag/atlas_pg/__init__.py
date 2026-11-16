"""Camada do Atlas sobre o RAG do Carlin (rag/backend, copiado sem alterações pelo scripts/sync_rag.sh).

Troca o armazenamento vetorial do Carlin (Qdrant) pelo pgvector do Atlas (atlas_pg/store.py): as coleções ficam no
mesmo Postgres que o resto do acervo, sem serviço externo. Precisa ser importado antes de qualquer outro módulo do
backend, porque eles fazem "from backend.flame.config import vector_client".
"""
import os

from backend.flame import config as _config


def vector_client(config):
    from .store import PgVectorClient
    return PgVectorClient(os.environ.get("RAG_PG_URL", "postgresql://atlas:atlas@db:5432/atlas"))


_config.vector_client = vector_client
