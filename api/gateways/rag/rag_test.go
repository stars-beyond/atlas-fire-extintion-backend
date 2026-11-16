package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRag é o serviço FastAPI do RAG de mentira: cria conversas e responde às mensagens como o teste mandar.
type fakeRag struct {
	t        *testing.T
	mu       sync.Mutex
	created  int      // conversas criadas
	paths    []string // caminhos pedidos, na ordem
	bodies   []string
	stream   func(w http.ResponseWriter, r *http.Request, conversation string) // POST .../messages/stream
	message  func(w http.ResponseWriter, r *http.Request, conversation string) // POST .../messages
	health   func(w http.ResponseWriter)
	accepted []string // cabeçalho Accept dos pedidos de streaming
}

func (f *fakeRag) server() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.paths = append(f.paths, r.Method+" "+r.URL.Path)
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/health":
			if f.health != nil {
				f.health(w)
				return
			}
			fmt.Fprint(w, `{"rag":{"ready":true,"chunks":4321,"detail":"ok"}}`)
		case r.URL.Path == "/conversations/" && r.Method == http.MethodPost:
			f.mu.Lock()
			f.created++
			id := fmt.Sprintf("new%d", f.created)
			f.mu.Unlock()
			fmt.Fprintf(w, `{"id":%q}`, id)
		case strings.HasSuffix(r.URL.Path, "/messages/stream"):
			f.mu.Lock()
			f.accepted = append(f.accepted, r.Header.Get("Accept"))
			f.mu.Unlock()
			f.stream(w, r, strings.Split(r.URL.Path, "/")[2])
		case strings.HasSuffix(r.URL.Path, "/messages"):
			f.message(w, r, strings.Split(r.URL.Path, "/")[2])
		default:
			f.t.Errorf("pedido inesperado: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	f.t.Cleanup(srv.Close)
	return srv
}

// sse escreve o corpo de um text/event-stream, linha a linha, como o RAG.
func sse(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	for _, l := range lines {
		fmt.Fprint(w, l+"\n")
	}
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
}

const pronto = `data: {"ai_message":{"content":"Flames spread [1].","sources":[{"citation":1,"title":"BASS-II report","page":12,"cited":true,"excerpt":"spread"}]}}`

func collect() (func(string), *[]string) {
	var got []string
	return func(t string) { got = append(got, t) }, &got
}

// --- o parser do streaming ---

func TestAskStreamParsesEvents(t *testing.T) {
	f := &fakeRag{t: t}
	f.stream = func(w http.ResponseWriter, _ *http.Request, _ string) {
		sse(w,
			": ping", "", // comentário de keep-alive
			"event: inicio", `data: {"conversation":"x"}`, "", // evento desconhecido: ignorado
			"event: texto", `data: {"t":"Flames "}`, "",
			"event: texto", `data: {"t":""}`, "", // pedaço vazio não é repassado
			"event: texto", `data: not json`, "", // pedaço ilegível é pulado, sem derrubar a resposta
			"event: texto", `data: {"t":"spread [1]."}`, "",
			"id: 7", "retry: 1000", // campos do SSE que o cliente não usa
			"event: pronto", pronto, "",
			"event: texto", `data: {"t":"depois do fim"}`, "", // nada depois de pronto é lido
		)
	}
	c := New(f.server().URL + "/") // a barra final da URL não duplica
	onText, got := collect()
	ans, err := c.AskStream(context.Background(), "", "does acrylic burn?", onText)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*got, "|") != "Flames |spread [1]." {
		t.Errorf("pedaços = %q", *got)
	}
	if ans.Content != "Flames spread [1]." || ans.Conversation != "new1" || len(ans.Sources) != 1 {
		t.Fatalf("resposta = %+v", ans)
	}
	if s := ans.Sources[0]; s.Citation != 1 || s.Title != "BASS-II report" || s.Page == nil || *s.Page != 12 || s.Cited == nil || !*s.Cited {
		t.Errorf("fonte = %+v", s)
	}
	if want := []string{"POST /conversations/", "POST /conversations/new1/messages/stream"}; strings.Join(f.paths, ",") != strings.Join(want, ",") {
		t.Errorf("pedidos = %v", f.paths)
	}
	var sent map[string]string
	if json.Unmarshal([]byte(f.bodies[1]), &sent) != nil || sent["content"] != "does acrylic burn?" {
		t.Errorf("corpo enviado = %s", f.bodies[1])
	}
	if f.accepted[0] != "text/event-stream" {
		t.Errorf("Accept = %q", f.accepted[0])
	}
}

func TestAskStreamUsesGivenConversation(t *testing.T) {
	f := &fakeRag{t: t}
	f.stream = func(w http.ResponseWriter, _ *http.Request, _ string) { sse(w, "event: pronto", pronto, "") }
	ans, err := New(f.server().URL).AskStream(context.Background(), "abc", "q", func(string) {})
	if err != nil || ans.Conversation != "abc" {
		t.Fatalf("got %+v, %v", ans, err)
	}
	if f.created != 0 || len(f.paths) != 1 || f.paths[0] != "POST /conversations/abc/messages/stream" {
		t.Errorf("com a conversa dada nenhuma outra é criada: %v", f.paths)
	}
}

func TestAskStreamFailures(t *testing.T) {
	big := strings.Repeat("x", 200<<10) // um evento maior que o buffer inicial do leitor (64 KiB)
	cases := []struct {
		name    string
		stream  func(w http.ResponseWriter, r *http.Request, conv string)
		wantErr string
		texts   int
	}{
		{"evento de erro com detalhe", func(w http.ResponseWriter, _ *http.Request, _ string) {
			sse(w, "event: texto", `data: {"t":"partial"}`, "", "event: erro", `data: {"status":500,"detail":"model overloaded"}`, "")
		}, "model overloaded", 1},
		{"evento de erro sem detalhe", func(w http.ResponseWriter, _ *http.Request, _ string) {
			sse(w, "event: erro", `data: {"status":503}`, "")
		}, "the RAG responded 503", 0},
		{"HTTP de erro", func(w http.ResponseWriter, _ *http.Request, _ string) {
			http.Error(w, "internal stack trace", 500)
		}, "the RAG responded HTTP 500", 0},
		{"a resposta acaba sem o evento final", func(w http.ResponseWriter, _ *http.Request, _ string) {
			sse(w, "event: texto", `data: {"t":"partial"}`, "")
		}, "ended the answer before it finished", 1},
		{"corpo vazio", func(w http.ResponseWriter, _ *http.Request, _ string) { sse(w) }, "ended the answer before it finished", 0},
		{"evento final ilegível", func(w http.ResponseWriter, _ *http.Request, _ string) {
			sse(w, "event: pronto", `data: {"ai_message":`, "")
		}, "unexpected end of JSON input", 0},
		{"evento grande ainda é lido", func(w http.ResponseWriter, _ *http.Request, _ string) {
			sse(w, "event: texto", `data: {"t":"`+big+`"}`, "", "event: erro", `data: {"detail":"after big"}`, "")
		}, "after big", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeRag{t: t, stream: c.stream}
			onText, got := collect()
			ans, err := New(f.server().URL).AskStream(context.Background(), "", "q", onText)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("erro = %v, want %q", err, c.wantErr)
			}
			if strings.Contains(err.Error(), "stack trace") {
				t.Errorf("o corpo do erro HTTP entrou na mensagem: %v", err)
			}
			if len(*got) != c.texts {
				t.Errorf("pedaços repassados = %d, want %d", len(*got), c.texts)
			}
			if ans.Conversation != "new1" || ans.Content != "" {
				t.Errorf("no erro a conversa volta para a próxima tentativa, sem texto: %+v", ans)
			}
		})
	}
}

// Conversa de uma sessão anterior (o RAG reiniciou): começa outra, uma vez só.
func TestAskStreamRestartsUnknownConversation(t *testing.T) {
	f := &fakeRag{t: t}
	f.stream = func(w http.ResponseWriter, _ *http.Request, conv string) {
		if conv == "old" {
			sse(w, "event: erro", `data: {"status":404,"detail":"conversation not found"}`, "")
			return
		}
		sse(w, "event: texto", `data: {"t":"hi"}`, "", "event: pronto", pronto, "")
	}
	onText, got := collect()
	ans, err := New(f.server().URL).AskStream(context.Background(), "old", "q", onText)
	if err != nil || ans.Conversation != "new1" || len(*got) != 1 {
		t.Fatalf("got %+v, %v, pedaços %v", ans, err, *got)
	}
	if f.created != 1 {
		t.Errorf("conversas criadas = %d, want 1", f.created)
	}

	// se a conversa nova também dá 404, não fica tentando para sempre
	f2 := &fakeRag{t: t}
	f2.stream = func(w http.ResponseWriter, _ *http.Request, _ string) {
		sse(w, "event: erro", `data: {"status":404,"detail":"conversation not found"}`, "")
	}
	if _, err := New(f2.server().URL).AskStream(context.Background(), "old", "q", func(string) {}); err == nil || f2.created != 1 {
		t.Fatalf("erro = %v, conversas criadas = %d (want erro e 1)", err, f2.created)
	}

	// com texto já enviado ao cliente não dá para recomeçar: é erro
	f3 := &fakeRag{t: t}
	f3.stream = func(w http.ResponseWriter, _ *http.Request, _ string) {
		sse(w, "event: texto", `data: {"t":"partial"}`, "", "event: erro", `data: {"status":404,"detail":"gone"}`, "")
	}
	if _, err := New(f3.server().URL).AskStream(context.Background(), "old", "q", func(string) {}); err == nil || f3.created != 0 {
		t.Fatalf("erro = %v, conversas criadas = %d (want erro e 0)", err, f3.created)
	}
}

// RAG travado no meio da resposta: o prazo do contexto corta a leitura e devolve o erro do prazo.
func TestAskStreamStopsAtContextDeadline(t *testing.T) {
	f := &fakeRag{t: t}
	release := make(chan struct{})
	f.stream = func(w http.ResponseWriter, r *http.Request, _ string) {
		sse(w, "event: texto", `data: {"t":"partial"}`, "")
		select { // nunca manda o evento final
		case <-r.Context().Done():
		case <-release:
		}
	}
	srv := f.server()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	onText, got := collect()
	t0 := time.Now()
	_, err := New(srv.URL).AskStream(ctx, "", "q", onText)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("esperava o erro do prazo, veio %v", err)
	}
	if d := time.Since(t0); d > 3*time.Second {
		t.Fatalf("levou %v para desistir", d)
	}
	if len(*got) != 1 {
		t.Errorf("o pedaço recebido antes do corte devia ter sido repassado: %v", *got)
	}
}

func TestAskStreamConversationCreationFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		fmt.Fprint(w, `{"detail":"index is loading"}`)
	}))
	defer srv.Close()
	if _, err := New(srv.URL).AskStream(context.Background(), "", "q", func(string) {}); err == nil || !strings.Contains(err.Error(), "index is loading") {
		t.Fatalf("erro = %v", err)
	}
}

// --- sem streaming ---

func TestAsk(t *testing.T) {
	f := &fakeRag{t: t}
	f.message = func(w http.ResponseWriter, _ *http.Request, conv string) {
		if conv == "old" {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"detail":"conversation not found"}`)
			return
		}
		fmt.Fprint(w, `{"ai_message":{"content":"Answer [1].","sources":[{"citation":1,"title":"Doc"}]}}`)
	}
	c := New(f.server().URL)
	ans, err := c.Ask(context.Background(), "", "q")
	if err != nil || ans.Content != "Answer [1]." || ans.Conversation != "new1" || len(ans.Sources) != 1 {
		t.Fatalf("conversa nova: %+v, %v", ans, err)
	}
	ans, err = c.Ask(context.Background(), "keep", "q")
	if err != nil || ans.Conversation != "keep" || f.created != 1 {
		t.Fatalf("conversa dada: %+v, %v (criadas: %d)", ans, err, f.created)
	}
	ans, err = c.Ask(context.Background(), "old", "q") // conversa de uma sessão anterior: começa outra
	if err != nil || ans.Conversation != "new2" {
		t.Fatalf("conversa antiga: %+v, %v", ans, err)
	}
}

func TestAskErrors(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		body    string
		wantErr string
	}{
		{"detalhe em texto", 422, `{"detail":"content is required"}`, "content is required"},
		{"detalhe que não é texto", 422, `{"detail":[{"loc":["body"]}]}`, "the RAG responded with HTTP 422"},
		{"corpo que não é JSON", 502, `<html>bad gateway</html>`, "the RAG responded with HTTP 502"},
		{"200 com corpo ilegível", 200, `not json`, "invalid character"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeRag{t: t}
			f.message = func(w http.ResponseWriter, _ *http.Request, _ string) {
				w.WriteHeader(c.code)
				fmt.Fprint(w, c.body)
			}
			ans, err := New(f.server().URL).Ask(context.Background(), "", "q")
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("erro = %v, want %q", err, c.wantErr)
			}
			if ans.Content != "" {
				t.Errorf("resposta com erro não traz texto: %+v", ans)
			}
		})
	}
}

// RAG lento: o prazo do contexto (o orçamento de domain/ask) corta a chamada antes do teto de 80 s do cliente.
func TestAskStopsAtContextDeadline(t *testing.T) {
	f := &fakeRag{t: t}
	release := make(chan struct{})
	f.message = func(_ http.ResponseWriter, r *http.Request, _ string) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}
	srv := f.server()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	if _, err := New(srv.URL).Ask(ctx, "c1", "q"); err == nil {
		t.Fatal("esperava o erro do prazo")
	}
	if d := time.Since(t0); d > 3*time.Second {
		t.Fatalf("levou %v para desistir", d)
	}
}

// --- estado ---

func TestStatus(t *testing.T) {
	var hits atomic.Int32
	f := &fakeRag{t: t}
	f.health = func(w http.ResponseWriter) {
		hits.Add(1)
		fmt.Fprint(w, `{"rag":{"ready":true,"chunks":4321,"detail":"ok"}}`)
	}
	c := New(f.server().URL)
	for i := 0; i < 5; i++ {
		st := c.Status(context.Background())
		if !st.Enabled || !st.Ready || st.Chunks != 4321 || st.Detail != "ok" {
			t.Fatalf("estado = %+v", st)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("consultas ao /health = %d: o estado pronto fica em cache", hits.Load())
	}
}

func TestStatusWhenDownOrDisabled(t *testing.T) {
	var off *Client // RAG_URL vazio
	if st := off.Status(context.Background()); st.Enabled || st.Ready {
		t.Errorf("RAG desligado: %+v", st)
	}
	if New("") != nil || New("/") != nil {
		t.Error("sem URL não há cliente")
	}
	f := &fakeRag{t: t}
	f.health = func(w http.ResponseWriter) { w.WriteHeader(500) }
	st := New(f.server().URL).Status(context.Background())
	if !st.Enabled || st.Ready || st.Detail != "RAG service is down" {
		t.Errorf("RAG fora do ar: %+v", st)
	}
}
