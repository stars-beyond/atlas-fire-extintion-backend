package predict

import (
	"context"

	"atlas-api/domain/entities"
)

// Câmera térmica: o filme de temperatura da superfície que as redes do Atlas-MachineL preveem para o
// ar do cenário. Aqui as redes rodam de verdade, no serviço Python (brain), a cada pedido.
//
// A regra de quando mostrar é a mesma da velocidade de propagação (Spread): fora da grade do modelo
// não há filme, porque longe dos ensaios a rede não apaga a chama; dentro da grade mas fora da faixa
// dos ensaios o filme sai com o aviso de extrapolação. No modo de comparação o filme é o do ensaio de
// exemplo do modelo, ao lado da gravação medida.

// ThermalModel roda as redes.
type ThermalModel interface {
	Rollout(ctx context.Context, o2Pct, kPa, flow float64, sample bool) (map[string]any, error)
}

// ThermalIn é o cenário, o vento e se é para comparar com a gravação medida.
type ThermalIn struct {
	entities.Scenario
	Wind    *float64 `json:"wind_cm_s"`
	Compare bool     `json:"compare"`
}

// PredictThermalUseCase devolve o filme previsto, com os mesmos avisos da velocidade de propagação.
type PredictThermalUseCase struct {
	Files SpreadFiles
	Model ThermalModel
}

func (uc *PredictThermalUseCase) Predict(ctx context.Context, in ThermalIn) map[string]any {
	none := func(reason, level string, outside []string) map[string]any {
		if outside == nil {
			outside = []string{}
		}
		return map[string]any{"available": false, "reason": reason, "level": level, "out_of_range": outside, "compare": in.Compare}
	}
	if uc.Model == nil {
		return none("The thermal model service is not configured.", "", nil)
	}
	if in.Compare {
		out, err := uc.Model.Rollout(ctx, 0, 0, 0, true)
		if err != nil || out == nil {
			return none("The thermal model service did not answer.", "", nil)
		}
		out["available"], out["compare"], out["level"], out["in_range"], out["out_of_range"] = true, true, "medido", true, []string{}
		out["caveats"] = []string{
			"Part of the networks saw this run in training, so this is an illustration, not a test of the model. Its measured error is on runs it never saw: about 20 °C per pixel after 1.2 s.",
			"The model almost never predicts that a flame goes out at a metal barrier: it got 1 of 4 right.",
		}
		return out
	}
	t, err := LoadTable(uc.Files)
	if err != nil {
		return none(err.Error(), "", nil)
	}
	sp := Spread(t, entities.SpreadIn{Scenario: in.Scenario, Wind: in.Wind})
	if !sp.Available {
		return none(sp.Reason, sp.Level, sp.OutOfRange)
	}
	out, err := uc.Model.Rollout(ctx, sp.O2, sp.KPa, sp.Flow, false)
	if err != nil || out == nil {
		return none("The thermal model service did not answer.", "", nil)
	}
	out["available"], out["compare"] = true, false
	out["level"], out["in_range"], out["out_of_range"] = sp.Level, sp.InRange, sp.OutOfRange
	out["gravity_ignored"], out["caveats"], out["note"] = sp.GravityIgnored, sp.Caveats, sp.Note
	out["table_v_mm_s"], out["baseline_mm_s"], out["ratio"] = sp.V, sp.Baseline, sp.Ratio
	return out
}
