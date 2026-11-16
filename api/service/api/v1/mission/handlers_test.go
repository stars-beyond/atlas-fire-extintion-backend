package mission

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	domainmission "atlas-api/domain/mission"
)

type fakeEngine struct{ err error }

func (f fakeEngine) Presets(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"scenarios":[]}`), f.err
}

func (f fakeEngine) Simulate(_ context.Context, cfg json.RawMessage) (json.RawMessage, error) {
	return cfg, f.err
}

func TestHandlers(t *testing.T) {
	cases := []struct {
		name   string
		engine domainmission.Engine
		body   string
		code   int
		want   string
	}{
		{"ok", fakeEngine{}, `{"crew":4}`, 200, `"crew":4`},
		{"not json", fakeEngine{}, `{`, 400, `invalid input`},
		{"not object", fakeEngine{}, `[1]`, 422, `JSON object`},
		{"refused", fakeEngine{err: &domainmission.InvalidError{Message: "too long"}}, `{}`, 422, `too long`},
		{"down", fakeEngine{err: domainmission.ErrUnavailable}, `{}`, 503, `unavailable`},
		{"not configured", nil, `{}`, 503, `unavailable`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			SimulateHandler(&domainmission.SimulateUseCase{Engine: c.engine})(w, httptest.NewRequest("POST", "/api/missions/simulations", strings.NewReader(c.body)))
			if w.Code != c.code || !strings.Contains(w.Body.String(), c.want) {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
		})
	}

	w := httptest.NewRecorder()
	PresetsHandler(&domainmission.GetPresetsUseCase{Engine: fakeEngine{}})(w, httptest.NewRequest("GET", "/api/missions/presets", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"scenarios"`) {
		t.Fatalf("presets: %d %s", w.Code, w.Body.String())
	}
}
