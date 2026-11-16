// Package predict tem os handlers das previsões: taxa de queima (modelos do FLEX) e velocidade de
// propagação (modelo neural da equipe, treinado nos ensaios do ZARM).
package predict

import (
	"context"
	"net/http"

	"atlas-api/domain/entities"
	domainpredict "atlas-api/domain/predict"
	"atlas-api/extensions/zweb"
)

type BurnRateUseCase interface {
	Predict(in entities.PredictIn) (entities.PredictOut, error)
}

// BurnRateHandler: POST /api/predict/burn-rate — corpo: {fuel, o2, co2, he, pressure_atm, d0_mm}.
func BurnRateHandler(uc BurnRateUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in entities.PredictIn
		if err := zweb.DecodeJSON(r, &in); err != nil {
			zweb.Error(w, 400, "invalid input")
			return
		}
		out, err := uc.Predict(in)
		if err != nil {
			zweb.Error(w, 400, err.Error())
			return
		}
		zweb.JSON(w, 200, out)
	}
}

type SpreadUseCase interface {
	Predict(in entities.SpreadIn) entities.SpreadOut
}

// SpreadHandler: POST /api/predict/spread — o cenário, com `wind_cm_s` opcional (padrão 10).
// Responde sempre 200; `available` diz se há número e `reason` por que não.
func SpreadHandler(uc SpreadUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in entities.SpreadIn
		if err := zweb.DecodeJSON(r, &in); err != nil {
			zweb.Error(w, 400, "invalid input")
			return
		}
		zweb.JSON(w, 200, uc.Predict(in))
	}
}

type ThermalUseCase interface {
	Predict(ctx context.Context, in domainpredict.ThermalIn) map[string]any
}

// ThermalHandler: POST /api/predict/thermal — o cenário, `wind_cm_s` e `compare` (o ensaio de exemplo
// ao lado da gravação medida). Responde sempre 200; `available` diz se há filme.
func ThermalHandler(uc ThermalUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in domainpredict.ThermalIn
		if err := zweb.DecodeJSON(r, &in); err != nil {
			zweb.Error(w, 400, "invalid input")
			return
		}
		zweb.JSON(w, 200, uc.Predict(r.Context(), in))
	}
}
