package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"atlas-api/domain/entities"
)

// --- casos de uso falsos ---

type fakeMeta struct{}

func (fakeMeta) Get(context.Context) map[string]any {
	return map[string]any{"counts": map[string]int{"investigations": 2}, "habitats": entities.Habitats}
}

type fakeInvs struct{ list []*entities.Investigation }

func (f fakeInvs) List() []*entities.Investigation { return f.list }

type fakeDetail struct {
	err      error
	id       string
	deadline time.Duration
}

func (f *fakeDetail) note(ctx context.Context, id string) {
	f.id = id
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
}

type fakeInv struct{ fakeDetail }

func (f *fakeInv) Get(ctx context.Context, id string) (entities.InvestigationDetail, error) {
	f.note(ctx, id)
	if f.err != nil {
		return entities.InvestigationDetail{}, f.err
	}
	return entities.InvestigationDetail{Investigation: &entities.Investigation{ID: id, Name: "BASS-II"}, Objective: "obj",
		Publications: []entities.Publication{}, Documents: []entities.DocumentSummary{}}, nil
}

type fakeTest struct{ fakeDetail }

func (f *fakeTest) Get(ctx context.Context, id string) (entities.TestDetail, error) {
	f.note(ctx, id)
	if f.err != nil {
		return entities.TestDetail{}, f.err
	}
	return entities.TestDetail{Test: &entities.Test{ID: id, Assumed: []string{}}, Investigation: "BASS-II",
		Metrics: json.RawMessage(`{"k":1}`), Raw: json.RawMessage(`{"Fuel":"PMMA"}`), Source: "bass.csv, row 2"}, nil
}

type fakeTests struct{}

func (fakeTests) List() entities.CompactTests {
	return entities.CompactTests{Columns: []string{"id", "inv"}, Rows: [][]any{{"t1", "PSI-25"}}}
}

type fakeExternal struct{}

func (fakeExternal) List() map[string]any {
	return map[string]any{"available": false, "tests": []any{}}
}

// --- apoio ---

// get chama o handler pelo roteador, para o {id} do caminho chegar como chega em produção.
func get(pattern string, h http.HandlerFunc, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, h)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func wantError(t *testing.T, w *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status = %d, want %d (%s)", w.Code, code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("o erro não é JSON: %q", w.Body.String())
	}
	if len(body) != 1 || body["error"] != msg {
		t.Fatalf(`corpo = %s, want {"error": %q}`, w.Body.String(), msg)
	}
}

func object(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("a resposta não é um objeto JSON: %q", w.Body.String())
	}
	return body
}

var errDB = errors.New("failed to connect to `host=db user=atlas database=atlas`: dial tcp 172.18.0.2:5432: connect: connection refused")

func noLeak(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for _, leak := range []string{"172.18", "host=db", "user=atlas", "refused", "dial tcp"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("a causa vazou para o cliente (%q): %s", leak, w.Body.String())
		}
	}
}

// --- os handlers sem parâmetro ---

func TestHealth(t *testing.T) {
	w := get("GET /api/health", HealthHandler(), "/api/health")
	if body := object(t, w); body["ok"] != true || len(body) != 1 {
		t.Errorf("corpo = %s", w.Body.String())
	}
}

func TestMeta(t *testing.T) {
	body := object(t, get("GET /api/meta", MetaHandler(fakeMeta{}), "/api/meta"))
	if body["counts"] == nil || len(body["habitats"].([]any)) != len(entities.Habitats) {
		t.Errorf("corpo = %v", body)
	}
}

func TestInvestigations(t *testing.T) {
	w := get("GET /api/investigations", InvestigationsHandler(fakeInvs{list: []*entities.Investigation{{ID: "PSI-25", Name: "BASS-II"}}}), "/api/investigations")
	var list []map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 1 || list[0]["id"] != "PSI-25" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	// a resposta é a lista direta (o front faz .map nela), e os campos internos não saem
	if _, ok := list[0]["Vec"]; ok || strings.Contains(w.Body.String(), "StatsData") {
		t.Errorf("campos internos na resposta: %s", w.Body.String())
	}
	w = get("GET /api/investigations", InvestigationsHandler(fakeInvs{list: []*entities.Investigation{}}), "/api/investigations")
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("acervo vazio = %s, want []", w.Body.String())
	}
}

func TestTestsAndExternal(t *testing.T) {
	body := object(t, get("GET /api/tests", TestsHandler(fakeTests{}), "/api/tests"))
	if len(body["columns"].([]any)) != 2 || len(body["rows"].([]any)) != 1 {
		t.Errorf("testes = %v", body)
	}
	body = object(t, get("GET /api/external", ExternalHandler(fakeExternal{}), "/api/external"))
	if body["available"] != false || body["tests"] == nil {
		t.Errorf("externos = %v", body)
	}
}

// --- as fichas, que consultam o banco a cada pedido ---

func TestInvestigationDetail(t *testing.T) {
	const pattern = "GET /api/investigations/{id}"
	t.Run("existe", func(t *testing.T) {
		uc := &fakeInv{}
		body := object(t, get(pattern, InvestigationHandler(uc), "/api/investigations/PSI-25"))
		if uc.id != "PSI-25" {
			t.Errorf("id recebido = %q", uc.id)
		}
		for _, k := range []string{"investigation", "objective", "approach", "hypothesis", "impacts", "doi", "publications", "documents"} {
			if _, ok := body[k]; !ok {
				t.Errorf("faltou o campo %q", k)
			}
		}
		// a consulta ao banco tem prazo: antes recebia o contexto cru do pedido
		if uc.deadline <= 0 || uc.deadline > detailTimeout {
			t.Errorf("prazo = %v, want até %v", uc.deadline, detailTimeout)
		}
	})
	t.Run("id com caracteres especiais chega decodificado", func(t *testing.T) {
		uc := &fakeInv{}
		get(pattern, InvestigationHandler(uc), "/api/investigations/PSI%2D25%20x")
		if uc.id != "PSI-25 x" {
			t.Errorf("id recebido = %q", uc.id)
		}
	})
	t.Run("não existe", func(t *testing.T) {
		uc := &fakeInv{fakeDetail{err: entities.ErrNotFound}}
		wantError(t, get(pattern, InvestigationHandler(uc), "/api/investigations/PSI-404"), 404, "investigation not found")
	})
	t.Run("banco fora do ar", func(t *testing.T) {
		w := get(pattern, InvestigationHandler(&fakeInv{fakeDetail{err: errDB}}), "/api/investigations/PSI-25")
		wantError(t, w, 503, dbDown)
		noLeak(t, w)
	})
	t.Run("prazo da consulta estourado", func(t *testing.T) {
		w := get(pattern, InvestigationHandler(&fakeInv{fakeDetail{err: context.DeadlineExceeded}}), "/api/investigations/PSI-25")
		wantError(t, w, 503, dbDown)
	})
}

func TestTestDetail(t *testing.T) {
	const pattern = "GET /api/tests/{id}"
	t.Run("existe", func(t *testing.T) {
		uc := &fakeTest{}
		body := object(t, get(pattern, TestHandler(uc), "/api/tests/PSI-25:7"))
		if uc.id != "PSI-25:7" {
			t.Errorf("id recebido = %q", uc.id)
		}
		for _, k := range []string{"test", "investigation", "metrics", "raw", "source"} {
			if _, ok := body[k]; !ok {
				t.Errorf("faltou o campo %q", k)
			}
		}
		if body["raw"].(map[string]any)["Fuel"] != "PMMA" { // a linha original vai como objeto, não como texto
			t.Errorf("raw = %v", body["raw"])
		}
		if uc.deadline <= 0 || uc.deadline > detailTimeout {
			t.Errorf("prazo = %v, want até %v", uc.deadline, detailTimeout)
		}
	})
	t.Run("não existe", func(t *testing.T) {
		wantError(t, get(pattern, TestHandler(&fakeTest{fakeDetail{err: entities.ErrNotFound}}), "/api/tests/nope"), 404, "test not found")
	})
	t.Run("banco fora do ar", func(t *testing.T) {
		w := get(pattern, TestHandler(&fakeTest{fakeDetail{err: errDB}}), "/api/tests/t1")
		wantError(t, w, 503, dbDown)
		noLeak(t, w)
	})
}
