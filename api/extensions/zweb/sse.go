package zweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSE escreve Server-Sent Events. É seguro chamar Send de várias goroutines.
// Depois de Close nada mais é escrito: o http.ResponseWriter não pode ser usado depois que o handler retorna
// (o net/http recicla o buffer, e uma escrita tardia derruba o processo ou cai na resposta de outro cliente).
type SSE struct {
	mu     sync.Mutex
	w      http.ResponseWriter
	f      http.Flusher
	closed bool
}

// Close impede qualquer escrita a partir daqui. Chame antes de o handler retornar.
func (s *SSE) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// NewSSE prepara a resposta como text/event-stream. Devolve false se o servidor não permite streaming.
func NewSSE(w http.ResponseWriter) (*SSE, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no") // o nginx repassa cada evento na hora
	w.WriteHeader(200)
	return &SSE{w: w, f: f}, true
}

// Send manda um evento com os dados em JSON.
func (s *SSE) Send(event string, data any) {
	b, _ := json.Marshal(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b)
	s.f.Flush()
}

func (s *SSE) ping() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	fmt.Fprint(s.w, ": ping\n\n")
	s.f.Flush()
}

// KeepAlive manda um comentário ": ping" a cada intervalo até ctx acabar ou stop ser chamado,
// para o nginx e o Cloudflare não fecharem a conexão enquanto a resposta é gerada.
// stop fecha o SSE e só volta depois que a goroutine terminou: nenhum ping sai depois do handler.
func (s *SSE) KeepAlive(ctx context.Context, every time.Duration) (stop func()) {
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				s.ping()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.Close()
			close(done)
			<-exited
		})
	}
}
