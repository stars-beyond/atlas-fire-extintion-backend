package predict

import (
	"math"
	"testing"

	"atlas-api/domain/entities"
)

func burnCatalog() *entities.Catalog {
	m := entities.ModelSpec{Intercept: 0.16, Features: []string{"o2", "co2", "he", "pressure_atm", "d0_mm"},
		Coef:     []float64{2.34, -0.31, 0.61, 0.14, -0.08},
		Range:    map[string][2]float64{"o2": {0.13, 0.34}, "co2": {0, 0.7}, "he": {0, 0.45}, "pressure_atm": {0.69, 2.04}, "d0_mm": {1.1, 4.67}},
		Defaults: map[string]float64{"d0_mm": 2.92}}
	return &entities.Catalog{Models: map[string]entities.ModelSpec{"flex_k_heptane": m}}
}

// Trocar parte do ar pelo agente dilui o oxigênio: o modelo é lido em o2·(1−agente), contra o ar de partida.
func TestBurnRateReplaceAirDilutesOxygen(t *testing.T) {
	uc := &PredictBurnRateUseCase{Catalog: burnCatalog()}
	fixed, err := uc.Predict(entities.PredictIn{Fuel: "Heptane", O2: 0.21, CO2: 0.3, P: 1})
	if err != nil {
		t.Fatal(err)
	}
	repl, err := uc.Predict(entities.PredictIn{Fuel: "Heptane", O2: 0.21, CO2: 0.3, P: 1, Replace: true})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(repl.O2-0.147) > 1e-9 || fixed.O2 != 0.21 {
		t.Fatalf("O₂ efetivo: %v (troca) e %v (fixo)", repl.O2, fixed.O2)
	}
	if repl.Baseline != fixed.Baseline || !(repl.DeltaPct < fixed.DeltaPct) {
		t.Fatalf("com o oxigênio diluído a queda tem de ser maior, contra a mesma base: %+v / %+v", repl, fixed)
	}
}

// Frações que somam mais de 1 não são uma mistura; CO₂ e hélio juntos nunca foram testados.
func TestBurnRateRejectsImpossibleMixtures(t *testing.T) {
	uc := &PredictBurnRateUseCase{Catalog: burnCatalog()}
	if _, err := uc.Predict(entities.PredictIn{Fuel: "Heptane", O2: 0.34, CO2: 0.7, He: 0.45, P: 1}); err == nil {
		t.Fatal("34% + 70% + 45% devia ser recusado")
	}
	// traje: O₂ puro com 60% do ar trocado vira 40% de O₂ + 60% de CO₂, uma mistura possível (fora da faixa)
	out, err := uc.Predict(entities.PredictIn{Fuel: "Heptane", O2: 1, CO2: 0.6, P: 0.2925, Replace: true})
	if err != nil || out.InRange {
		t.Fatalf("traje: %+v, %v", out, err)
	}
	both, err := uc.Predict(entities.PredictIn{Fuel: "Heptane", O2: 0.21, CO2: 0.2, He: 0.2, P: 1})
	if err != nil || both.InRange || both.Level != "extrapolado" {
		t.Fatalf("CO₂ e hélio juntos: %+v, %v", both, err)
	}
}
