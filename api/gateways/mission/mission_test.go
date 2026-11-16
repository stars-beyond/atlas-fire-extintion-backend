package mission

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	domainmission "atlas-api/domain/mission"
)

// O cliente repassa a configuração intacta, devolve a resposta do motor e separa recusa (4xx) de queda.
func TestClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mission/presets":
			io.WriteString(w, `{"scenarios":[{"id":"demo-lunar"}]}`)
		case "/mission/simulate":
			body, _ := io.ReadAll(r.Body)
			var cfg map[string]any
			_ = json.Unmarshal(body, &cfg)
			switch cfg["name"] {
			case "impossible":
				w.WriteHeader(422)
				io.WriteString(w, `{"detail":"duration too long"}`)
			case "schema":
				w.WriteHeader(422)
				io.WriteString(w, `{"detail":[{"loc":["cfg","crew"],"msg":"bad"}]}`)
			case "crash":
				w.WriteHeader(500)
				io.WriteString(w, `{"detail":"boom"}`)
			default:
				w.Write(body)
			}
		}
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL}
	ctx := context.Background()

	if out, err := c.Presets(ctx); err != nil || string(out) != `{"scenarios":[{"id":"demo-lunar"}]}` {
		t.Fatalf("presets: %s %v", out, err)
	}
	if out, err := c.Simulate(ctx, json.RawMessage(`{"name":"ok","crew":4}`)); err != nil || string(out) != `{"name":"ok","crew":4}` {
		t.Fatalf("simulate echo: %s %v", out, err)
	}

	var inv *domainmission.InvalidError
	if _, err := c.Simulate(ctx, json.RawMessage(`{"name":"impossible"}`)); !errors.As(err, &inv) || inv.Message != "duration too long" {
		t.Fatalf("422 string: %v", err)
	}
	if _, err := c.Simulate(ctx, json.RawMessage(`{"name":"schema"}`)); !errors.As(err, &inv) || len(inv.Detail) == 0 {
		t.Fatalf("422 schema: %v", err)
	}
	if _, err := c.Simulate(ctx, json.RawMessage(`{"name":"crash"}`)); !errors.Is(err, domainmission.ErrUnavailable) {
		t.Fatalf("500: %v", err)
	}
	if _, err := (&Client{URL: "http://127.0.0.1:1"}).Presets(ctx); !errors.Is(err, domainmission.ErrUnavailable) {
		t.Fatalf("down: %v", err)
	}
}
