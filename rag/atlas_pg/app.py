"""Ponto de entrada do serviço rag: o app do Carlin com as coleções no pgvector do Atlas."""
import atlas_pg  # noqa: F401  (troca o vector_client antes de o backend importá-lo)
from backend.aplication.main import app  # noqa: E402,F401
