package datasets

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"atlas-api/domain/entities"
)

// --- casos de uso falsos: só o conjunto "A" existe ---

type fakeList struct{}

func (fakeList) List() []entities.Dataset {
	return []entities.Dataset{{ID: "todos", Rows: 6}, {ID: "A", Rows: 4, Numeric: []entities.VarDef{{Key: "o2_pct", Label: "Oxygen"}}}}
}

type fakeRecords struct{ id string }

func (f *fakeRecords) Get(id string) (entities.Dataset, []entities.Record, error) {
	f.id = id
	if id != "A" {
		return entities.Dataset{}, nil, entities.ErrNotFound
	}
	v := 21.0
	return entities.Dataset{ID: "A", Rows: 1}, []entities.Record{{ID: "a1", Num: map[string]*float64{"o2_pct": &v, "k": nil}, Cat: map[string]*string{}}}, nil
}

type fakeChart struct {
	exists, builds int
	cfg            entities.ChartConfig
	vanish         bool // o conjunto some entre Exists e Build
}

func (f *fakeChart) Exists(id string) bool { f.exists++; return id == "A" }

func (f *fakeChart) Build(id string, cfg entities.ChartConfig) (entities.BuiltChart, []string, error) {
	f.builds++
	f.cfg = cfg
	if f.vanish {
		return entities.BuiltChart{}, nil, entities.ErrNotFound
	}
	if cfg.Type != "scatter" && cfg.Type != "bar" && cfg.Type != "histogram" {
		return entities.BuiltChart{}, nil, fmt.Errorf("unknown chart type")
	}
	return entities.BuiltChart{Kind: cfg.Type, Used: 4, Missing: 1}, []string{"Heptane", "Methanol"}, nil
}

type fakeCSV struct{ id, key, value string }

func (f *fakeCSV) Export(id, key, value string) (string, [][]string, error) {
	f.id, f.key, f.value = id, key, value
	if id != "A" {
		return "", nil, entities.ErrNotFound
	}
	return "A", [][]string{{"source_file", "test", "Fuel"}, {"a.csv", "run 1", `He said "hot", twice`}, {"a.csv", "run 2", "line\nbreak"}}, nil
}

// --- apoio ---

func serve(method, pattern string, h http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc(method+" "+pattern, h)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
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

func object(t *testing.T, w *httptest.ResponseRecorder, want ...string) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("a resposta não é um objeto JSON: %q", w.Body.String())
	}
	if len(body) != len(want) {
		t.Errorf("campos da resposta: %s, want %v", w.Body.String(), want)
	}
	for _, k := range want {
		if _, ok := body[k]; !ok {
			t.Errorf("faltou o campo %q", k)
		}
	}
	return body
}

// --- GET /api/datasets ---

func TestDatasets(t *testing.T) {
	body := object(t, serve("GET", "/api/datasets", DatasetsHandler(fakeList{}, "the note"), "/api/datasets", ""), "datasets", "note")
	list := body["datasets"].([]any)
	if len(list) != 2 || body["note"] != "the note" || list[0].(map[string]any)["id"] != "todos" {
		t.Errorf("corpo = %v", body)
	}
	for _, k := range []string{"id", "label", "investigation", "psi", "rows", "files", "numeric", "categorical"} {
		if _, ok := list[1].(map[string]any)[k]; !ok {
			t.Errorf("faltou o campo %q no conjunto", k)
		}
	}
}

// --- GET /api/datasets/{id}/records ---

func TestRecords(t *testing.T) {
	const pattern = "/api/datasets/{id}/records"
	uc := &fakeRecords{}
	body := object(t, serve("GET", pattern, RecordsHandler(uc), "/api/datasets/A/records", ""), "dataset", "records")
	recs := body["records"].([]any)
	if uc.id != "A" || len(recs) != 1 {
		t.Fatalf("id %q, corpo %v", uc.id, body)
	}
	num := recs[0].(map[string]any)["num"].(map[string]any)
	if num["o2_pct"] != 21.0 {
		t.Errorf("num = %v", num)
	}
	if v, ok := num["k"]; !ok || v != nil { // valor ausente é null, nunca zero
		t.Errorf("valor ausente = %v, want null", v)
	}
	wantError(t, serve("GET", pattern, RecordsHandler(&fakeRecords{}), "/api/datasets/nope/records", ""), 404, notFound)
}

// --- POST /api/datasets/{id}/chart ---

func TestChart(t *testing.T) {
	const pattern = "/api/datasets/{id}/chart"
	cases := []struct {
		name, path, body string
		code             int
		msg              string
		builds           int
	}{
		{"conjunto inexistente: 404 antes de olhar o corpo", "/api/datasets/nope/chart", `{"type":`, 404, notFound, 0},
		{"corpo vazio", "/api/datasets/A/chart", ``, 400, "invalid chart configuration", 0},
		{"não é JSON", "/api/datasets/A/chart", `{"type":`, 400, "invalid chart configuration", 0},
		{"tipo errado", "/api/datasets/A/chart", `{"bins":"dez"}`, 400, "invalid chart configuration", 0},
		{"tipo de gráfico desconhecido", "/api/datasets/A/chart", `{"type":"pie"}`, 400, "unknown chart type: pie", 1},
		{"sem tipo de gráfico", "/api/datasets/A/chart", `{}`, 400, "unknown chart type: ", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeChart{}
			wantError(t, serve("POST", pattern, ChartHandler(uc), c.path, c.body), c.code, c.msg)
			if uc.builds != c.builds {
				t.Errorf("gráficos calculados = %d, want %d", uc.builds, c.builds)
			}
		})
	}
	t.Run("sucesso", func(t *testing.T) {
		uc := &fakeChart{}
		w := serve("POST", pattern, ChartHandler(uc), "/api/datasets/A/chart",
			`{"type":"scatter","x":"o2_pct","y":"k","group":"fuel","filterKey":"fuel","filterValue":"Heptane","bins":12}`)
		body := object(t, w, "chart", "filter_options")
		want := entities.ChartConfig{Type: "scatter", X: "o2_pct", Y: "k", Group: "fuel", FilterKey: "fuel", FilterValue: "Heptane", Bins: 12}
		if uc.cfg != want {
			t.Errorf("configuração recebida: %+v", uc.cfg)
		}
		chart := body["chart"].(map[string]any)
		if chart["kind"] != "scatter" || chart["used"] != 4.0 || chart["missing"] != 1.0 || len(body["filter_options"].([]any)) != 2 {
			t.Errorf("corpo = %v", body)
		}
		if uc.exists != 1 || uc.builds != 1 {
			t.Errorf("Exists %d vez(es), Build %d", uc.exists, uc.builds)
		}
	})
	t.Run("o conjunto some entre a conferência e o cálculo", func(t *testing.T) {
		wantError(t, serve("POST", pattern, ChartHandler(&fakeChart{vanish: true}), "/api/datasets/A/chart", `{"type":"bar"}`), 404, notFound)
	})
}

// --- GET /api/datasets/{id}/csv ---

func TestCSV(t *testing.T) {
	const pattern = "/api/datasets/{id}/csv"
	uc := &fakeCSV{}
	w := serve("GET", pattern, CSVHandler(uc), "/api/datasets/A/csv?filter_key=fuel&filter_value=He%20said", "")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if uc.id != "A" || uc.key != "fuel" || uc.value != "He said" {
		t.Errorf("recebido %q, %q, %q", uc.id, uc.key, uc.value)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="atlas-A.csv"` {
		t.Errorf("content-disposition = %q", cd)
	}
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil || len(rows) != 3 || rows[0][0] != "source_file" {
		t.Fatalf("csv ilegível: %v, %v", rows, err)
	}
	if rows[1][2] != `He said "hot", twice` || rows[2][2] != "line\nbreak" { // aspas, vírgulas e quebras de linha saem escapadas
		t.Errorf("células = %q, %q", rows[1][2], rows[2][2])
	}

	w = serve("GET", pattern, CSVHandler(&fakeCSV{}), "/api/datasets/nope/csv", "")
	wantError(t, w, 404, notFound)
	if w.Header().Get("Content-Disposition") != "" {
		t.Error("o erro não é um arquivo para baixar")
	}
}
