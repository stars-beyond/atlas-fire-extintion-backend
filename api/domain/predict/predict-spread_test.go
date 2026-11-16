package predict

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"atlas-api/domain/entities"
)

// tabela pequena e linear (v = O₂/10 + kPa/100 + fluxo/10), que a interpolação trilinear reproduz exatamente
func fakeTable() *entities.SpreadTable {
	t := &entities.SpreadTable{Schema: spreadSchema, Tolerance: 0.05}
	t.Model.Nets, t.Model.Trials = 8, 51
	t.Sample.Material, t.Sample.Thickness = "pmma", 0.075
	t.Axes.O2 = []float64{18, 24, 30, 39}
	t.Axes.KPa = []float64{50, 80, 106}
	t.Axes.Flow = []float64{6, 14, 22}
	t.TrainRange = map[string][2]float64{"o2_pct": {21, 36}, "pressure_kpa": {60, 103}, "flow_cm_s": {8.75, 20}, "ppo2_kpa": {21, 25.5}}
	for _, o := range t.Axes.O2 {
		for _, p := range t.Axes.KPa {
			for _, u := range t.Axes.Flow {
				v := o/10 + p/100 + u/10
				t.V, t.VLo, t.VHi, t.Stalls = append(t.V, v), append(t.VLo, v-0.5), append(t.VHi, v+0.5), append(t.Stalls, 0)
			}
		}
	}
	return t
}

func scen(o2, atm, g float64) entities.Scenario {
	return entities.Scenario{O2: o2, P: atm, G: g, Family: "solido", Material: "pmma"}
}

func TestSpreadInterpolates(t *testing.T) {
	tab := fakeTable()
	wind := 10.0
	out := Spread(tab, entities.SpreadIn{Scenario: scen(0.30, 70.3/kPaPerAtm, 0), Wind: &wind})
	if !out.Available || out.Level != "interpolado" || !out.InRange {
		t.Fatalf("base lunar em 0 g devia ser interpolado: %+v", out)
	}
	want := 30.0/10 + 70.3/100 + 10.0/10
	if math.Abs(out.V-want) > 2e-3 {
		t.Fatalf("v = %v, esperado %v", out.V, want)
	}
	base := 21.0/10 + stationKPa/100 + 1
	if math.Abs(out.Baseline-base) > 2e-3 || math.Abs(out.Ratio-want/base) > 2e-3 {
		t.Fatalf("referência %v razão %v", out.Baseline, out.Ratio)
	}
	if out.Nets[0] >= out.V || out.Nets[1] <= out.V || out.Per10cm != math.Round(100/out.V) {
		t.Fatalf("faixa ou tempo errados: %+v", out)
	}
	if out.GravityIgnored || len(out.Caveats) != 3 {
		t.Fatalf("em 0 g só os três avisos fixos: %v", out.Caveats)
	}
}

func TestSpreadLevels(t *testing.T) {
	tab := fakeTable()
	w := func(v float64) *float64 { return &v }

	// rover: pressão um pouco abaixo da faixa, ainda dentro da grade
	out := Spread(tab, entities.SpreadIn{Scenario: scen(0.34, 56.5/kPaPerAtm, 0.165), Wind: w(10)})
	if !out.Available || out.Level != "extrapolado" || len(out.OutOfRange) != 1 || !out.GravityIgnored {
		t.Fatalf("rover: %+v", out)
	}
	// fora da linha dos ensaios: O₂ e pressão na faixa, pressão parcial não
	out = Spread(tab, entities.SpreadIn{Scenario: scen(0.21, 62/kPaPerAtm, 0), Wind: w(10)})
	if !out.Available || out.Level != "extrapolado" || len(out.OutOfRange) != 1 {
		t.Fatalf("fora da linha: %+v", out)
	}
	// ventilação cortada: além da grade, sem número
	out = Spread(tab, entities.SpreadIn{Scenario: scen(0.21, 1, 0), Wind: w(0)})
	if out.Available || out.Level != "fora" || out.Reason == "" || out.V != 0 {
		t.Fatalf("sem vento: %+v", out)
	}
	// traje: O₂ puro
	if out = Spread(tab, entities.SpreadIn{Scenario: scen(1, 0.29, 0)}); out.Available {
		t.Fatalf("traje: %+v", out)
	}
	// outro material
	s := scen(0.21, 1, 0)
	s.Material = "sibal"
	if out = Spread(tab, entities.SpreadIn{Scenario: s}); out.Available || out.Level != "" {
		t.Fatalf("algodão: %+v", out)
	}
	// sem vento informado vale 10 cm/s
	if out = Spread(tab, entities.SpreadIn{Scenario: scen(0.21, 1, 0)}); !out.Available || out.Flow != 10 {
		t.Fatalf("vento padrão: %+v", out)
	}
}

func TestSpreadBadTable(t *testing.T) {
	tab := fakeTable()
	tab.V = tab.V[:3]
	if validSpread(tab) {
		t.Fatal("tabela com tamanho errado passou")
	}
}

// A tabela de verdade (data/seed/sim/spread.json), se existir: formato válido e os habitats do Atlas.
func TestSpreadSeed(t *testing.T) {
	raw, err := os.ReadFile("../../../data/seed/sim/spread.json")
	if err != nil {
		t.Skip("sem data/seed/sim/spread.json")
	}
	var tab entities.SpreadTable
	if err := json.Unmarshal(raw, &tab); err != nil || !validSpread(&tab) {
		t.Fatalf("tabela inválida: %v", err)
	}
	st := Spread(&tab, entities.SpreadIn{Scenario: scen(0.21, 1, 0)})
	lua := Spread(&tab, entities.SpreadIn{Scenario: scen(0.30, 10.2/14.696, 0.165)})
	if !st.Available || st.Level != "interpolado" || !lua.Available || lua.Level != "interpolado" {
		t.Fatalf("estação %+v\nlua %+v", st, lua)
	}
	if lua.V <= st.V {
		t.Fatalf("no ar da base lunar a chama devia andar mais rápido: %v contra %v", lua.V, st.V)
	}
	t.Logf("estação %.2f mm/s (%.2f–%.2f); base lunar %.2f mm/s (%.2f–%.2f), razão %.2f", st.V, st.Nets[0], st.Nets[1], lua.V, lua.Nets[0], lua.Nets[1], lua.Ratio)
}
