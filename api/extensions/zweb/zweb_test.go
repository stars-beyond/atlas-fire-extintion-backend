package zweb

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Um valor que não vira JSON (NaN) não pode sair como 200 de corpo vazio.
func TestJSONUnencodableValueIs500WithBody(t *testing.T) {
	w := httptest.NewRecorder()
	JSON(w, 200, map[string]float64{"k": math.Inf(1)})
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var e struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error == "" {
		t.Fatalf("body = %q, want a JSON error", w.Body.String())
	}
}

func TestJSONNormal(t *testing.T) {
	w := httptest.NewRecorder()
	JSON(w, 201, map[string]int{"n": 1})
	if w.Code != 201 || strings.TrimSpace(w.Body.String()) != `{"n":1}` {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
}

// recorder que acusa qualquer escrita depois de "fechado" (o handler já retornou).
type strictRecorder struct {
	*httptest.ResponseRecorder
	mu       sync.Mutex
	finished bool
	late     int
}

func (r *strictRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		r.late++
	}
	return r.ResponseRecorder.Write(b)
}

func (r *strictRecorder) Flush() {}

func (r *strictRecorder) finish() {
	r.mu.Lock()
	r.finished = true
	r.mu.Unlock()
}

// Depois de stop() nenhum ping pode ser escrito, nem os que já estavam a caminho.
func TestKeepAliveStopsBeforeHandlerReturns(t *testing.T) {
	for i := 0; i < 50; i++ {
		rec := &strictRecorder{ResponseRecorder: httptest.NewRecorder()}
		s, ok := NewSSE(rec)
		if !ok {
			t.Fatal("NewSSE refused a flusher")
		}
		stop := s.KeepAlive(context.Background(), time.Microsecond)
		time.Sleep(200 * time.Microsecond)
		stop()
		rec.finish() // o handler retornou: daqui em diante nada pode ser escrito
		time.Sleep(time.Millisecond)
		s.Send("x", 1) // um envio tardio também é ignorado
		stop()         // chamar de novo não trava nem entra em pânico
		if rec.late != 0 {
			t.Fatalf("round %d: %d writes after stop", i, rec.late)
		}
	}
}

// Um pânico num handler vira 500 no formato de erro da API, em vez de uma conexão cortada.
func TestRecoverTurnsPanicInto500JSON(t *testing.T) {
	h := Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/x", nil))
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("corpo sem o campo error: %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("a causa do pânico vazou para o cliente: %q", w.Body.String())
	}
}
