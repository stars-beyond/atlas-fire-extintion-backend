package entities

import "encoding/json"

// ExternalDoc é um conjunto de ensaios de fora do PSI (formato "atlas.external/1", gerado por
// scripts/make_external.py). Hoje há um só: a torre de queda do ZARM, com filme fino de acrílico em 0 g
// entre 21% e 36% de O₂, a faixa em que o PSI não tem nenhum sólido.
//
// Estes ensaios ficam fora de Catalog.Tests de propósito: o veredito, o ranking, os achados e as contagens
// continuam a falar só do que a NASA testou. Eles aparecem ao lado, sempre rotulados.
type ExternalDoc struct {
	Schema  string           `json:"schema"`
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Agency  string           `json:"agency"`
	What    string           `json:"what"`
	NotNasa string           `json:"not_nasa"`
	License string           `json:"license"`
	Records []ExternalRecord `json:"records"`
	Summary json.RawMessage  `json:"summary"`
	Tests   []ExternalTest   `json:"tests"`
}

type ExternalRecord struct {
	ID      string `json:"id"`
	Short   string `json:"short"`
	Title   string `json:"title"`
	Authors string `json:"authors"`
	URL     string `json:"url"`
}

// ExternalTest é um ensaio de fora da NASA, com os mesmos campos de atmosfera de um Test.
type ExternalTest struct {
	ID         string          `json:"id"`
	Record     string          `json:"record"`
	Run        string          `json:"run"`
	Label      string          `json:"label"`
	O2         float64         `json:"o2"`
	P          float64         `json:"pressure_atm"`
	KPa        float64         `json:"pressure_kpa"`
	G          float64         `json:"gravity"`
	Flow       float64         `json:"flow_cm_s"`
	Material   string          `json:"material"`
	Fuel       string          `json:"fuel"`
	Sample     string          `json:"sample"`
	Outcome    string          `json:"outcome"`
	Raw        string          `json:"outcome_raw"`
	Spread     *float64        `json:"spread_mm_s"`
	Barriers   json.RawMessage `json:"barriers"`
	AlsoIn     *string         `json:"also_in"`
	File       string          `json:"source_file"`
	Row        int             `json:"source_row"`
	Provenance string          `json:"provenance"`
}

// AsTest devolve o ensaio na forma de um Test, para as mesmas contas de vizinhança do cenário.
func (e *ExternalTest) AsTest(inv string) *Test {
	t := &Test{ID: e.ID, Inv: inv, Label: e.Label, Family: "solido", G: e.G, Outcome: e.Outcome, Assumed: []string{}, File: e.File, Row: e.Row}
	o2, p, flow, fuel, mat, raw := e.O2, e.P, e.Flow, e.Fuel, e.Material, e.Raw
	t.O2, t.P, t.Flow, t.Fuel, t.Material, t.Raw = &o2, &p, &flow, &fuel, &mat, &raw
	return t
}

// Record acha o registro de origem de um ensaio.
func (d *ExternalDoc) Record(id string) *ExternalRecord {
	for i := range d.Records {
		if d.Records[i].ID == id {
			return &d.Records[i]
		}
	}
	return nil
}

// ExternalNear é um ensaio de fora da NASA perto do cenário.
type ExternalNear struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	O2      float64  `json:"o2"`
	P       float64  `json:"pressure_atm"`
	Flow    float64  `json:"flow_cm_s"`
	Outcome string   `json:"outcome"`
	Raw     string   `json:"outcome_raw"`
	Spread  *float64 `json:"spread_mm_s"`
}

// ExternalEvidence é o que os ensaios de fora da NASA dizem sobre o cenário. Só existe quando há algum
// na mesma atmosfera (as tolerâncias do veredito). Não muda o nível de evidência: TierWith diz que nível
// seria se eles contassem.
type ExternalEvidence struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Agency    string           `json:"agency"`
	N         int              `json:"n"`
	Burned    int              `json:"burned"`
	Stopped   int              `json:"stopped"` // apagaram numa barreira corta-fogo
	Unknown   int              `json:"unknown"`
	Spread    *[2]float64      `json:"spread_mm_s"` // da menor à maior velocidade medida
	NSpread   int              `json:"n_spread"`
	TierWith  string           `json:"tier_with"`
	TierLabel string           `json:"tier_with_label"`
	Headline  string           `json:"headline"`
	Detail    string           `json:"detail"`
	Caveat    string           `json:"caveat"`
	Nearest   []ExternalNear   `json:"nearest"`
	License   string           `json:"license"`
	Records   []ExternalRecord `json:"records"`
}
