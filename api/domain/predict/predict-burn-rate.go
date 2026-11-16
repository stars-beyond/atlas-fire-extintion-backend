// Package predict aplica os modelos ajustados na ingestão (taxa de queima de gotas do FLEX),
// sempre dizendo se o ponto está dentro da faixa em que o modelo foi validado.
package predict

import (
	"fmt"
	"math"
	"strings"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// PredictBurnRateUseCase prevê a taxa de queima de uma gota com CO₂ ou hélio.
type PredictBurnRateUseCase struct {
	Catalog *entities.Catalog
}

func (uc *PredictBurnRateUseCase) Predict(in entities.PredictIn) (entities.PredictOut, error) {
	id := "flex_k_" + strings.ToLower(in.Fuel)
	m, ok := uc.Catalog.Models[id]
	if !ok {
		return entities.PredictOut{}, fmt.Errorf("model %q does not exist; use Methanol or Heptane", in.Fuel)
	}
	d0 := m.Defaults["d0_mm"]
	if in.D0 != nil && *in.D0 > 0 {
		d0 = *in.D0
	}
	// o2 é o oxigênio da mistura em que o modelo é lido. Trocar parte do ar pelo agente dilui o oxigênio junto.
	o2 := in.O2
	if in.Replace {
		o2 = in.O2 * (1 - in.CO2 - in.He)
	}
	x := map[string]float64{"o2": o2, "co2": in.CO2, "he": in.He, "pressure_atm": in.P, "d0_mm": d0}
	// Fora disso não há leitura física possível, e o modelo linear daria Inf/NaN (que não viram JSON).
	for k, v := range x {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e4 {
			return entities.PredictOut{}, fmt.Errorf("%s must be a finite number between -10000 and 10000", k)
		}
	}
	// frações de uma mistura: nenhuma é negativa e a soma não passa de 1
	if in.O2 < 0 || in.CO2 < 0 || in.He < 0 || in.CO2+in.He > 1+1e-9 || o2+in.CO2+in.He > 1+1e-9 {
		return entities.PredictOut{}, fmt.Errorf("o2, co2 and he are fractions of the mixture: none can be negative and together they cannot exceed 1")
	}
	out := entities.PredictOut{ModelID: id, Model: m, O2: ptbr.Round3(o2), InRange: true, OutOfRange: []string{}}
	names := map[string]string{"o2": "O₂", "co2": "CO₂", "he": "helium", "pressure_atm": "pressure", "d0_mm": "droplet diameter"}
	for _, f := range m.Features {
		r := m.Range[f]
		if x[f] < r[0]-1e-9 || x[f] > r[1]+1e-9 {
			out.InRange = false
			out.OutOfRange = append(out.OutOfRange, names[f])
		}
	}
	if in.CO2 > 0 && in.He > 0 { // cada variável está na faixa, mas nenhuma gota do FLEX queimou com os dois juntos
		out.InRange = false
		out.OutOfRange = append(out.OutOfRange, "CO₂ and helium together")
	}
	out.K = ptbr.Round3(m.Eval(x))
	base := map[string]float64{"o2": in.O2, "co2": 0, "he": 0, "pressure_atm": in.P, "d0_mm": d0}
	out.Baseline = ptbr.Round3(m.Eval(base))
	if out.K < 0 { // o modelo linear, longe da faixa, passa de zero: a taxa de queima não é negativa
		out.K = 0
	}
	if out.Baseline > 0 {
		out.DeltaPct = ptbr.Round3((out.K/out.Baseline - 1) * 100)
	}
	out.Level = "interpolado"
	out.Note = fmt.Sprintf("Linear model fitted to %d FLEX droplets (R² %s in cross-validation by test day).", m.N, ptbr.Num(m.R2, 2))
	if !out.InRange {
		out.Level = "extrapolado"
		out.Note += " Outside the tested range for: " + strings.Join(out.OutOfRange, ", ") + "."
	}
	if math.IsNaN(out.K) || math.IsInf(out.K, 0) || math.IsNaN(out.DeltaPct) || math.IsInf(out.DeltaPct, 0) {
		return entities.PredictOut{}, fmt.Errorf("the model has no reading for these values")
	}
	return out, nil
}
