package sim

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type fakeConfig struct {
	cfg map[string]any
	err error
}

func (f fakeConfig) Get() (map[string]any, error) { return f.cfg, f.err }

type fakeValidation struct{}

func (fakeValidation) Get() map[string]any { return map[string]any{"available": true, "rows": []any{}} }

func get(h http.HandlerFunc) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/api/sim/x", nil))
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

// GET /api/sim/config
func TestConfigHandler(t *testing.T) {
	_, missing := os.Open("/caminho/que/nao/existe/lab3d.json") // o erro de arquivo ausente, como o os o devolve
	cases := []struct {
		name string
		err  error
		code int
		msg  string
	}{
		{"arquivo ausente: 404, o simulador usa o padrão embutido", fs.ErrNotExist, 404, "simulator configuration not found"},
		{"arquivo ausente, erro do sistema", missing, 404, "simulator configuration not found"},
		{"arquivo ausente, erro embrulhado", fmt.Errorf("loading config: %w", fs.ErrNotExist), 404, "simulator configuration not found"},
		{"arquivo ilegível", fmt.Errorf("open /srv/atlas/data/seed/sim/lab3d.json: permission denied"), 500, "the simulator configuration could not be read"},
		{"JSON quebrado", fmt.Errorf("/srv/atlas/data/seed/sim/lab3d.json: invalid character '}' at offset 812"), 500, "the simulator configuration could not be read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := get(ConfigHandler(fakeConfig{err: c.err}))
			body := object(t, w)
			if w.Code != c.code || len(body) != 1 || body["error"] != c.msg {
				t.Fatalf(`got %d %s, want %d {"error": %q}`, w.Code, w.Body.String(), c.code, c.msg)
			}
			// o erro traz o caminho do arquivo no servidor: ele não vai para o cliente
			for _, leak := range []string{"/srv/", "/caminho/", "lab3d.json", "permission denied", "offset"} {
				if strings.Contains(w.Body.String(), leak) {
					t.Errorf("a causa vazou para o cliente (%q): %s", leak, w.Body.String())
				}
			}
		})
	}
	t.Run("sucesso", func(t *testing.T) {
		w := get(ConfigHandler(fakeConfig{cfg: map[string]any{"schema": "lab3d.config/1", "materials": []any{}, "source": "Atlas API"}}))
		body := object(t, w)
		if w.Code != 200 || body["schema"] != "lab3d.config/1" || body["source"] != "Atlas API" {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
		// o arquivo é lido a cada pedido para valer na hora depois de editado: a resposta não pode ficar em cache
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("cache-control = %q, want no-store", cc)
		}
	})
}

// GET /api/sim/validation
func TestValidationHandler(t *testing.T) {
	w := get(ValidationHandler(fakeValidation{}))
	body := object(t, w)
	if w.Code != 200 || body["available"] != true || body["rows"] == nil {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}
