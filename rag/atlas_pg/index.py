"""Indexação automática do acervo do RAG no pgvector (serviço rag-index do docker compose).

  python -m atlas_pg.index

Roda antes do serviço rag:
1. Monta o corpus: o do Carlin (FLAME_CORPUS_BASE) + os PDFs do PSI que o Atlas baixou e o Carlin ainda não tem
   (atlas_pg/bridge.py), gravado em FLAME_CORPUS, que é o que o rag lê.
2. Indexa com a rotina do próprio Carlin (backend/flame/index.py) e resolve sozinho o que lá exige gente:
   - corpus mudou (PDF novo, corpus.json novo): grava a revisão nova nos pontos que continuam, apaga os que saíram e
     gera embedding só dos trechos novos;
   - modelo de embedding mudou: refaz do zero;
   - indexação parou no meio: continua de onde parou (os trechos já gravados não são pagos de novo).
Nunca falha a subida: sem chave da OpenAI ou com erro, avisa e sai com 0, e o rag sobe dizendo o que falta.
"""
import argparse
import json
import os
import sys
import time

import atlas_pg
from atlas_pg import bridge
from backend.flame import index as carlin
from backend.flame.config import ENV_FILE, settings


def log(msg):
    print(f"[rag-index {time.strftime('%H:%M:%S')}] {msg}", flush=True)


def build_corpus(config):
    base = os.environ.get("FLAME_CORPUS_BASE")
    if not base or str(config["corpus"]) == base:
        return
    manifest = os.environ.get("ATLAS_PSI_MANIFEST", "")
    n_base, n_extra, files = bridge.build(base, config["corpus"], manifest, os.environ.get("ATLAS_DATA", "/atlas-data"))
    log(f"corpus: {n_base} páginas do Carlin + {n_extra} páginas de {len(files)} PDFs do PSI que só o Atlas tinha")
    for f in files[:10]:
        log(f"  + {f}")
    if len(files) > 10:
        log(f"  + ... e mais {len(files) - 10}")


def main():
    config = settings()
    build_corpus(config)  # mesmo sem chave: o rag precisa do arquivo para dizer o que falta
    if not config["api_key"]:
        log("sem OPENAI_API_KEY: o RAG da equipe fica indisponível (a API usa a busca local)")
        return
    collection, model = config["collection"], config["embedding_model"]
    chunks = carlin.prepare(json.loads(config["corpus"].read_text(encoding="utf-8")))
    revision = carlin.fingerprint(chunks)
    client = atlas_pg.vector_client(config)
    try:
        if client.collection_exists(collection):
            points, _ = client.scroll(collection, limit=1)
            if points and points[0].payload.get("embedding_model") != model:
                log(f"{collection}: outro modelo de embedding; refazendo do zero")
                client.delete_collection(collection)
            else:
                if points and points[0].payload.get("corpus_revision") != revision:
                    kept, gone = client.restamp(collection, chunks, revision, model)
                    log(f"{collection}: corpus mudou; {kept} trechos mantidos, {gone} removidos")
                count = client.count(collection).count
                if count == len(chunks):
                    log(f"{collection}: {count} trechos indexados ({model}); nada a gerar")
                    return
                log(f"{collection}: {count} de {len(chunks)} trechos; gerando os {len(chunks) - count} que faltam")
    finally:
        client.close()
    t0 = time.time()
    log(f"indexando em {collection} (pgvector) com {model}")
    carlin.run(argparse.Namespace(env=ENV_FILE, corpus=None, collection=None, dry_run=False, batch_size=128))
    log(f"pronto em {round(time.time() - t0)} s")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # a subida segue; o rag mostra "indexação incompleta" e a API usa a busca local
        log(f"FALHOU: {type(e).__name__}: {e}")
    sys.exit(0)
