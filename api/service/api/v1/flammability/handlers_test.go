package flammability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"atlas-api/domain/entities"
)

type fakeGet struct{ acc string }

func (f *fakeGet) Get(acc string) map[string]any {
	f.acc = acc
	if acc == "PSI-404" {
		return map[string]any{"available": false, "investigation": acc}
	}
	return map[string]any{"available": true, "investigation": "PSI-25", "forms": []entities.FormCount{{Form: "bastão", N: 2}}}
}

type fakeAssess struct {
	s         entities.Scenario
	forced    *float64
	form      string
	called    bool
	available bool
}

func (f *fakeAssess) Assess(s entities.Scenario, forced *float64, form string) (entities.Assessment, bool) {
	f.called, f.s, f.forced, f.form = true, s, forced, form
	return entities.Assessment{Form: "bastão", Status: "queima", Headline: "The flame sustains itself", Outside: []string{}}, f.available
}

type fakeQueue struct{}

func (fakeQueue) Get() map[string]any {
	return map[string]any{"available": true, "queue": []entities.Proposal{{Rank: 1, ID: "g1"}}}
}

func call(h http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(method, target, strings.NewReader(body)))
	return w
}

func object(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("a resposta não é um objeto JSON: %q", w.Body.String())
	}
	return body
}

// GET /api/flammability?investigation=
func TestFlammabilityHandler(t *testing.T) {
	cases := []struct {
		name, target, acc string
		available         bool
	}{
		{"sem parâmetro (o caso de uso escolhe o BASS-II)", "/api/flammability", "", true},
		{"investigação dada", "/api/flammability?investigation=PSI-25", "PSI-25", true},
		{"investigação sem desfechos: 200 com available false", "/api/flammability?investigation=PSI-404", "PSI-404", false},
		{"valor com caracteres escapados", "/api/flammability?investigation=PSI%2D25", "PSI-25", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeGet{}
			w := call(FlammabilityHandler(uc), "GET", c.target, "")
			if w.Code != 200 || uc.acc != c.acc {
				t.Fatalf("status %d, investigação recebida %q", w.Code, uc.acc)
			}
			if body := object(t, w); body["available"] != c.available {
				t.Errorf("corpo = %v", body)
			}
		})
	}
}

// POST /api/flammability/assess
func TestAssessHandler(t *testing.T) {
	for name, body := range map[string]string{"corpo vazio": ``, "não é JSON": `{"o2":`, "tipo errado": `{"forced_cm_s":"forte"}`, "lista": `[]`} {
		t.Run(name, func(t *testing.T) {
			uc := &fakeAssess{available: true}
			w := call(AssessHandler(uc), "POST", "/api/flammability/assess", body)
			got := object(t, w)
			if w.Code != 400 || len(got) != 1 || got["error"] != "invalid body" {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if uc.called {
				t.Error("o caso de uso foi chamado com um corpo inválido")
			}
		})
	}
	t.Run("cenário, vento e forma chegam ao caso de uso", func(t *testing.T) {
		uc := &fakeAssess{available: true}
		w := call(AssessHandler(uc), "POST", "/api/flammability/assess",
			`{"o2":0.3,"pressure_atm":0.694,"gravity":0.165,"family":"solido","material":"pmma","forced_cm_s":25,"form":"placa"}`)
		body := object(t, w)
		if w.Code != 200 || len(body) != 2 || body["available"] != true {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
		if uc.s.O2 != 0.3 || uc.s.P != 0.694 || uc.s.G != 0.165 || uc.s.Material != "pmma" || uc.forced == nil || *uc.forced != 25 || uc.form != "placa" {
			t.Errorf("recebido: %+v, vento %v, forma %q", uc.s, uc.forced, uc.form)
		}
		as := body["assessment"].(map[string]any)
		for _, k := range []string{"form", "status", "headline", "detail", "o2", "limit_o2", "band_o2", "margin_o2", "forced_cm_s", "buoyant_cm_s",
			"flow_cm_s", "dilution_min", "inside_tested", "outside", "source"} {
			if _, ok := as[k]; !ok {
				t.Errorf("faltou o campo %q na avaliação", k)
			}
		}
	})
	t.Run("sem vento no corpo o caso de uso recebe nil", func(t *testing.T) {
		uc := &fakeAssess{available: true}
		call(AssessHandler(uc), "POST", "/api/flammability/assess", `{"o2":0.21}`)
		if uc.forced != nil || uc.form != "" {
			t.Errorf("vento %v, forma %q", uc.forced, uc.form)
		}
	})
	t.Run("sem curva carregada: 200 com available false e sem avaliação", func(t *testing.T) {
		w := call(AssessHandler(&fakeAssess{}), "POST", "/api/flammability/assess", `{"o2":0.21}`)
		body := object(t, w)
		if w.Code != 200 || len(body) != 1 || body["available"] != false {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
}

// GET /api/flammability/queue
func TestQueueHandler(t *testing.T) {
	w := call(QueueHandler(fakeQueue{}), "GET", "/api/flammability/queue", "")
	body := object(t, w)
	if w.Code != 200 || body["available"] != true || len(body["queue"].([]any)) != 1 {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}
