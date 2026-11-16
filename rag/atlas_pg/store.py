"""Coleções do RAG do Carlin guardadas no Postgres (pgvector), no lugar do Qdrant.

Implementa só a parte do QdrantClient que o rag-Carlin usa (backend/aplication/services.py e backend/flame/index.py),
com a mesma semântica: distância cosseno, score = similaridade de cosseno, filtros must/must_not com MatchValue e
MatchAny sobre o payload (um campo lista casa se contém o valor, como no Qdrant).

Cada coleção vira uma tabela rag."<nome>" com o vetor em halfvec (o text-embedding-3-large tem 3072 dimensões; o
índice HNSW do tipo vector vai só até 2000, o do halfvec até 4000) e o payload em jsonb.
"""
import json
import re
from types import SimpleNamespace

import psycopg2
import psycopg2.extras
import psycopg2.pool

SCHEMA = "rag"
_NAME = re.compile(r"^[a-z0-9_]{1,60}$")


def _table(name):
    if not _NAME.match(name):
        raise ValueError(f"nome de coleção inválido para o pgvector: {name!r}")
    return f'{SCHEMA}."{name}"'


def _clean(x):
    """O jsonb não aceita o caractere nulo (\\u0000), que aparece em textos extraídos de PDF; o Qdrant aceita."""
    if isinstance(x, str):
        return x.replace("\x00", "")
    if isinstance(x, dict):
        return {k: _clean(v) for k, v in x.items()}
    if isinstance(x, list):
        return [_clean(v) for v in x]
    return x


def _vec(v):
    return "[" + ",".join(repr(float(x)) for x in v) + "]"


def _contains(key, value):
    """payload contém key=value (key com pontos: metadata.psi_id). Lista no payload casa se contém o valor."""
    def nest(v):
        for part in reversed(key.split(".")):
            v = {part: v}
        return json.dumps(v, ensure_ascii=False)
    # o jsonb só aceita "lista contém valor" no nível de cima; aninhado, testa as duas formas
    return "(payload @> %s::jsonb OR payload @> %s::jsonb)", [nest(value), nest([value])]


def _condition(cond):
    match = cond.match
    if hasattr(match, "any"):  # MatchAny
        values = list(match.any or [])
        if not values:
            return "FALSE", []
        parts, args = [], []
        for v in values:
            sql, a = _contains(cond.key, v)
            parts.append(sql)
            args += a
        return "(" + " OR ".join(parts) + ")", args
    if hasattr(match, "value"):  # MatchValue
        return _contains(cond.key, match.value)
    raise NotImplementedError(f"filtro do Qdrant sem tradução para o pgvector: {type(match).__name__}")


def _where(flt):
    if flt is None:
        return "TRUE", []
    parts, args = [], []
    for cond in flt.must or []:
        sql, a = _condition(cond)
        parts.append(sql)
        args += a
    for cond in flt.must_not or []:
        sql, a = _condition(cond)
        parts.append(f"NOT {sql}")
        args += a
    if flt.should:
        ors = []
        for cond in flt.should:
            sql, a = _condition(cond)
            ors.append(sql)
            args += a
        parts.append("(" + " OR ".join(ors) + ")")
    return (" AND ".join(parts) or "TRUE"), args


class PgVectorClient:
    def __init__(self, dsn):
        self.pool = psycopg2.pool.ThreadedConnectionPool(1, 8, dsn)
        with self._cur() as cur:
            cur.execute("CREATE EXTENSION IF NOT EXISTS vector SCHEMA public")
            cur.execute(f"CREATE SCHEMA IF NOT EXISTS {SCHEMA}")

    # --- conexão -------------------------------------------------------------------------
    class _Cursor:
        def __init__(self, pool):
            self.pool = pool

        def __enter__(self):
            self.conn = self.pool.getconn()
            self.conn.autocommit = True
            self.cur = self.conn.cursor()
            return self.cur

        def __exit__(self, *exc):
            self.cur.close()
            self.pool.putconn(self.conn)

    def _cur(self):
        return self._Cursor(self.pool)

    def close(self):
        self.pool.closeall()

    # --- coleções ------------------------------------------------------------------------
    def collection_exists(self, name):
        with self._cur() as cur:
            cur.execute("SELECT to_regclass(%s)", (_table(name),))
            return cur.fetchone()[0] is not None

    def create_collection(self, name, vectors_config):
        size = int(vectors_config.size)
        with self._cur() as cur:
            cur.execute(f"""CREATE TABLE IF NOT EXISTS {_table(name)} (
                id uuid PRIMARY KEY, embedding halfvec({size}) NOT NULL, payload jsonb NOT NULL)""")
            cur.execute(f'CREATE INDEX IF NOT EXISTS "{name}_hnsw" ON {_table(name)} USING hnsw (embedding halfvec_cosine_ops)')
            cur.execute(f'CREATE INDEX IF NOT EXISTS "{name}_payload" ON {_table(name)} USING gin (payload jsonb_path_ops)')
        return True

    def delete_collection(self, name):
        with self._cur() as cur:
            cur.execute(f"DROP TABLE IF EXISTS {_table(name)}")
        return True

    def get_collection(self, name):
        with self._cur() as cur:
            cur.execute("SELECT atttypmod FROM pg_attribute WHERE attrelid = %s::regclass AND attname = 'embedding'",
                        (_table(name),))
            size = cur.fetchone()[0]
        from qdrant_client import models
        vectors = SimpleNamespace(size=size, distance=models.Distance.COSINE)
        return SimpleNamespace(config=SimpleNamespace(params=SimpleNamespace(vectors=vectors)), payload_schema={})

    def create_payload_index(self, *a, **kw):  # o índice GIN do payload já cobre todos os filtros
        return True

    def count(self, name, exact=True, count_filter=None):
        where, args = _where(count_filter)
        with self._cur() as cur:
            cur.execute(f"SELECT count(*) FROM {_table(name)} WHERE {where}", args)
            return SimpleNamespace(count=cur.fetchone()[0])

    # --- pontos --------------------------------------------------------------------------
    def upsert(self, name, points, wait=True):
        rows = [(str(p.id), _vec(p.vector), json.dumps(_clean(p.payload), ensure_ascii=False)) for p in points]
        with self._cur() as cur:
            psycopg2.extras.execute_values(
                cur, f"INSERT INTO {_table(name)} (id, embedding, payload) VALUES %s "
                     "ON CONFLICT (id) DO UPDATE SET embedding = EXCLUDED.embedding, payload = EXCLUDED.payload",
                rows, template="(%s::uuid, %s::halfvec, %s::jsonb)")
        return SimpleNamespace(status="completed")

    def restamp(self, name, chunks, revision, model):
        """O corpus mudou (PDFs novos da ponte, ou um corpus.json novo do Carlin): apaga os pontos que saíram e grava
        a revisão nova nos que continuam, sem gerar embedding de novo. Os que faltam a indexação do Carlin cria."""
        keep = {c["id"]: c for c in chunks}
        with self._cur() as cur:
            cur.execute(f"SELECT id::text FROM {_table(name)}")
            present = [r[0] for r in cur.fetchall()]
            gone = [i for i in present if i not in keep]
            if gone:
                cur.execute(f"DELETE FROM {_table(name)} WHERE id = ANY(%s::uuid[])", (gone,))
            rows = [(json.dumps(_clean({"page_content": keep[i]["page_content"], "metadata": keep[i]["metadata"],
                                        "corpus_revision": revision, "embedding_model": model}), ensure_ascii=False), i)
                    for i in present if i in keep]
            psycopg2.extras.execute_batch(cur, f"UPDATE {_table(name)} SET payload = %s::jsonb WHERE id = %s::uuid",
                                          rows, page_size=500)
        return len(rows), len(gone)

    def retrieve(self, name, ids, with_payload=True, with_vectors=False):
        with self._cur() as cur:
            cur.execute(f"SELECT id, payload FROM {_table(name)} WHERE id = ANY(%s::uuid[])", ([str(i) for i in ids],))
            return [SimpleNamespace(id=str(i), payload=p, score=None) for i, p in cur.fetchall()]

    def scroll(self, name, limit=10, with_payload=True, with_vectors=False, scroll_filter=None, offset=None):
        where, args = _where(scroll_filter)
        with self._cur() as cur:
            cur.execute(f"SELECT id, payload FROM {_table(name)} WHERE {where} ORDER BY id LIMIT %s", args + [limit])
            return [SimpleNamespace(id=str(i), payload=p, score=None) for i, p in cur.fetchall()], None

    def query_points(self, name, query, query_filter=None, limit=10, score_threshold=None, with_payload=True, **_):
        where, args = _where(query_filter)
        q = _vec(query)
        sql = f"""SELECT id, payload, 1 - (embedding <=> %s::halfvec) AS score FROM {_table(name)}
                  WHERE {where} ORDER BY embedding <=> %s::halfvec LIMIT %s"""
        with self._cur() as cur:
            # com filtro, o HNSW continua varrendo até achar "limit" pontos que passem (pgvector ≥ 0.8)
            cur.execute("SET hnsw.iterative_scan = strict_order")
            cur.execute("SET hnsw.ef_search = 100")
            cur.execute(sql, [q] + args + [q, limit])
            rows = cur.fetchall()
        points = [SimpleNamespace(id=str(i), payload=p, score=float(s)) for i, p, s in rows]
        if score_threshold is not None:
            points = [p for p in points if p.score >= score_threshold]
        return SimpleNamespace(points=points)
