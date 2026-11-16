package predict

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"atlas-api/domain/entities"
	domainpredict "atlas-api/domain/predict"
)

type fakeBurn struct {
	err    error
	in     entities.PredictIn
	called bool
}

func (f *fakeBurn) Predict(in entities.PredictIn) (entities.PredictOut, error) {
	f.called, f.in = true, in
	if f.err != nil {
		return entities.PredictOut{}, f.err
	}
	return entities.PredictOut{ModelID: "flex_k_heptane", K: 0.7, InRange: true, OutOfRange: []string{}, Level: "interpolado"}, nil
}

type fakeSpread struct {
	in     entities.SpreadIn
	called bool
}

func (f *fakeSpread) Predict(in entities.SpreadIn) entities.SpreadOut {
	f.called, f.in = true, in
	return entities.SpreadOut{Available: true, V: 1.5, Level: "interpolado", OutOfRange: []string{}, Caveats: []string{}}
}

type fakeThermal struct {
	in     domainpredict.ThermalIn
	called bool
}

func (f *fakeThermal) Predict(_ context.Context, in domainpredict.ThermalIn) map[string]any {
	f.called, f.in = true, in
	return map[string]any{"available": false, "reason": "The thermal model service did not answer.", "compare": in.Compare}
}

func post(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/api/predict/x", strings.NewReader(body)))
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

func wantError(t *testing.T, w *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	body := object(t, w)
	if w.Code != code || len(body) != 1 || body["error"] != msg {
		t.Fatalf(`got %d %s, want %d {"error": %q}`, w.Code, w.Body.String(), code, msg)
	}
}

var badBodies = map[string]string{"corpo vazio": ``, "não é JSON": `{"o2":`, "tipo errado": `{"o2":"muito"}`, "lista": `[1]`}

// POST /api/predict/burn-rate
func TestBurnRateHandler(t *testing.T) {
	for name, body := range badBodies {
		t.Run(name, func(t *testing.T) {
			uc := &fakeBurn{}
			wantError(t, post(BurnRateHandler(uc), body), 400, "invalid input")
			if uc.called {
				t.Error("o caso de uso foi chamado com um corpo inválido")
			}
		})
	}
	t.Run("sucesso", func(t *testing.T) {
		uc := &fakeBurn{}
		w := post(BurnRateHandler(uc), `{"fuel":"Heptane","o2":0.21,"co2":0.3,"he":0,"pressure_atm":1,"d0_mm":2.5,"replace_air":true}`)
		body := object(t, w)
		if w.Code != 200 || body["model_id"] != "flex_k_heptane" || body["k_mm2_s"] != 0.7 {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
		in := uc.in
		if in.Fuel != "Heptane" || in.O2 != 0.21 || in.CO2 != 0.3 || in.P != 1 || in.D0 == nil || *in.D0 != 2.5 || !in.Replace {
			t.Errorf("entrada recebida: %+v", in)
		}
		for _, k := range []string{"model_id", "o2_effective", "k_mm2_s", "baseline_k_mm2_s", "delta_pct", "in_range", "out_of_range", "level", "model", "note"} {
			if _, ok := body[k]; !ok {
				t.Errorf("faltou o campo %q", k)
			}
		}
	})
	// a recusa do modelo é uma mensagem de validação escrita para o cliente: sai como veio, com 400
	t.Run("entrada recusada pelo modelo", func(t *testing.T) {
		uc := &fakeBurn{err: errors.New(`model "Kerosene" does not exist; use Methanol or Heptane`)}
		wantError(t, post(BurnRateHandler(uc), `{"fuel":"Kerosene"}`), 400, `model "Kerosene" does not exist; use Methanol or Heptane`)
	})
}

// POST /api/predict/spread
func TestSpreadHandler(t *testing.T) {
	for name, body := range badBodies {
		t.Run(name, func(t *testing.T) {
			uc := &fakeSpread{}
			wantError(t, post(SpreadHandler(uc), body), 400, "invalid input")
			if uc.called {
				t.Error("o caso de uso foi chamado com um corpo inválido")
			}
		})
	}
	uc := &fakeSpread{}
	w := post(SpreadHandler(uc), `{"o2":0.3,"pressure_atm":0.694,"gravity":0.165,"family":"solido","wind_cm_s":15}`)
	body := object(t, w)
	if w.Code != 200 || body["available"] != true || body["v_mm_s"] != 1.5 {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if uc.in.O2 != 0.3 || uc.in.P != 0.694 || uc.in.G != 0.165 || uc.in.Wind == nil || *uc.in.Wind != 15 {
		t.Errorf("entrada recebida: %+v", uc.in)
	}
	uc = &fakeSpread{}
	post(SpreadHandler(uc), `{"o2":0.21}`)
	if uc.in.Wind != nil { // sem vento no corpo o caso de uso usa o padrão
		t.Errorf("vento = %v, want nil", *uc.in.Wind)
	}
}

// POST /api/predict/thermal
func TestThermalHandler(t *testing.T) {
	for name, body := range badBodies {
		t.Run(name, func(t *testing.T) {
			uc := &fakeThermal{}
			wantError(t, post(ThermalHandler(uc), body), 400, "invalid input")
			if uc.called {
				t.Error("o caso de uso foi chamado com um corpo inválido")
			}
		})
	}
	uc := &fakeThermal{}
	w := post(ThermalHandler(uc), `{"o2":0.21,"pressure_atm":1,"wind_cm_s":10,"compare":true}`)
	body := object(t, w)
	// serviço fora do ar ainda é 200: available diz se há filme, e reason por que não
	if w.Code != 200 || body["available"] != false || body["compare"] != true || body["reason"] == "" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if !uc.in.Compare || uc.in.O2 != 0.21 || uc.in.Wind == nil || *uc.in.Wind != 10 {
		t.Errorf("entrada recebida: %+v", uc.in)
	}
}
