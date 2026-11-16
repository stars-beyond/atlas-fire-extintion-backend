package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- banco falso: responde a cada consulta pelo trecho do SQL que a identifica, sem rede ---

type fakeRows struct {
	rows    [][]any
	i       int
	err     error // erro no meio da leitura (rows.Err)
	scanErr error
	closed  bool
}

func (r *fakeRows) Close()                                       { r.closed = true }
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) TypeMap() *pgtype.Map                         { return nil }
func (r *fakeRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}
func (r *fakeRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	return assign(dest, r.rows[r.i-1])
}

type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return assign(dest, r.vals)
}

// assign copia os valores para os destinos do Scan, como o pgx faria (nil zera o destino).
func assign(dest, vals []any) error {
	if len(dest) != len(vals) {
		return fmt.Errorf("scan: %d destinos para %d colunas", len(dest), len(vals))
	}
	for i, d := range dest {
		dv := reflect.ValueOf(d).Elem()
		if vals[i] == nil {
			dv.Set(reflect.Zero(dv.Type()))
			continue
		}
		v := reflect.ValueOf(vals[i])
		if !v.Type().ConvertibleTo(dv.Type()) {
			return fmt.Errorf("scan: coluna %d é %s, destino é %s", i, v.Type(), dv.Type())
		}
		dv.Set(v.Convert(dv.Type()))
	}
	return nil
}

type answer struct {
	rows    [][]any
	err     error // erro da consulta (Query) ou do Scan da linha única (QueryRow)
	rowsErr error
	scanErr error
}

type fakeDB struct {
	answers map[string]answer // trecho do SQL -> resposta
	opened  []*fakeRows
}

func (db *fakeDB) find(sql string) answer {
	for k, a := range db.answers {
		if strings.Contains(sql, k) {
			return a
		}
	}
	return answer{err: fmt.Errorf("consulta inesperada: %s", sql)}
}

func (db *fakeDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	a := db.find(sql)
	if a.err != nil {
		return nil, a.err
	}
	r := &fakeRows{rows: a.rows, err: a.rowsErr, scanErr: a.scanErr}
	db.opened = append(db.opened, r)
	return r, nil
}

func (db *fakeDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	a := db.find(sql)
	if a.err != nil {
		return fakeRow{err: a.err}
	}
	if len(a.rows) == 0 {
		return fakeRow{err: pgx.ErrNoRows}
	}
	return fakeRow{vals: a.rows[0]}
}

var errDown = errors.New("connection refused")

func f64(v float64) *float64 { return &v }
func str(v string) *string   { return &v }

// --- MetricFloat ---

// Banco fora do ar é erro; antes virava um mapa vazio, e os achados saíam com "0.00 mm²/s" e status 200.
func TestMetricFloat(t *testing.T) {
	const q = "FROM tests WHERE investigation_id"
	cases := []struct {
		name string
		a    answer
		want map[string]float64
		fail bool
	}{
		{"valores", answer{rows: [][]any{{"t1", f64(1.5)}, {"t2", f64(2)}}}, map[string]float64{"t1": 1.5, "t2": 2}, false},
		{"valor nulo fica de fora", answer{rows: [][]any{{"t1", f64(1.5)}, {"t2", nil}}}, map[string]float64{"t1": 1.5}, false},
		{"investigação sem o campo", answer{}, map[string]float64{}, false},
		{"a consulta falha", answer{err: errDown}, nil, true},
		{"a conexão cai no meio", answer{rows: [][]any{{"t1", f64(1.5)}}, rowsErr: errDown}, nil, true},
		{"uma linha não é lida", answer{rows: [][]any{{"t1", f64(1.5)}}, scanErr: errors.New("cannot scan")}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := &fakeDB{answers: map[string]answer{q: c.a}}
			got, err := metricFloat(context.Background(), db, "PSI-69", "k_mm2_s")
			if c.fail {
				if err == nil || got != nil {
					t.Fatalf("esperava erro e nenhum mapa, veio %v, %v", got, err)
				}
			} else if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, %v; want %v", got, err, c.want)
			}
			for _, r := range db.opened {
				if !r.closed {
					t.Error("as linhas ficaram abertas (a conexão não volta ao pool)")
				}
			}
		})
	}
}

// --- fichas ---

const (
	qInvText = "FROM investigations WHERE id"
	qPubs    = "FROM publications"
	qDocs    = "FROM chunks WHERE investigation_id"
)

func detailDB() *fakeDB {
	return &fakeDB{answers: map[string]answer{
		qInvText: {rows: [][]any{{"objective", "approach", "hypothesis", "impacts", "10.1/x"}}},
		qPubs:    {rows: [][]any{{"Paper A", "Olson, S.", "10.1/a"}, {"Paper B", "", ""}}},
		qDocs:    {rows: [][]any{{"report.pdf", "Final report", "Reports", 40, func() *int { v := 88; return &v }()}, {"notes.txt", "Notes", "", 2, nil}}},
	}}
}

func TestInvestigationDetail(t *testing.T) {
	d, err := investigationDetail(context.Background(), detailDB(), "PSI-25")
	if err != nil {
		t.Fatal(err)
	}
	if d.Objective != "objective" || d.DOI != "10.1/x" || len(d.Publications) != 2 || d.Publications[1].Title != "Paper B" {
		t.Errorf("ficha: %+v", d)
	}
	if len(d.Documents) != 2 || d.Documents[0].Chunks != 40 || *d.Documents[0].Pages != 88 || d.Documents[1].Pages != nil {
		t.Errorf("documentos: %+v", d.Documents)
	}

	// investigação sem texto no banco e sem publicações nem documentos: ficha vazia, com listas (e não null)
	empty := detailDB()
	empty.answers[qInvText], empty.answers[qPubs], empty.answers[qDocs] = answer{}, answer{}, answer{}
	d, err = investigationDetail(context.Background(), empty, "PSI-1")
	if err != nil || d.Objective != "" || d.Publications == nil || d.Documents == nil || len(d.Publications)+len(d.Documents) != 0 {
		t.Errorf("ficha vazia: %+v, %v", d, err)
	}
}

// Banco que não responde, ou linha que não é lida, é erro: nunca uma ficha vazia ou pela metade.
func TestInvestigationDetailFailures(t *testing.T) {
	cases := []struct {
		name string
		key  string
		a    answer
	}{
		{"texto: a consulta falha", qInvText, answer{err: errDown}},
		{"publicações: a consulta falha", qPubs, answer{err: errDown}},
		{"publicações: a conexão cai no meio", qPubs, answer{rows: [][]any{{"Paper A", "x", "y"}}, rowsErr: errDown}},
		{"publicações: uma linha não é lida", qPubs, answer{rows: [][]any{{"Paper A", "x", "y"}}, scanErr: errors.New("cannot scan")}},
		{"documentos: a consulta falha", qDocs, answer{err: errDown}},
		{"documentos: a conexão cai no meio", qDocs, answer{rowsErr: errDown}},
		{"documentos: uma linha não é lida", qDocs, answer{rows: [][]any{{"a.pdf", "A", "", 1, nil}}, scanErr: errors.New("cannot scan")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := detailDB()
			db.answers[c.key] = c.a
			if d, err := investigationDetail(context.Background(), db, "PSI-25"); err == nil {
				t.Fatalf("esperava erro, veio %+v", d)
			}
			for _, r := range db.opened {
				if !r.closed {
					t.Error("as linhas ficaram abertas (a conexão não volta ao pool)")
				}
			}
		})
	}
}

// As consultas das fichas tratam campo nulo como texto vazio, para uma publicação sem DOI não sumir nem derrubar a ficha.
func TestInvestigationDetailQueriesTolerateNulls(t *testing.T) {
	db := &recordingDB{fakeDB: detailDB()}
	if _, err := investigationDetail(context.Background(), db, "PSI-25"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"coalesce(title,'')", "coalesce(authors,'')", "coalesce(doi,'')", "coalesce(min(title),'')", "coalesce(min(category),'')"} {
		if !strings.Contains(strings.Join(db.sql, "\n"), want) {
			t.Errorf("faltou %s nas consultas da ficha", want)
		}
	}
}

type recordingDB struct {
	*fakeDB
	sql []string
}

func (db *recordingDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	db.sql = append(db.sql, sql)
	return db.fakeDB.Query(ctx, sql, args...)
}

func TestTestRaw(t *testing.T) {
	const q = "FROM tests WHERE id"
	db := &fakeDB{answers: map[string]answer{q: {rows: [][]any{{json.RawMessage(`{"k":1}`), json.RawMessage(`{"Fuel":"PMMA"}`)}}}}}
	m, raw, err := testRaw(context.Background(), db, "t1")
	if err != nil || string(m) != `{"k":1}` || string(raw) != `{"Fuel":"PMMA"}` {
		t.Fatalf("got %s, %s, %v", m, raw, err)
	}
	// teste do catálogo sem linha no banco: ficha sem a linha original, sem erro
	m, raw, err = testRaw(context.Background(), &fakeDB{answers: map[string]answer{q: {}}}, "t1")
	if err != nil || m != nil || raw != nil {
		t.Fatalf("linha ausente: %s, %s, %v", m, raw, err)
	}
	if _, _, err = testRaw(context.Background(), &fakeDB{answers: map[string]answer{q: {err: errDown}}}, "t1"); !errors.Is(err, errDown) {
		t.Fatalf("banco fora do ar: %v", err)
	}
}

// --- loadOutcomes ---

func TestLoadOutcomes(t *testing.T) {
	const q = "key LIKE 'outcomes:%'"
	doc := []byte(`{"investigation":"PSI-25","method":"regras","tests":[{"test":"BASS-II 1","form":"bastão"}]}`)
	t.Run("lê os documentos e pula o que não é JSON", func(t *testing.T) {
		db := &fakeDB{answers: map[string]answer{q: {rows: [][]any{{"outcomes:PSI-25", doc}, {"outcomes:PSI-99", []byte("{quebrado")}}}}}
		out, err := loadOutcomes(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out["PSI-25"] == nil || out["PSI-25"].Method != "regras" || len(out["PSI-25"].Tests) != 1 {
			t.Fatalf("out = %+v", out)
		}
	})
	t.Run("acervo sem desfechos", func(t *testing.T) {
		out, err := loadOutcomes(context.Background(), &fakeDB{answers: map[string]answer{q: {}}})
		if err != nil || out == nil || len(out) != 0 {
			t.Fatalf("got %v, %v; want mapa vazio", out, err)
		}
	})
	for name, a := range map[string]answer{
		"a consulta falha":       {err: errDown},
		"a conexão cai no meio":  {rows: [][]any{{"outcomes:PSI-25", doc}}, rowsErr: errDown},
		"uma linha não é lida":   {rows: [][]any{{"outcomes:PSI-25", doc}}, scanErr: errors.New("cannot scan")},
		"coluna de tipo trocado": {rows: [][]any{{"outcomes:PSI-25", 7}}},
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := loadOutcomes(context.Background(), &fakeDB{answers: map[string]answer{q: a}}); err == nil {
				t.Fatalf("esperava erro, veio %v", out)
			}
		})
	}
}

// --- loadCatalog ---

const (
	qIngest  = "key='ingest'"
	qInvs    = "FROM investigations"
	qTests   = "FROM tests ORDER BY id"
	qModels  = "FROM models"
	qChunks  = "count(*) FROM chunks"
	qOutcome = "key LIKE 'outcomes:%'"
	qChanges = "key='changes'"
)

// goodDB é um acervo mínimo e completo: uma investigação, dois testes, um modelo, desfechos e novidades.
func goodDB() *fakeDB {
	return &fakeDB{answers: map[string]answer{
		qIngest: {rows: [][]any{{json.RawMessage(`{"finished":"2026-11-14"}`)}}},
		qInvs: {rows: [][]any{{"PSI-25", "BASS-II", "Burning and Suppression of Solids II", "experiment", "combustion", "MSG", "msg",
			"solido", "core", 0.9, "Cabin solids", nil, nil, 1.5, 3, 40, json.RawMessage(`{"n_tests":2,"has_o2":true}`), "[0.1,0.2,0.3]"}}},
		qTests: {rows: [][]any{
			{"t1", "PSI-25", "BASS-II 1", "solido", str("PMMA rod"), str("pmma"), f64(0.21), f64(1), 0.0, f64(10), nil, nil,
				"queimou", str("flame held"), []string{"pressure"}, "bass.csv", 2, []byte(`{"k_mm2_s":1.2}`)},
			{"t2", "PSI-25", "BASS-II 2", "solido", nil, nil, f64(0.17), nil, 0.0, nil, nil, nil,
				"desconhecido", nil, nil, "bass.csv", 3, []byte(`{}`)},
		}},
		qModels:  {rows: [][]any{{"flex_k_heptane", []byte(`{"label":"Heptane","n":120}`)}, {"quebrado", []byte(`{`)}}},
		qChunks:  {rows: [][]any{{1234}}},
		qOutcome: {rows: [][]any{{"outcomes:PSI-25", []byte(`{"investigation":"PSI-25"}`)}}},
		qChanges: {rows: [][]any{{json.RawMessage(`{"since":"2026-11-13"}`)}}},
	}}
}

func TestLoadCatalog(t *testing.T) {
	cat, err := loadCatalog(context.Background(), goodDB())
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Invs) != 1 || cat.InvByID["PSI-25"] == nil || len(cat.Tests) != 2 || cat.TestByID["t2"] == nil {
		t.Fatalf("acervo: %d investigações, %d testes", len(cat.Invs), len(cat.Tests))
	}
	inv := cat.Invs[0]
	if inv.Name != "BASS-II" || inv.StatsData.NTests != 2 || !inv.StatsData.HasO2 || len(inv.Vec) != 3 {
		t.Errorf("investigação: %+v", inv)
	}
	if cat.Plot != 1 { // só t1 tem O₂ e pressão
		t.Errorf("plot = %d, want 1", cat.Plot)
	}
	if t1 := cat.TestByID["t1"]; t1.Metrics["k_mm2_s"] != 1.2 || len(t1.Assumed) != 1 {
		t.Errorf("t1: %+v", t1)
	}
	if t2 := cat.TestByID["t2"]; t2.Assumed == nil { // [] e não null no JSON
		t.Error("assumed de t2 devia ser uma lista vazia")
	}
	if len(cat.Models) != 1 || cat.Models["flex_k_heptane"].N != 120 {
		t.Errorf("modelos: %+v", cat.Models)
	}
	if cat.Chunks != 1234 || cat.Outcomes["PSI-25"] == nil || string(cat.Changes) != `{"since":"2026-11-13"}` {
		t.Errorf("chunks %d, desfechos %v, novidades %s", cat.Chunks, cat.Outcomes, cat.Changes)
	}
}

// Acervo antigo não tem a linha de novidades: isso é esperado e não falha a carga.
func TestLoadCatalogWithoutChangesRow(t *testing.T) {
	db := goodDB()
	db.answers[qChanges] = answer{} // nenhuma linha: pgx.ErrNoRows
	cat, err := loadCatalog(context.Background(), db)
	if err != nil {
		t.Fatalf("linha ausente não é falha: %v", err)
	}
	if len(cat.Changes) != 0 {
		t.Fatalf("novidades = %s, want vazio", cat.Changes)
	}
}

// Qualquer leitura que falhe de verdade falha a carga inteira, em vez de servir um acervo pela metade.
func TestLoadCatalogFailsOnRealErrors(t *testing.T) {
	cases := []struct {
		name string
		key  string
		a    answer
	}{
		{"a ingestão não terminou", qIngest, answer{}},
		{"investigações: consulta", qInvs, answer{err: errDown}},
		{"investigações: no meio", qInvs, answer{rowsErr: errDown}},
		{"testes: consulta", qTests, answer{err: errDown}},
		{"testes: no meio", qTests, answer{rowsErr: errDown}},
		{"modelos: consulta", qModels, answer{err: errDown}},
		{"modelos: no meio", qModels, answer{rowsErr: errDown}},
		{"contagem dos trechos", qChunks, answer{err: errDown}},
		{"desfechos: consulta", qOutcome, answer{err: errDown}},
		{"desfechos: no meio", qOutcome, answer{rows: [][]any{{"outcomes:PSI-25", []byte(`{}`)}}, rowsErr: errDown}},
		{"novidades: erro que não é linha ausente", qChanges, answer{err: errDown}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := goodDB()
			db.answers[c.key] = c.a
			cat, err := loadCatalog(context.Background(), db)
			if err == nil || cat != nil {
				t.Fatalf("esperava a carga falhar, veio %v, %v", cat, err)
			}
		})
	}
}

// --- vetores ---

func TestParseVecAndLiteral(t *testing.T) {
	if v := parseVec("[0.5,-1,2]"); !reflect.DeepEqual(v, []float32{0.5, -1, 2}) {
		t.Errorf("parseVec = %v", v)
	}
	if v := parseVec("[]"); v != nil {
		t.Errorf("vetor vazio = %v, want nil", v)
	}
	if v := parseVec(""); v != nil {
		t.Errorf("texto vazio = %v, want nil", v)
	}
	if s := vecLiteral([]float32{0.5, -1, 2}); s != "[0.500000,-1.000000,2.000000]" {
		t.Errorf("vecLiteral = %s", s)
	}
	if s := vecLiteral(nil); s != "[]" {
		t.Errorf("vecLiteral(nil) = %s", s)
	}
}
