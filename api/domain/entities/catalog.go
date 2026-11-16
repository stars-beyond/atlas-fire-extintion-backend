package entities

import "encoding/json"

// Investigation é uma investigação do PSI com o resumo dos seus testes.
type Investigation struct {
	ID          string          `json:"id"`
	Acronym     string          `json:"acronym"`
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Kind        string          `json:"kind"`
	Area        string          `json:"area"`
	Hardware    string          `json:"hardware"`
	HwFamily    string          `json:"hardware_family"`
	Family      string          `json:"family"`
	Role        string          `json:"role"`
	Priority    float64         `json:"priority"`
	PriorityWhy string          `json:"priority_why"`
	StartYear   *int            `json:"start_year"`
	EndYear     *int            `json:"end_year"`
	TotalGB     float64         `json:"total_gb"`
	NPubs       int             `json:"n_publications"`
	NFiles      int             `json:"n_files"`
	Stats       json.RawMessage `json:"stats"`
	StatsData   InvStats        `json:"-"` // Stats já lido
	Vec         []float32       `json:"-"` // embedding da descrição
}

type InvStats struct {
	NTests     int       `json:"n_tests"`
	NPlot      int       `json:"n_plot"`
	O2         []float64 `json:"o2"`
	Pressure   []float64 `json:"pressure_atm"`
	HasO2      bool      `json:"has_o2"`
	HasP       bool      `json:"has_pressure"`
	HasOutcome bool      `json:"has_outcome"`
	Declared   *struct {
		O2 []float64 `json:"o2"`
		P  []float64 `json:"pressure_atm"`
	} `json:"declared"`
}

// Test é uma linha de tabela experimental, normalizada.
type Test struct {
	ID       string         `json:"id"`
	Inv      string         `json:"investigation_id"`
	Label    string         `json:"label"`
	Family   string         `json:"family"`
	Fuel     *string        `json:"fuel"`
	Material *string        `json:"material"`
	O2       *float64       `json:"o2"`
	P        *float64       `json:"pressure_atm"`
	G        float64        `json:"gravity"`
	Flow     *float64       `json:"flow_cm_s"`
	CO2      *float64       `json:"co2"`
	He       *float64       `json:"he"`
	Outcome  string         `json:"outcome"`
	Raw      *string        `json:"outcome_raw"`
	Assumed  []string       `json:"assumed"`
	File     string         `json:"source_file"`
	Row      int            `json:"source_row"`
	Metrics  map[string]any `json:"-"` // métricas próprias da investigação (tests.metrics)
}

// Plot diz se o teste tem O₂ e pressão, isto é, se entra no mapa e nos vereditos.
func (t *Test) Plot() bool { return t.O2 != nil && t.P != nil }

// Catalog é o acervo carregado da ingestão. É lido uma vez na subida e só consultado depois.
type Catalog struct {
	Invs     []*Investigation
	InvByID  map[string]*Investigation
	Tests    []*Test
	TestByID map[string]*Test
	Plot     int // testes com O₂ e pressão
	Models   map[string]ModelSpec
	Ingest   json.RawMessage
	Changes  json.RawMessage // o que entrou na última ingestão (meta changes); vazio na primeira carga
	Chunks   int
	// desfechos lidos dos relatórios, por investigação (ver domain/flammability)
	Outcomes map[string]*FlamDoc
	// ensaios de fora do PSI (torre de queda do ZARM), fora de Tests de propósito: ver ExternalDoc. Pode ser nil.
	External *ExternalDoc
}

// InvName devolve o nome curto da investigação, ou vazio se ela não existe.
func (c *Catalog) InvName(id string) string {
	if inv := c.InvByID[id]; inv != nil {
		return inv.Name
	}
	return ""
}

// ByInv lista os testes de uma investigação, na ordem do acervo.
func (c *Catalog) ByInv(id string) []*Test {
	var out []*Test
	for _, t := range c.Tests {
		if t.Inv == id {
			out = append(out, t)
		}
	}
	return out
}
