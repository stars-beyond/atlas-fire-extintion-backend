package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// stubApi preenche todos os handlers com um que responde o nome do campo, para saber qual rota chegou onde.
func stubApi() *Api {
	a := &Api{}
	v := reflect.ValueOf(a).Elem()
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		v.Field(i).Set(reflect.ValueOf(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Handler", name)
			w.Header().Set("X-Id", r.PathValue("id"))
		})))
	}
	return a
}

// Cada rota que o front chama, com o método e o handler dela.
var routes = []struct{ method, path, handler, id string }{
	{"GET", "/api/health", "Health", ""},
	{"GET", "/api/meta", "Meta", ""},
	{"GET", "/api/investigations", "Investigations", ""},
	{"GET", "/api/investigations/PSI-25", "Investigation", "PSI-25"},
	{"GET", "/api/tests", "Tests", ""},
	{"GET", "/api/tests/t1", "Test", "t1"},
	{"GET", "/api/external", "External", ""},
	{"POST", "/api/verdict", "Verdict", ""},
	{"POST", "/api/rank", "Rank", ""},
	{"POST", "/api/next-tests", "NextTests", ""},
	{"POST", "/api/graph", "Graph", ""},
	{"GET", "/api/insights", "Insights", ""},
	{"POST", "/api/ask", "Ask", ""},
	{"POST", "/api/ask/stream", "AskStream", ""},
	{"POST", "/api/search", "Search", ""},
	{"POST", "/api/scenario/infer", "Infer", ""},
	{"GET", "/api/flammability", "Flammability", ""},
	{"POST", "/api/flammability/assess", "Assess", ""},
	{"GET", "/api/flammability/queue", "Queue", ""},
	{"POST", "/api/predict/burn-rate", "BurnRate", ""},
	{"POST", "/api/predict/spread", "Spread", ""},
	{"POST", "/api/predict/thermal", "Thermal", ""},
	{"GET", "/api/sim/config", "SimConfig", ""},
	{"GET", "/api/sim/validation", "SimValidation", ""},
	{"GET", "/api/datasets", "Datasets", ""},
	{"GET", "/api/datasets/A/records", "Records", "A"},
	{"POST", "/api/datasets/A/chart", "Chart", "A"},
	{"GET", "/api/datasets/A/csv", "CSV", "A"},
	{"GET", "/api/missions/presets", "MissionPresets", ""},
	{"POST", "/api/missions/simulations", "MissionSimulate", ""},
}

func TestRoutes(t *testing.T) {
	h := stubApi().Routes()
	seen := map[string]bool{}
	for _, r := range routes {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(r.method, r.path, nil))
		if w.Code != 200 || w.Header().Get("X-Handler") != r.handler || w.Header().Get("X-Id") != r.id {
			t.Errorf("%s %s: status %d, handler %q, id %q; want %s", r.method, r.path, w.Code, w.Header().Get("X-Handler"), w.Header().Get("X-Id"), r.handler)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s %s sem CORS", r.method, r.path)
		}
		seen[r.handler] = true

		// o método trocado não chega ao handler
		other := "POST"
		if r.method == "POST" {
			other = "GET"
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(other, r.path, nil))
		if w.Header().Get("X-Handler") == r.handler {
			t.Errorf("%s %s chegou ao handler %s", other, r.path, r.handler)
		}
	}
	// nenhum handler de Api fica sem rota
	v := reflect.TypeOf(Api{})
	for i := 0; i < v.NumField(); i++ {
		if !seen[v.Field(i).Name] {
			t.Errorf("o handler %s não tem rota conferida aqui", v.Field(i).Name)
		}
	}
}

func TestRoutesUnknownPathAndPreflight(t *testing.T) {
	h := stubApi().Routes()
	for _, p := range []string{"/", "/api", "/api/nope", "/api/tests/a/b", "/api/datasets/A", "/metrics", "/api/../etc/passwd"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Header().Get("X-Handler") != "" || w.Code == 200 {
			t.Errorf("GET %s: status %d, handler %q", p, w.Code, w.Header().Get("X-Handler"))
		}
	}
	// preflight do navegador: 204 com os cabeçalhos, sem chegar a handler nenhum
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("OPTIONS", "/api/ask", nil))
	if w.Code != 204 || w.Header().Get("X-Handler") != "" || !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "POST") ||
		w.Header().Get("Access-Control-Allow-Headers") != "content-type" {
		t.Errorf("preflight: %d %v", w.Code, w.Header())
	}
}

// Um pânico em qualquer handler vira 500 no formato de erro da API, com CORS (senão o navegador esconde o erro).
func TestRoutesRecoverFromPanic(t *testing.T) {
	a := stubApi()
	a.Verdict = func(http.ResponseWriter, *http.Request) { panic("nil map em /srv/atlas") }
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, httptest.NewRequest("POST", "/api/verdict", nil))
	var body map[string]string
	if w.Code != 500 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["error"] == "" || strings.Contains(w.Body.String(), "/srv/") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("o erro saiu sem CORS")
	}
}
