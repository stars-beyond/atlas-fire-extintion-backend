package entities

// Closed é uma ressalva de um habitat-alvo que um experimento proposto tira.
type Closed struct {
	Habitat string `json:"habitat"`
	Name    string `json:"name"`
	What    string `json:"what"` // gravidade | oxigenio | pressao
	Label   string `json:"label"`
}

// LunarEffect é o que o experimento muda na leitura da base lunar.
type LunarEffect struct {
	Text          string  `json:"text"`
	CaveatsBefore int     `json:"caveats_before"`
	CaveatsAfter  int     `json:"caveats_after"`
	WidthBefore   float64 `json:"width_before_o2"`
	WidthAfter    float64 `json:"width_after_o2"`
}

// Proposal é um experimento da fila "Próximo fogo".
type Proposal struct {
	Rank       int         `json:"rank"`
	ID         string      `json:"id"`
	Kind       string      `json:"kind"` // gravidade | oxigenio | pressao | habitat | curva | modelo
	Title      string      `json:"title"`
	Material   string      `json:"material"`
	O2         string      `json:"o2"`
	Pressure   string      `json:"pressure"`
	Gravity    string      `json:"gravity"`
	Flow       string      `json:"flow"`
	Measure    string      `json:"measure"`
	Prediction string      `json:"prediction"`
	Decides    string      `json:"decides"`
	Closes     []Closed    `json:"closes"`
	Remains    []string    `json:"remains"`
	GainO2     float64     `json:"gain_o2"`
	Factors    []string    `json:"new_factors"`
	Score      float64     `json:"score"`
	Platform   string      `json:"platform"`
	CheckFirst string      `json:"check_first"`
	Lunar      LunarEffect `json:"lunar"`
	Sources    []string    `json:"sources"`
}

// HabitatGap é um habitat-alvo com a leitura da curva e o que falta testar nele.
type HabitatGap struct {
	Habitat Habitat    `json:"habitat"`
	As      Assessment `json:"assessment"`
	Gaps    []string   `json:"gaps"` // gravidade | oxigenio | pressao
	Width   float64    `json:"width_o2"`
}
