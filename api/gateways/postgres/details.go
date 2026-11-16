package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"atlas-api/domain/entities"

	"github.com/jackc/pgx/v5"
)

// InvestigationDetail lê o texto da investigação, as publicações e os documentos lidos pela ingestão.
func (r *Repo) InvestigationDetail(ctx context.Context, id string) (entities.InvestigationDetail, error) {
	return investigationDetail(ctx, r.DB, id)
}

func investigationDetail(ctx context.Context, db querier, id string) (entities.InvestigationDetail, error) {
	d := entities.InvestigationDetail{Publications: []entities.Publication{}, Documents: []entities.DocumentSummary{}}
	// Investigação sem texto no banco é normal (campos vazios); banco que não responde é erro, não uma ficha vazia.
	err := db.QueryRow(ctx, "SELECT objective, approach, hypothesis, impacts, doi FROM investigations WHERE id=$1", id).
		Scan(&d.Objective, &d.Approach, &d.Hypothesis, &d.Impacts, &d.DOI)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return d, err
	}
	// Campo nulo (as colunas aceitam: uma publicação sem DOI no PSI) é texto vazio. Assim uma linha que não é lida
	// é sempre falha de verdade e sobe como erro; antes ela era pulada em silêncio, e a publicação sumia da ficha.
	rows, err := db.Query(ctx, "SELECT coalesce(title,''), coalesce(authors,''), coalesce(doi,'') FROM publications WHERE investigation_id=$1 ORDER BY id", id)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var p entities.Publication
		if err := rows.Scan(&p.Title, &p.Authors, &p.DOI); err != nil {
			rows.Close()
			return d, err
		}
		d.Publications = append(d.Publications, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}
	rows, err = db.Query(ctx, `SELECT coalesce(source_file,''), coalesce(min(title),''), coalesce(min(category),''), count(*), max(page)
		FROM chunks WHERE investigation_id=$1 AND kind='documento' GROUP BY source_file ORDER BY count(*) DESC`, id)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var doc entities.DocumentSummary
		if err := rows.Scan(&doc.File, &doc.Title, &doc.Category, &doc.Chunks, &doc.Pages); err != nil {
			rows.Close()
			return d, err
		}
		d.Documents = append(d.Documents, doc)
	}
	rows.Close()
	return d, rows.Err()
}

// TestRaw lê as métricas e a linha original de um teste.
func (r *Repo) TestRaw(ctx context.Context, id string) (metrics, raw json.RawMessage, err error) {
	return testRaw(ctx, r.DB, id)
}

func testRaw(ctx context.Context, db querier, id string) (metrics, raw json.RawMessage, err error) {
	err = db.QueryRow(ctx, "SELECT metrics, raw FROM tests WHERE id=$1", id).Scan(&metrics, &raw)
	if errors.Is(err, pgx.ErrNoRows) { // teste do catálogo sem linha original no banco: ficha sem ela
		return nil, nil, nil
	}
	return
}

// MetricFloat lê um campo numérico de tests.metrics para uma investigação. Investigação sem o campo é um mapa
// vazio; banco que não responde é erro, não um mapa vazio (que viraria estatística zerada nos achados).
func (r *Repo) MetricFloat(ctx context.Context, inv, key string) (map[string]float64, error) {
	return metricFloat(ctx, r.DB, inv, key)
}

func metricFloat(ctx context.Context, db querier, inv, key string) (map[string]float64, error) {
	rows, err := db.Query(ctx, "SELECT id, (metrics->>$2)::float8 FROM tests WHERE investigation_id=$1 AND metrics ? $2", inv, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var id string
		var v *float64
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		if v != nil { // campo presente com null: o teste fica sem o valor
			out[id] = *v
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
