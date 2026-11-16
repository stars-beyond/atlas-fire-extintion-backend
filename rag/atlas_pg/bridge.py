"""Ponte do Atlas para o RAG da equipe: soma ao corpus do Carlin os PDFs do PSI que ele ainda não tem.

A ingestão do Atlas (brain/atlas/ingest.py) baixa os PDFs do PSI todo dia e publica data/rag/psi_docs.json com o hash
de cada um. O corpus do Carlin identifica os PDFs pelo mesmo hash (metadata.content_sha256). Aqui, cada PDF do
manifesto que não está no corpus vira páginas no formato do Carlin (os mesmos campos de "PSI technical asset"), e o
corpus completo (o dele + os extras) é gravado em FLAME_CORPUS. A saída é determinística: sem PDF novo, o corpus
gerado é idêntico e nada é reindexado.

O que não entra: fórmulas e tabelas de condições do Carlin (data/formulas_v2), que vêm de uma transcrição à parte.
"""
import hashlib
import json
import os
import re
from pathlib import Path

from pypdf import PdfReader

from backend.flame.pipeline import normalize

WARNING = ("Extração automática não validada cientificamente. Preserve números, unidades, negações e campos ausentes; "
           "fórmulas não foram recalculadas. Confira o original antes de uso quantitativo.")
METHOD = "atlas_bridge_pypdf_v1"


def _pages(path, max_pages=120):
    """(página, texto) com texto de verdade: páginas de desenho ou imagem ficam de fora, como na ingestão do Atlas."""
    try:
        reader = PdfReader(str(path))
    except Exception:
        return
    for i, page in enumerate(reader.pages):
        if i >= max_pages:
            break
        try:
            text = normalize(page.extract_text() or "")
        except Exception:
            continue
        if len(text) >= 200 and sum(c.isalpha() for c in text) >= 0.5 * len(text):
            yield i + 1, text


def _experiment_names(base):
    """psi_id -> nome do experimento como o Carlin escreve (ex.: PSI-10 -> "ACME Flame Design")."""
    names = {}
    for unit in base:
        m = unit["metadata"]
        if m.get("document_kind") == "technical_asset" and isinstance(m.get("psi_id"), str) and m.get("experiment"):
            names.setdefault(m["psi_id"], m["experiment"])
    return names


def extra_documents(base, manifest, data_root):
    known = {u["metadata"].get("content_sha256") for u in base}
    experiments = _experiment_names(base)
    extra, files = [], []
    for doc in manifest:
        if doc["sha256"] in known:
            continue
        path = Path(data_root) / doc["path"]
        if not path.is_file():
            continue
        acc, name = doc["psi_id"], doc["file_name"]
        meta = {
            "document_id": f"{acc.lower()}-asset-{doc['sha256'][:20]}", "psi_id": acc, "related_psi_ids": [acc],
            "title": name, "experiment": experiments.get(acc) or doc.get("acronym") or acc,
            "source": "NASA PSI", "source_type": f"PSI technical asset: {doc.get('category') or 'Documents'}",
            "document_kind": "technical_asset", "topic": "microgravity_combustion", "evidence_level": "full_text",
            "url": f"https://psi.nasa.gov/physci/repo/data/investigations/{acc}",
            "download_url": f"https://psi.nasa.gov/geode-py/ws/studies/{acc}/download?file={name}",
            "local_file": f"atlas:{doc['path']}", "content_sha256": doc["sha256"], "extraction_method": METHOD,
            "retrieved_at": doc.get("mtime"), "data_warnings": [WARNING],
        }
        n0 = len(extra)
        for page, text in _pages(path):
            extra.append({"content": text, "metadata": {**meta, "page": page}})
        if len(extra) > n0:
            files.append(f"{acc}: {name}")
    return extra, files


def build(base_path, out_path, manifest_path, data_root):
    """Grava em out_path o corpus do Carlin + extras. Devolve (documentos do Carlin, páginas extras, arquivos extras)."""
    base = json.loads(Path(base_path).read_text(encoding="utf-8"))
    extra, files = [], []
    if manifest_path and Path(manifest_path).is_file():
        extra, files = extra_documents(base, json.loads(Path(manifest_path).read_text(encoding="utf-8")), data_root)
    body = json.dumps(base + extra, ensure_ascii=False)
    out = Path(out_path)
    out.parent.mkdir(parents=True, exist_ok=True)
    if not out.is_file() or hashlib.sha256(out.read_bytes()).hexdigest() != hashlib.sha256(body.encode()).hexdigest():
        tmp = out.with_suffix(".part")
        tmp.write_text(body, encoding="utf-8")
        os.replace(tmp, out)
    return len(base), len(extra), files
