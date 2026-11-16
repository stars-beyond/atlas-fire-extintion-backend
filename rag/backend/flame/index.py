"""Index a Flame corpus without deleting collections; identical reruns skip embeddings."""
import argparse
import hashlib
import json
import os
import uuid
from pathlib import Path
from dotenv import load_dotenv
from .config import ENV_FILE, openai_client, settings, vector_client
from .pipeline import write_json

def prepare(documents, size=1500, overlap=250):
    if not 0 <= overlap < size:
        raise ValueError("Invalid chunk overlap")
    chunks = []
    for document in documents:
        meta = document["metadata"]
        if meta.get("topic") != "microgravity_combustion" or meta.get("evidence_level") not in {"full_text", "abstract_only"}:
            raise ValueError("Unverified or unrelated document in corpus")
        content = document["content"].strip()
        for start in range(0, len(content), size - overlap):
            chunk = content[start:start + size]
            if not chunk.strip():
                continue
            stable_key = f'{meta["document_id"]}:{meta.get("page")}:{meta.get("row")}:{start}:{chunk}'
            chunks.append({"id": str(uuid.uuid5(uuid.NAMESPACE_URL, stable_key)), "page_content": chunk, "metadata": {**meta, "chunk_offset": start}})
            if start + size >= len(content):
                break
    return list({c["id"]: c for c in chunks}.values())

def fingerprint(chunks):
    # Timestamps are incidental; changes to provenance/warnings invalidate the revision.
    stable = [{"id": c["id"], "metadata": {k:v for k,v in c["metadata"].items() if k not in {"retrieved_at"}}} for c in chunks]
    return hashlib.sha256(json.dumps(sorted(stable, key=lambda c:c["id"]), sort_keys=True, ensure_ascii=False).encode()).hexdigest()

def validate_collection(name):
    if not name.startswith("flame_freefall_2026"):
        raise ValueError("Use a separate collection beginning with flame_freefall_2026")

def embedding_text(chunk):
    meta = chunk["metadata"]
    return f'{meta.get("title", "")}\nExperiment: {meta.get("experiment", "")}\n{chunk["page_content"]}'

def ensure_filter_indexes(client, collection):
    """Qdrant Cloud strict mode requires indexes for every retrieval filter."""
    from qdrant_client import models
    schema = client.get_collection(collection).payload_schema
    for field in ("metadata.experiment", "metadata.psi_id", "metadata.section", "metadata.document_role"):
        if field in schema:
            if schema[field].data_type != models.PayloadSchemaType.KEYWORD:
                raise ValueError(f"Incompatible payload index for {field}; use a new collection")
            continue
        client.create_payload_index(collection, field_name=field,
                                    field_schema=models.PayloadSchemaType.KEYWORD, wait=True)

def index_chunks(chunks, client, embed, collection, model, batch_size=48):
    from qdrant_client import models
    if type(batch_size) is not int or not 1 <= batch_size <= 256:
        raise ValueError("Batch size must be an integer between 1 and 256")
    if not chunks:
        raise ValueError("Empty corpus; nothing indexed")
    validate_collection(collection)
    revision = fingerprint(chunks)
    exists = client.collection_exists(collection)
    if exists:
        prior, _ = client.scroll(collection, limit=1, with_payload=True, with_vectors=False)
        if prior and (prior[0].payload.get("corpus_revision") != revision or prior[0].payload.get("embedding_model") != model):
            raise ValueError("Different corpus/model already present. Use a new flame_freefall_2026_* collection. Nothing deleted.")
        vectors_config = client.get_collection(collection).config.params.vectors
        if not hasattr(vectors_config, "size") or vectors_config.distance != models.Distance.COSINE:
            raise ValueError("Existing collection must use unnamed cosine vectors")
    embedded = 0
    for start in range(0, len(chunks), batch_size):
        batch = chunks[start:start + batch_size]
        present = {str(p.id):p for p in client.retrieve(collection, [c["id"] for c in batch], with_payload=True, with_vectors=False)} if exists else {}
        for point in present.values():
            if point.payload.get("corpus_revision") != revision or point.payload.get("embedding_model") != model:
                raise ValueError("Inconsistent collection revision; use a new collection")
        pending = [c for c in batch if c["id"] not in present]
        if not pending:
            continue
        vectors = embed([embedding_text(c) for c in pending])
        if len(vectors) != len(pending) or not vectors or not vectors[0]:
            raise ValueError("Invalid embeddings response")
        if not exists:
            client.create_collection(collection, vectors_config=models.VectorParams(size=len(vectors[0]), distance=models.Distance.COSINE))
            exists = True
        elif client.get_collection(collection).config.params.vectors.size != len(vectors[0]):
            raise ValueError("Incompatible embedding dimensions")
        points = [models.PointStruct(id=c["id"], vector=v, payload={"page_content":c["page_content"], "metadata":c["metadata"], "corpus_revision":revision, "embedding_model":model}) for c,v in zip(pending,vectors)]
        client.upsert(collection, points=points, wait=True)
        embedded += len(points)
        print(f"Indexed {min(start + batch_size, len(chunks))}/{len(chunks)}", flush=True)
    if client.count(collection, exact=True).count != len(chunks):
        raise ValueError("Unexpected points in collection; use a new collection")
    return {"chunks":len(chunks), "embedded_now":embedded, "collection":collection, "fingerprint":revision, "embedding_model":model, "ready":True}

def run(args):
    load_dotenv(args.env)
    config = settings()
    corpus = args.corpus or config["corpus"]
    if not corpus.is_file():
        raise ValueError(f"Corpus absent: {corpus}. Run backend.flame.ingest_local first.")
    chunks = prepare(json.loads(corpus.read_text(encoding="utf-8")))
    if not chunks:
        raise ValueError("Empty corpus; nothing indexed")
    collection = args.collection or config["collection"]
    validate_collection(collection)
    if args.dry_run:
        report = {"chunks":len(chunks), "documents":len({c["metadata"]["document_id"] for c in chunks}), "characters":sum(len(embedding_text(c)) for c in chunks), "collection":collection, "fingerprint":fingerprint(chunks), "external_calls":0}
        print(json.dumps(report))
        return report
    if not config["api_key"]:
        raise ValueError("Set OPENAI_API_KEY in backend/aplication/.env; first use --dry-run")
    client, llm = vector_client(config), openai_client(config)
    # Bulk indexing can wait for transient API rate limits. Chat retains its
    # separate zero-retry client and request lifecycle.
    llm.max_retries = 3
    try:
        def embed(texts):
            result = llm.embeddings.create(model=config["embedding_model"], input=texts)
            return [item.embedding for item in sorted(result.data, key=lambda d:d.index)]
        report = index_chunks(chunks, client, embed, collection, config["embedding_model"],
                              batch_size=getattr(args, "batch_size", 48))
        if config.get("qdrant_url"):
            ensure_filter_indexes(client, collection)
        write_json(corpus.parent / "index_report.json", report)
        print(json.dumps(report))
        return report
    finally:
        client.close()
        llm.close()

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--corpus", type=Path)
    parser.add_argument("--env", type=Path, default=ENV_FILE)
    parser.add_argument("--collection")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--batch-size", type=int, choices=range(1, 257), default=48)
    args = parser.parse_args()
    try:
        run(args)
    except (ValueError, RuntimeError) as error:
        parser.exit(1, f"{error}\n")

if __name__ == "__main__":
    main()
