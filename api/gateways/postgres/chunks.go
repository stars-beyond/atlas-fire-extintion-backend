package postgres

import (
	"context"
	"fmt"

	"atlas-api/domain/entities"
	"atlas-api/domain/search"
)

// SearchChunks faz a busca híbrida no pgvector: vetor + palavra, fundidos por posição (RRF).
func (r *Repo) SearchChunks(ctx context.Context, q search.ChunkQuery) ([]entities.Hit, error) {
	var invArg any
	if q.Inv != "" {
		invArg = q.Inv
	}
	sql := `
WITH v AS (
  SELECT id, row_number() OVER (ORDER BY embedding <=> $1::vector) AS r
  FROM chunks WHERE kind = ANY($3) AND ($4::text IS NULL OR investigation_id = $4)
  ORDER BY embedding <=> $1::vector LIMIT 40),
f AS (
  SELECT id, row_number() OVER (ORDER BY ts_rank_cd(tsv, to_tsquery('english', $2)) DESC) AS r
  FROM chunks WHERE $2 <> '' AND tsv @@ to_tsquery('english', $2) AND kind = ANY($3) AND ($4::text IS NULL OR investigation_id = $4)
  ORDER BY ts_rank_cd(tsv, to_tsquery('english', $2)) DESC LIMIT 40)
SELECT c.id, c.kind, c.title, coalesce(c.category,''), c.investigation_id, c.test_id, coalesce(c.source_file,''), c.page, c.text,
       (coalesce(1.0/(60+v.r),0) + coalesce(1.0/(60+f.r),0))
         * CASE WHEN c.category = 'Engineering Documents' THEN 0.6 ELSE 1 END AS score,
       1 - (c.embedding <=> $1::vector) AS sim
FROM (SELECT id FROM v UNION SELECT id FROM f) u
JOIN chunks c USING (id) LEFT JOIN v USING (id) LEFT JOIN f USING (id)
ORDER BY score DESC LIMIT $5`
	args := []any{vecLiteral(q.Vec), q.FTS, q.Kinds, invArg, q.K}
	if len(q.Vec) == 0 {
		// sem vetor (embedder fora do ar): só a busca por palavra, com a mesma pontuação por posição
		sql = `
WITH f AS (
  SELECT id, row_number() OVER (ORDER BY ts_rank_cd(tsv, to_tsquery('english', $1)) DESC) AS r
  FROM chunks WHERE $1 <> '' AND tsv @@ to_tsquery('english', $1) AND kind = ANY($2) AND ($3::text IS NULL OR investigation_id = $3)
  ORDER BY ts_rank_cd(tsv, to_tsquery('english', $1)) DESC LIMIT 40)
SELECT c.id, c.kind, c.title, coalesce(c.category,''), c.investigation_id, c.test_id, coalesce(c.source_file,''), c.page, c.text,
       (1.0/(60+f.r)) * CASE WHEN c.category = 'Engineering Documents' THEN 0.6 ELSE 1 END AS score,
       0::float8 AS sim
FROM f JOIN chunks c USING (id)
ORDER BY score DESC LIMIT $4`
		args = []any{q.FTS, q.Kinds, invArg, q.K}
	}
	rows, err := r.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []entities.Hit
	for rows.Next() {
		var h entities.Hit
		var id int64
		if err := rows.Scan(&id, &h.Kind, &h.Title, &h.Category, &h.Inv, &h.TestID, &h.File, &h.Page, &h.Text, &h.Score, &h.Sim); err != nil {
			return nil, err
		}
		h.ID, h.Origin = fmt.Sprintf("a%d", id), "atlas"
		out = append(out, h)
	}
	return out, rows.Err()
}
