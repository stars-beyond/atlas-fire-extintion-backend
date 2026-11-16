package evidence

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

type fakeVerdict struct {
	got    entities.Scenario
	called bool
}

func (f *fakeVerdict) Get(s entities.Scenario) (entities.Verdict, []entities.MatrixCell) {
	f.called, f.got = true, s
	return entities.Verdict{Tier: "medido", Label: "Measured", Outcomes: map[string]int{}, Scenario: s},
		[]entities.MatrixCell{{Family: "solido", Tier: "medido", Label: "Measured", N: 3}}
}

type fakeRank struct {
	err      error
	got      entities.Scenario
	question string
	called   bool
}

func (f *fakeRank) Rank(_ context.Context, s entities.Scenario, question string) ([]entities.Ranked, string, error) {
	f.called, f.got, f.question = true, s, question
	if f.err != nil {
		return nil, "", f.err
	}
	return []entities.Ranked{{ID: "PSI-25", Name: "BASS-II", Score: 0.9}}, "the query used", nil
}

type fakeNext struct {
	family, material string
	calls            int
}

func (f *fakeNext) Next(family, material string) []entities.NextTest {
	f.calls++
	f.family, f.material = family, material
	return []entities.NextTest{{O2: 0.34, P: 0.558, Platform: "ISS", Gain: 1, Closes: []string{"x"}, Why: "y"}}
}

type fakeGraph struct {
	got    entities.Scenario
	called bool
}

func (f *fakeGraph) Build(s entities.Scenario) ([]entities.GNode, []entities.GEdge) {
	f.called, f.got = true, s
	return []entities.GNode{{ID: "cenario", Type: "cenario"}}, []entities.GEdge{{Source: "cenario", Target: "t:1", Type: "PROXIMO"}}
}

type fakeInsights struct {
	err      error
	deadline time.Duration
}

func (f *fakeInsights) List(ctx context.Context) ([]entities.Insight, error) {
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	if f.err != nil {
		return nil, f.err
	}
	return []entities.Insight{{ID: "cobertura", Title: "t", Finding: "f", Source: "s", Data: []int{1}}}, nil
}

// --- apoio ---

func call(h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(method, "/api/x", strings.NewReader(body)))
	return w
}

// wantError confere o código e o formato de erro da API: um objeto JSON com o campo error e mais nada.
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

func keys(t *testing.T, w *httptest.ResponseRecorder, want ...string) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("a resposta não é JSON: %q", w.Body.String())
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

const scenario = `{"o2":0.3,"pressure_atm":0.694,"gravity":0.165,"family":"solido","material":"pmma"}`

var badBodies = []struct{ name, body string }{
	{"corpo vazio", ``},
	{"não é JSON", `{"o2":`},
	{"tipo errado", `{"o2":"muito"}`},
	{"lista", `[1,2]`},
}

// --- POST /api/verdict ---

func TestVerdictHandler(t *testing.T) {
	for _, c := range badBodies {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeVerdict{}
			wantError(t, call(VerdictHandler(uc), "POST", c.body), 400, "invalid scenario")
			if uc.called {
				t.Error("o caso de uso foi chamado com um corpo inválido")
			}
		})
	}
	uc := &fakeVerdict{}
	body := keys(t, call(VerdictHandler(uc), "POST", scenario), "verdict", "matrix")
	if uc.got.O2 != 0.3 || uc.got.P != 0.694 || uc.got.G != 0.165 || uc.got.Family != "solido" || uc.got.Material != "pmma" {
		t.Errorf("cenário recebido: %+v", uc.got)
	}
	if body["verdict"].(map[string]any)["tier"] != "medido" || len(body["matrix"].([]any)) != 1 {
		t.Errorf("corpo = %v", body)
	}
	// campos desconhecidos são ignorados, e um objeto vazio é um cenário (o caso de uso o completa)
	if w := call(VerdictHandler(&fakeVerdict{}), "POST", `{"extra":true}`); w.Code != 200 {
		t.Errorf("objeto sem campos: %d", w.Code)
	}
}

// --- POST /api/rank ---

func TestRankHandler(t *testing.T) {
	for _, c := range badBodies {
		t.Run(c.name, func(t *testing.T) {
			body := c.body
			if c.name == "tipo errado" {
				body = `{"question": 7}`
			}
			uc := &fakeRank{}
			wantError(t, call(RankHandler(uc), "POST", body), 400, "invalid input")
			if uc.called {
				t.Error("o caso de uso foi chamado com um corpo inválido")
			}
		})
	}
	t.Run("pergunta longa demais", func(t *testing.T) {
		uc := &fakeRank{}
		b, _ := json.Marshal(map[string]string{"question": strings.Repeat("é", maxQuestion+1)})
		wantError(t, call(RankHandler(uc), "POST", string(b)), 400, "the question is too long (at most 2000 characters)")
		if uc.called {
			t.Error("a pergunta longa chegou ao serviço de embeddings")
		}
	})
	t.Run("pergunta no limite", func(t *testing.T) {
		b, _ := json.Marshal(map[string]string{"question": strings.Repeat("é", maxQuestion)}) // caracteres, não bytes
		if w := call(RankHandler(&fakeRank{}), "POST", string(b)); w.Code != 200 {
			t.Errorf("status = %d", w.Code)
		}
	})
	t.Run("sucesso", func(t *testing.T) {
		uc := &fakeRank{}
		body := keys(t, call(RankHandler(uc), "POST", `{"scenario":`+scenario+`,"question":"flame spread"}`), "ranking", "query")
		if uc.got.O2 != 0.3 || uc.question != "flame spread" || body["query"] != "the query used" {
			t.Errorf("recebido %+v %q, corpo %v", uc.got, uc.question, body)
		}
	})
	t.Run("serviço fora do ar", func(t *testing.T) {
		w := call(RankHandler(&fakeRank{err: errors.New("Post http://brain:8000/embed: connection refused")}), "POST", `{}`)
		wantError(t, w, 502, "embedding service unavailable")
		if strings.Contains(w.Body.String(), "brain") {
			t.Errorf("o endereço interno vazou: %s", w.Body.String())
		}
	})
}

// --- POST /api/next-tests ---

func TestNextTestsHandler(t *testing.T) {
	cases := []struct {
		name, body, family, material string
	}{
		{"família e material", `{"family":"solido","material":"pmma"}`, "solido", "pmma"},
		{"só a família", `{"family":"gota"}`, "gota", ""},
		{"objeto vazio", `{}`, "", ""},
		// este endpoint nunca recusou o corpo: sem corpo, ou com corpo ilegível, vale o padrão
		{"sem corpo", ``, "", ""},
		{"não é JSON", `{"family":`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeNext{}
			body := keys(t, call(NextTestsHandler(uc, "the method"), "POST", c.body), "next", "method")
			if uc.calls != 1 || uc.family != c.family || uc.material != c.material {
				t.Errorf("recebido %q / %q em %d chamadas", uc.family, uc.material, uc.calls)
			}
			next := body["next"].([]any)
			if body["method"] != "the method" || len(next) != 1 || next[0].(map[string]any)["pressure_atm"] != 0.558 {
				t.Errorf("corpo = %v", body)
			}
		})
	}
}

// --- POST /api/graph ---

func TestGraphHandler(t *testing.T) {
	for _, c := range badBodies {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeGraph{}
			wantError(t, call(GraphHandler(uc), "POST", c.body), 400, "invalid scenario")
			if uc.called {
				t.Error("o caso de uso foi chamado com um corpo inválido")
			}
		})
	}
	uc := &fakeGraph{}
	body := keys(t, call(GraphHandler(uc), "POST", scenario), "nodes", "edges")
	if uc.got.Material != "pmma" || len(body["nodes"].([]any)) != 1 || len(body["edges"].([]any)) != 1 {
		t.Errorf("recebido %+v, corpo %v", uc.got, body)
	}
}

// --- GET /api/insights ---

func TestInsightsHandler(t *testing.T) {
	uc := &fakeInsights{}
	body := keys(t, call(InsightsHandler(uc), "GET", ""), "insights")
	list := body["insights"].([]any)
	if len(list) != 1 {
		t.Fatalf("corpo = %v", body)
	}
	first := list[0].(map[string]any)
	for _, k := range []string{"id", "title", "finding", "source", "data"} {
		if _, ok := first[k]; !ok {
			t.Errorf("faltou o campo %q no achado", k)
		}
	}
	// as leituras do banco têm prazo: antes recebiam o contexto cru do pedido
	if uc.deadline <= 0 || uc.deadline > insightsTimeout {
		t.Errorf("prazo = %v, want até %v", uc.deadline, insightsTimeout)
	}
}

// Banco fora do ar: 503 com a mensagem fixa. Antes saía 200 com estatísticas zeradas.
func TestInsightsHandlerDatabaseDown(t *testing.T) {
	uc := &fakeInsights{err: errors.New("reading FLEX burning rates: failed to connect to `host=db user=atlas`: dial tcp 172.18.0.2:5432: connect: connection refused")}
	w := call(InsightsHandler(uc), "GET", "")
	wantError(t, w, 503, "the archive database did not answer; try again in a few seconds")
	for _, leak := range []string{"172.18", "host=db", "atlas`", "FLEX", "refused"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("a causa vazou para o cliente (%q): %s", leak, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "insights") {
		t.Errorf("no erro não pode haver achados: %s", w.Body.String())
	}
}
