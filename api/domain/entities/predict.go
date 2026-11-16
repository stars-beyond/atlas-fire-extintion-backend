package entities

// ModelSpec é um modelo ajustado na ingestão, com a faixa em que foi validado.
type ModelSpec struct {
	Kind       string                `json:"kind"`
	Target     string                `json:"target"`
	Label      string                `json:"label"`
	Fuel       string                `json:"fuel"`
	Source     string                `json:"source"`
	N          int                   `json:"n"`
	R2         float64               `json:"r2_cv"`
	RMSE       float64               `json:"rmse_cv"`
	Intercept  float64               `json:"intercept"`
	Features   []string              `json:"features"`
	Coef       []float64             `json:"coef"`
	Range      map[string][2]float64 `json:"range"`
	Defaults   map[string]float64    `json:"defaults"`
	Validation string                `json:"validation"`
}

// Eval aplica o modelo linear às variáveis x.
func (m ModelSpec) Eval(x map[string]float64) float64 {
	y := m.Intercept
	for i, f := range m.Features {
		if i >= len(m.Coef) { // modelo mal formado no acervo: sem coeficiente, a variável não entra
			break
		}
		y += m.Coef[i] * x[f]
	}
	return y
}

type PredictIn struct {
	Fuel string   `json:"fuel"` // Methanol | Heptane
	O2   float64  `json:"o2"`
	CO2  float64  `json:"co2"`
	He   float64  `json:"he"`
	P    float64  `json:"pressure_atm"`
	D0   *float64 `json:"d0_mm"`
	// Replace diz que co2 e he são a parte do ar trocada pelo gás (soltar o agente na cabine): o oxigênio
	// cai junto, para o2·(1−co2−he). Sem ele, o2, co2 e he são as frações da mistura final, como nas tabelas do FLEX.
	Replace bool `json:"replace_air"`
}

type PredictOut struct {
	ModelID    string    `json:"model_id"`
	O2         float64   `json:"o2_effective"` // O₂ da mistura em que o modelo foi lido
	K          float64   `json:"k_mm2_s"`
	Baseline   float64   `json:"baseline_k_mm2_s"` // o ar de partida, sem CO₂ nem hélio
	DeltaPct   float64   `json:"delta_pct"`
	InRange    bool      `json:"in_range"`
	OutOfRange []string  `json:"out_of_range"`
	Level      string    `json:"level"` // interpolado | extrapolado
	Model      ModelSpec `json:"model"`
	Note       string    `json:"note"`
}
