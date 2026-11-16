package ask

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	domainask "atlas-api/domain/ask"
	"atlas-api/domain/entities"
)

// --- casos de uso falsos ---

type fakeAsk struct {
	out      entities.AskOut
	err      error
	got      entities.AskIn
	called   bool
	deadline time.Duration // quanto tempo o contexto dava ao caso de uso
}

func (f *fakeAsk) Ask(ctx context.Context, in entities.AskIn) (entities.AskOut, error) {
	f.called, f.got = true, in
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	return f.out, f.err
}

func (f *fakeAsk) Stream(_ context.Context, in entities.AskIn, emit domainask.Emitter) {
	f.called, f.got = true, in
	emit.Send("contexto", entities.AskContext{Understood: []string{"O₂: 30%"}})
	emit.Send("texto", map[string]string{"t": "partial"})
	emit.Send("resposta", f.out)
}

type fakeSearch struct {
	hits     []entities.Hit
	err      error
	q        string
	kinds    []string
	inv      string
	k        int
	called   bool
	deadline time.Duration
}

func (f *fakeSearch) Search(ctx context.Context, q string, kinds []string, inv string, k int) ([]entities.Hit, map[string]int, error) {
	f.called, f.q, f.kinds, f.inv, f.k = true, q, kinds, inv, k
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.hits, map[string]int{"atlas": len(f.hits)}, nil
}

type fakeInfer struct {
	text   string
	cur    *entities.Scenario
	called bool
}

func (f *fakeInfer) Describe(_ context.Context, text string, cur *entities.Scenario) domainask.InferResult {
	f.called, f.text, f.cur = true, text, cur
	return domainask.InferResult{Scenario: entities.Scenario{O2: 0.3, P: 0.694, Family: "solido"}, Understood: []string{"habitat: Lunar base"}, Method: "regras", Changed: true}
}

// --- apoio ---

func post(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/api/x", strings.NewReader(body)))
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
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
}

func jsonBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("a resposta não é JSON: %q", w.Body.String())
	}
	return body
}

func q(v any) string { b, _ := json.Marshal(v); return string(b) }

// As entradas que POST /api/ask e POST /api/ask/stream recusam antes de qualquer chamada paga.
var badQuestions = []struct{ name, body, msg string }{
	{"corpo vazio", ``, "write a question"},
	{"não é JSON", `{"question":`, "write a question"},
	{"tipo errado", `{"question": 42}`, "write a question"},
	{"lista no lugar do objeto", `["hi"]`, "write a question"},
	{"sem pergunta", `{}`, "write a question"},
	{"pergunta só de espaços", `{"question":"  \n\t "}`, "write a question"},
	{"pergunta longa demais", q(map[string]string{"question": strings.Repeat("a", maxQuestion+1)}), "the question is too long (at most 2000 characters)"},
	{"id de conversa longo demais", q(map[string]string{"question": "hi", "conversation_id": strings.Repeat("c", maxConversation+1)}), "invalid conversation_id"},
	{"corpo acima de 1 MB", q(map[string]string{"question": strings.Repeat("a", 2<<20)}), "write a question"},
}

// --- POST /api/ask ---

func TestAskRejectsBadInput(t *testing.T) {
	for _, c := range badQuestions {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeAsk{}
			wantError(t, post(AskHandler(uc), c.body), 400, c.msg)
			if uc.called {
				t.Error("o caso de uso foi chamado com uma entrada inválida")
			}
		})
	}
}

func TestAskAcceptsLimits(t *testing.T) {
	// o limite conta caracteres, não bytes: 2000 letras acentuadas passam
	body := q(map[string]any{"question": strings.Repeat("é", maxQuestion), "conversation_id": strings.Repeat("c", maxConversation),
		"scenario": map[string]any{"o2": 0.3, "pressure_atm": 0.694, "family": "solido"}})
	uc := &fakeAsk{out: entities.AskOut{Answer: "ok", Mode: "extrativo", Citations: []entities.Citation{}}}
	w := post(AskHandler(uc), body)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if uc.got.Scenario == nil || uc.got.Scenario.O2 != 0.3 || len(uc.got.Conversation) != maxConversation {
		t.Errorf("entrada recebida: %+v", uc.got)
	}
	// o caso de uso recebe um prazo abaixo dos 100 s em que o Cloudflare corta
	if uc.deadline <= 0 || uc.deadline > 90*time.Second {
		t.Errorf("prazo do pedido = %v, want até 90 s", uc.deadline)
	}
}

func TestAskSuccessShape(t *testing.T) {
	uc := &fakeAsk{out: entities.AskOut{Answer: "It burns [1].", Mode: "si", Provider: "anthropic", Model: "m",
		Citations: []entities.Citation{{N: 1, Ref: "T1", Kind: "teste"}}, Sources: map[string]int{"atlas": 2}, Warning: "w"}}
	w := post(AskHandler(uc), `{"question":"does it burn?"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := jsonBody(t, w)
	// os campos que o front lê, com estes nomes
	for _, k := range []string{"answer", "citations", "scenario", "scenario_changed", "understood", "verdict", "ranking", "mode", "provider",
		"model", "sources", "abstained", "top_similarity", "warning", "took_ms"} {
		if _, ok := body[k]; !ok {
			t.Errorf("faltou o campo %q na resposta", k)
		}
	}
	if body["answer"] != "It burns [1]." || body["mode"] != "si" {
		t.Errorf("corpo = %s", w.Body.String())
	}
}

// Um erro do caso de uso vira 502 com a mensagem fixa: a causa (endereço interno, erro do banco) fica no log.
func TestAskFailureDoesNotLeakCause(t *testing.T) {
	uc := &fakeAsk{err: errors.New("dial tcp 10.0.0.5:5432: connection refused (postgresql://atlas:secret@db)")}
	w := post(AskHandler(uc), `{"question":"does it burn?"}`)
	wantError(t, w, 502, unavailable)
	for _, leak := range []string{"10.0.0.5", "secret", "postgresql", "refused"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("a causa vazou para o cliente: %s", w.Body.String())
		}
	}
}

// --- POST /api/ask/stream ---

func TestAskStreamRejectsBadInput(t *testing.T) {
	for _, c := range badQuestions {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeAsk{}
			w := post(AskStreamHandler(uc), c.body)
			wantError(t, w, 400, c.msg) // recusado antes de abrir o stream: JSON comum, não SSE
			if uc.called {
				t.Error("o caso de uso foi chamado com uma entrada inválida")
			}
		})
	}
}

func TestAskStreamSendsEvents(t *testing.T) {
	uc := &fakeAsk{out: entities.AskOut{Answer: "done", Mode: "rag"}}
	w := post(AskStreamHandler(uc), `{"question":"does it burn?"}`)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d, content-type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w.Header().Get("X-Accel-Buffering") != "no" || !strings.Contains(w.Header().Get("Cache-Control"), "no-cache") {
		t.Errorf("cabeçalhos do stream: %v", w.Header())
	}
	var events []string
	for _, block := range strings.Split(strings.TrimSpace(w.Body.String()), "\n\n") {
		lines := strings.SplitN(block, "\n", 2)
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("bloco fora do formato SSE: %q", block)
		}
		if !json.Valid([]byte(strings.TrimPrefix(lines[1], "data: "))) {
			t.Errorf("dados que não são JSON: %q", lines[1])
		}
		events = append(events, strings.TrimPrefix(lines[0], "event: "))
	}
	if strings.Join(events, ",") != "contexto,texto,resposta" {
		t.Errorf("eventos = %v", events)
	}
	if !strings.Contains(w.Body.String(), `"answer":"done"`) {
		t.Errorf("corpo = %s", w.Body.String())
	}
}

// Sem http.Flusher não há como mandar eventos: erro no formato da API, sem chamar o caso de uso.
func TestAskStreamWithoutFlusher(t *testing.T) {
	rec := httptest.NewRecorder()
	plain := struct{ http.ResponseWriter }{rec} // só os métodos de ResponseWriter: sem Flush
	uc := &fakeAsk{}
	AskStreamHandler(uc)(plain, httptest.NewRequest("POST", "/api/ask/stream", strings.NewReader(`{"question":"q"}`)))
	wantError(t, rec, 500, "streaming is not available on this server")
	if uc.called {
		t.Error("o caso de uso foi chamado sem streaming")
	}
}

// --- POST /api/search ---

func TestSearchRejectsBadInput(t *testing.T) {
	cases := []struct{ name, body, msg string }{
		{"corpo vazio", ``, "provide a query"},
		{"não é JSON", `{`, "provide a query"},
		{"sem consulta", `{"k": 5}`, "provide a query"},
		{"consulta só de espaços", `{"query":"   "}`, "provide a query"},
		{"tipo errado", `{"query":"fire","k":"many"}`, "provide a query"},
		{"consulta longa demais", q(map[string]string{"query": strings.Repeat("a", maxSearch+1)}), "the query is too long (at most 500 characters)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeSearch{}
			wantError(t, post(SearchHandler(uc), c.body), 400, c.msg)
			if uc.called {
				t.Error("a busca foi chamada com uma entrada inválida")
			}
		})
	}
}

func TestSearchDefaultsAndLimits(t *testing.T) {
	all := []string{"catalogo", "documento", "recurso", "teste"}
	cases := []struct {
		name  string
		body  string
		k     int
		kinds []string
		inv   string
	}{
		{"só a consulta", `{"query":"flame"}`, 8, all, ""},
		{"k zero", `{"query":"flame","k":0}`, 8, all, ""},
		{"k negativo", `{"query":"flame","k":-4}`, 8, all, ""},
		{"k acima do teto", `{"query":"flame","k":31}`, 8, all, ""},
		{"k enorme", `{"query":"flame","k":1000000}`, 8, all, ""},
		{"k no teto", `{"query":"flame","k":30}`, 30, all, ""},
		{"filtros dados", `{"query":"flame","k":3,"investigation":"PSI-25","kinds":["documento"]}`, 3, []string{"documento"}, "PSI-25"},
		{"lista de tipos vazia", `{"query":"flame","kinds":[]}`, 8, all, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeSearch{hits: []entities.Hit{{ID: "a1"}}}
			w := post(SearchHandler(uc), c.body)
			if w.Code != 200 {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if uc.k != c.k || strings.Join(uc.kinds, ",") != strings.Join(c.kinds, ",") || uc.inv != c.inv || uc.q != "flame" {
				t.Errorf("busca chamada com k %d, tipos %v, investigação %q", uc.k, uc.kinds, uc.inv)
			}
			// a consulta ao banco tem prazo: antes recebia o contexto cru do pedido
			if uc.deadline <= 0 || uc.deadline > searchTimeout {
				t.Errorf("prazo da busca = %v, want até %v", uc.deadline, searchTimeout)
			}
		})
	}
}

func TestSearchResponses(t *testing.T) {
	// sem resultados a lista é [] e não null: o front faz .map nela
	w := post(SearchHandler(&fakeSearch{}), `{"query":"flame"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"hits":[]`) || !strings.Contains(w.Body.String(), `"sources":{"atlas":0}`) {
		t.Errorf("sem resultados: %d %s", w.Code, w.Body.String())
	}
	w = post(SearchHandler(&fakeSearch{hits: []entities.Hit{{ID: "a1", Origin: "atlas", Title: "Doc"}}}), `{"query":"flame"}`)
	body := jsonBody(t, w)
	hits, _ := body["hits"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["id"] != "a1" {
		t.Errorf("corpo = %s", w.Body.String())
	}
	// banco fora do ar: 502 com a mensagem fixa, sem o erro do pgx
	w = post(SearchHandler(&fakeSearch{err: errors.New(`ERROR: syntax error in tsquery: "fire &" (SQLSTATE 42601)`)}), `{"query":"flame"}`)
	wantError(t, w, 502, unavailable)
	if strings.Contains(w.Body.String(), "tsquery") || strings.Contains(w.Body.String(), "SQLSTATE") {
		t.Errorf("o erro do banco vazou: %s", w.Body.String())
	}
}

// --- POST /api/scenario/infer ---

func TestInferRejectsBadInput(t *testing.T) {
	cases := []struct{ name, body, msg string }{
		{"corpo vazio", ``, "describe the environment"},
		{"não é JSON", `nope`, "describe the environment"},
		{"sem texto", `{"scenario":{}}`, "describe the environment"},
		{"texto só de espaços", `{"text":" "}`, "describe the environment"},
		{"texto longo demais", q(map[string]string{"text": strings.Repeat("a", maxQuestion+1)}), "the description is too long (at most 2000 characters)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uc := &fakeInfer{}
			wantError(t, post(InferHandler(uc), c.body), 400, c.msg)
			if uc.called {
				t.Error("a inferência foi chamada com uma entrada inválida")
			}
		})
	}
}

func TestInferSuccess(t *testing.T) {
	uc := &fakeInfer{}
	w := post(InferHandler(uc), `{"text":"a lunar base","scenario":{"o2":0.21,"pressure_atm":1,"family":"solido"}}`)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if uc.text != "a lunar base" || uc.cur == nil || uc.cur.O2 != 0.21 {
		t.Errorf("recebido: %q, %+v", uc.text, uc.cur)
	}
	body := jsonBody(t, w)
	for _, k := range []string{"scenario", "understood", "method", "changed"} {
		if _, ok := body[k]; !ok {
			t.Errorf("faltou o campo %q", k)
		}
	}
	// sem cenário no corpo o caso de uso recebe nil (e parte do padrão)
	uc = &fakeInfer{}
	post(InferHandler(uc), `{"text":"a lunar base"}`)
	if uc.cur != nil {
		t.Errorf("cenário = %+v, want nil", uc.cur)
	}
}
