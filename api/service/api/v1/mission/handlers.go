// Package mission tem os handlers do motor de missão (modo história do site): as missões de referência e a
// simulação de uma missão.
package mission

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	domainmission "atlas-api/domain/mission"
	"atlas-api/extensions/zweb"
)

type PresetsUseCase interface {
	Get(ctx context.Context) (json.RawMessage, error)
}

type SimulateUseCase interface {
	Simulate(ctx context.Context, cfg json.RawMessage) (json.RawMessage, error)
}

// PresetsHandler: GET /api/missions/presets — perfis, atmosferas, falhas, materiais e as missões de referência
// (`scenarios`, cada uma com a `config` pronta para simular). 503 se o motor não responder.
func PresetsHandler(uc PresetsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := uc.Get(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		zweb.JSON(w, 200, out)
	}
}

// SimulateHandler: POST /api/missions/simulations — corpo: a `config` de uma missão (MissionConfig do motor).
// Responde a série hora a hora, os eventos e o resumo. 422 com o motivo do motor se a missão for impossível.
func SimulateHandler(uc SimulateUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg json.RawMessage
		if err := zweb.DecodeJSON(r, &cfg); err != nil {
			zweb.Error(w, 400, "invalid input")
			return
		}
		out, err := uc.Simulate(r.Context(), cfg)
		if err != nil {
			fail(w, err)
			return
		}
		zweb.JSON(w, 200, out)
	}
}

func fail(w http.ResponseWriter, err error) {
	var inv *domainmission.InvalidError
	if errors.As(err, &inv) {
		body := map[string]any{"error": inv.Message}
		if len(inv.Detail) > 0 {
			body["detail"] = inv.Detail
		}
		zweb.JSON(w, 422, body)
		return
	}
	zweb.Error(w, 503, "The mission engine is unavailable.")
}
