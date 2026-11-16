// Package flammability tem os handlers do mapa de inflamabilidade, da avaliação de cenário e do Próximo fogo.
package flammability

import (
	"net/http"

	"atlas-api/domain/entities"
	"atlas-api/extensions/zweb"
)

type GetUseCase interface {
	Get(investigation string) map[string]any
}

type AssessUseCase interface {
	Assess(s entities.Scenario, forced *float64, form string) (entities.Assessment, bool)
}

type QueueUseCase interface {
	Get() map[string]any
}

// FlammabilityHandler: GET /api/flammability?investigation=PSI-25
func FlammabilityHandler(uc GetUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zweb.JSON(w, 200, uc.Get(r.URL.Query().Get("investigation")))
	}
}

// AssessHandler: POST /api/flammability/assess — o cenário, com `forced_cm_s` (vento, padrão 10) e `form` opcionais.
func AssessHandler(uc AssessUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			entities.Scenario
			Forced *float64 `json:"forced_cm_s"`
			Form   string   `json:"form"`
		}
		if err := zweb.DecodeJSON(r, &in); err != nil {
			zweb.Error(w, 400, "invalid body")
			return
		}
		as, ok := uc.Assess(in.Scenario, in.Forced, in.Form)
		if !ok {
			zweb.JSON(w, 200, map[string]any{"available": false})
			return
		}
		zweb.JSON(w, 200, map[string]any{"available": true, "assessment": as})
	}
}

// QueueHandler: GET /api/flammability/queue — a fila do Próximo fogo.
func QueueHandler(uc QueueUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { zweb.JSON(w, 200, uc.Get()) }
}
