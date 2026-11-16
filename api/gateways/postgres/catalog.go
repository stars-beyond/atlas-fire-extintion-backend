// Package postgres lê o banco da ingestão (Postgres + pgvector): o acervo, as fichas e a busca nos trechos.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier é o que as leituras do acervo usam: o pool ou uma transação.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repo implementa as consultas que o domínio pede ao banco.
type Repo struct {
	DB *pgxpool.Pool
}

// LoadCatalog lê o acervo inteiro (investigações, testes, modelos e desfechos). Falha enquanto a ingestão não terminou.
// Tudo sai de uma única transação somente leitura (um retrato só): se a ingestão trocar o acervo no meio da leitura,
// não se misturam investigações antigas com testes novos. Um erro no meio de qualquer leitura falha a carga inteira,
// em vez de servir um acervo pela metade até o próximo reinício.
func (r *Repo) LoadCatalog(ctx context.Context) (cat *entities.Catalog, err error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	tx, err := r.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }() // só leitura: nada a confirmar
	return loadCatalog(ctx, tx)
}

func loadCatalog(ctx context.Context, db querier) (*entities.Catalog, error) {
	s := &entities.Catalog{InvByID: map[string]*entities.Investigation{}, TestByID: map[string]*entities.Test{}, Models: map[string]entities.ModelSpec{}}
	if err := db.QueryRow(ctx, "SELECT value FROM meta WHERE key='ingest'").Scan(&s.Ingest); err != nil {
		return nil, fmt.Errorf("ingest has not finished yet: %w", err)
	}
	rows, err := db.Query(ctx, `SELECT id, acronym, title, kind, area, hardware, hardware_family, family, role, priority,
		priority_why, start_year, end_year, total_gb, n_publications, n_files, stats, embedding::text
		FROM investigations ORDER BY priority DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v := &entities.Investigation{}
		var emb string
		if err := rows.Scan(&v.ID, &v.Acronym, &v.Title, &v.Kind, &v.Area, &v.Hardware, &v.HwFamily, &v.Family, &v.Role,
			&v.Priority, &v.PriorityWhy, &v.StartYear, &v.EndYear, &v.TotalGB, &v.NPubs, &v.NFiles, &v.Stats, &emb); err != nil {
			return nil, err
		}
		v.Name = v.Acronym
		if v.Name == "" {
			v.Name = ptbr.Shorten(v.Title, 46)
		}
		_ = json.Unmarshal(v.Stats, &v.StatsData)
		v.Vec = parseVec(emb)
		s.Invs = append(s.Invs, v)
		s.InvByID[v.ID] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading investigations: %w", err)
	}
	rows.Close()

	rows, err = db.Query(ctx, `SELECT id, investigation_id, label, family, fuel, material, o2, pressure_atm, gravity,
		flow_cm_s, co2, he, outcome, outcome_raw, assumed, source_file, source_row, metrics FROM tests ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		t := &entities.Test{}
		var metrics []byte
		if err := rows.Scan(&t.ID, &t.Inv, &t.Label, &t.Family, &t.Fuel, &t.Material, &t.O2, &t.P, &t.G, &t.Flow, &t.CO2,
			&t.He, &t.Outcome, &t.Raw, &t.Assumed, &t.File, &t.Row, &metrics); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metrics, &t.Metrics)
		if t.Assumed == nil {
			t.Assumed = []string{}
		}
		s.Tests = append(s.Tests, t)
		s.TestByID[t.ID] = t
		if t.Plot() {
			s.Plot++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading tests: %w", err)
	}
	rows.Close()

	rows, err = db.Query(ctx, "SELECT id, spec FROM models")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var m entities.ModelSpec
		if json.Unmarshal(raw, &m) == nil {
			s.Models[id] = m
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading models: %w", err)
	}
	rows.Close()
	if err := db.QueryRow(ctx, "SELECT count(*) FROM chunks").Scan(&s.Chunks); err != nil {
		return nil, fmt.Errorf("counting chunks: %w", err)
	}
	if s.Outcomes, err = loadOutcomes(ctx, db); err != nil {
		return nil, fmt.Errorf("reading outcomes: %w", err)
	}
	// A linha não existe em acervos antigos (nem na primeira carga): fica vazio. Qualquer outro erro é o banco.
	if err := db.QueryRow(ctx, "SELECT value FROM meta WHERE key='changes'").Scan(&s.Changes); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("reading changes: %w", err)
	}
	return s, nil
}

// loadOutcomes lê os desfechos extraídos dos relatórios (meta outcomes:<investigação>). Acervo sem desfechos é um
// mapa vazio; erro de consulta ou de leitura sobe. Um documento que não é o JSON esperado é pulado, com aviso no log.
func loadOutcomes(ctx context.Context, db querier) (map[string]*entities.FlamDoc, error) {
	rows, err := db.Query(ctx, "SELECT key, value FROM meta WHERE key LIKE 'outcomes:%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*entities.FlamDoc{}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		d := &entities.FlamDoc{}
		if err := json.Unmarshal(raw, d); err != nil {
			log.Printf("acervo: %s ignorado, não é o JSON esperado: %v", key, err)
			continue
		}
		out[strings.TrimPrefix(key, "outcomes:")] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseVec(s string) []float32 {
	s = strings.Trim(s, "[]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]float32, len(parts))
	for i, p := range parts {
		f, _ := strconv.ParseFloat(p, 32)
		out[i] = float32(f)
	}
	return out
}

func vecLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'f', 6, 32))
	}
	b.WriteByte(']')
	return b.String()
}
