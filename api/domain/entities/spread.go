package entities

// SpreadTable é a tabela de velocidade de propagação gerada pelo modelo neural da equipe
// (stars-beyond/Atlas-MachineL) com ml/spread_table.py: formato "atlas.spread/1". As redes rodam
// fora do servidor; a API só interpola nesta grade de O₂ × pressão × fluxo.
type SpreadTable struct {
	Schema string `json:"schema"`
	Date   string `json:"date"`
	Model  struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Commit string `json:"commit"`
		Nets   int    `json:"nets"`
		Trials int    `json:"trials"`
	} `json:"model"`
	Sample struct {
		Material  string  `json:"material"`
		Form      string  `json:"form"`
		Thickness float64 `json:"thickness_mm"`
		Length    float64 `json:"length_mm"`
		History   string  `json:"history"`
	} `json:"sample"`
	// TrainRange: o2_pct, pressure_kpa, flow_cm_s e ppo2_kpa (O₂ e pressão variam juntos nos ensaios).
	TrainRange map[string][2]float64 `json:"train_range"`
	Tolerance  float64               `json:"tolerance"`
	Axes       struct {
		O2   []float64 `json:"o2_pct"`
		KPa  []float64 `json:"pressure_kpa"`
		Flow []float64 `json:"flow_cm_s"`
	} `json:"axes"`
	V      []float64 `json:"v_mm_s"`    // média do conjunto; o último índice (fluxo) corre mais rápido
	VLo    []float64 `json:"v_lo_mm_s"` // a rede mais lenta
	VHi    []float64 `json:"v_hi_mm_s"` // a rede mais rápida
	Stalls []int     `json:"stalls"`    // 1 = a frente prevista para antes do fim da amostra
	Check  struct {
		What string           `json:"what"`
		Rows []SpreadCheckRow `json:"rows"`
		MAE  float64          `json:"mae_mm_s"`
		Max  float64          `json:"max_mm_s"`
	} `json:"check"`
	Reported struct {
		Source      string  `json:"source"`
		Barriers    float64 `json:"heldout_mae_barriers_mm_s"`
		Gaps        float64 `json:"heldout_mae_gaps_mm_s"`
		External    float64 `json:"external_mae_mm_s"`
		ExternalR2  float64 `json:"external_r2"`
		ExternalN   int     `json:"external_runs"`
		ExternalSrc string  `json:"external_source"`
	} `json:"reported"`
	License     string   `json:"license"`
	Attribution []string `json:"attribution"`
}

// SpreadCheckRow é um ensaio contínuo medido no ZARM e o que o modelo dá nas mesmas condições.
type SpreadCheckRow struct {
	Trial     string     `json:"trial"`
	O2        float64    `json:"o2_pct"`
	KPa       float64    `json:"pressure_kpa"`
	Flow      float64    `json:"flow_cm_s"`
	Measured  float64    `json:"measured_mm_s"`
	Predicted float64    `json:"predicted_mm_s"`
	Nets      [2]float64 `json:"nets_mm_s"`
}

// SpreadIn é o cenário mais o vento da ventilação (cm/s; nil = 10).
type SpreadIn struct {
	Scenario
	Wind *float64 `json:"wind_cm_s"`
}

// SpreadOut é a velocidade de propagação prevista para o ar do cenário.
type SpreadOut struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"` // por que não há número

	V        float64    `json:"v_mm_s"`
	Nets     [2]float64 `json:"nets_mm_s"`     // da rede mais lenta à mais rápida
	Baseline float64    `json:"baseline_mm_s"` // ar da estação (21% O₂, 1 atm) no mesmo vento
	Ratio    float64    `json:"ratio"`         // V / Baseline
	Per10cm  float64    `json:"s_per_10cm"`    // segundos para a frente andar 10 cm

	Level          string   `json:"level"` // interpolado | extrapolado | fora
	InRange        bool     `json:"in_range"`
	OutOfRange     []string `json:"out_of_range"`
	GravityIgnored bool     `json:"gravity_ignored"` // o cenário tem gravidade; o modelo é só de 0 g
	Stalls         bool     `json:"stalls"`

	O2   float64 `json:"o2_pct"`
	KPa  float64 `json:"pressure_kpa"`
	Flow float64 `json:"flow_cm_s"`
	PPO2 float64 `json:"ppo2_kpa"`

	Headline string   `json:"headline"`
	Compare  string   `json:"compare"`
	Detail   string   `json:"detail"`
	Caveats  []string `json:"caveats"`
	Note     string   `json:"note"`

	Model   *SpreadModel     `json:"model,omitempty"`
	Check   []SpreadCheckRow `json:"check,omitempty"`
	Sources []string         `json:"sources,omitempty"`
}

// SpreadModel resume de onde vem o número.
type SpreadModel struct {
	Name       string  `json:"name"`
	URL        string  `json:"url"`
	Commit     string  `json:"commit"`
	Date       string  `json:"date"`
	Nets       int     `json:"nets"`
	Trials     int     `json:"trials"`
	Sample     string  `json:"sample"`
	HeldoutMAE float64 `json:"heldout_mae_mm_s"` // README do modelo: ensaios que as redes não viram
	CheckMAE   float64 `json:"check_mae_mm_s"`   // conferência nos ensaios contínuos medidos
	License    string  `json:"license"`
}
