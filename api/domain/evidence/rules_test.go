package evidence

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"atlas-api/domain/entities"
)

// A regra central: mesma gravidade = medido; os dois lados = delimitado; um lado = parcial; nada = sem evidência.
func TestClassify(t *testing.T) {
	cases := []struct {
		name            string
		same, zero, one int
		want            string
	}{
		{"teste na mesma gravidade", 1, 0, 0, "medido"},
		{"mesma gravidade vence os lados", 3, 5, 5, "medido"},
		{"0 g e 1 g", 0, 2, 1, "delimitado"},
		{"só 0 g", 0, 4, 0, "parcial"},
		{"só 1 g", 0, 0, 1, "parcial"},
		{"nada", 0, 0, 0, "sem_evidencia"},
	}
	for _, c := range cases {
		if got := Classify(0.165, c.same, c.zero, c.one); got != c.want {
			t.Errorf("%s: Classify = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestTierScore(t *testing.T) {
	want := map[string]float64{"medido": 2, "delimitado": 1, "parcial": 0.5, "sem_evidencia": 0, "": 0, "outro": 0}
	for tier, w := range want {
		if got := TierScore(tier); got != w {
			t.Errorf("TierScore(%q) = %v, want %v", tier, got, w)
		}
	}
}

func matTest(id, fam, mat string, o2, p, g float64, outcome string) *entities.Test {
	t := &entities.Test{ID: id, Inv: "X", Label: id, Family: fam, O2: &o2, P: &p, G: g, Outcome: outcome}
	if mat != "" {
		t.Material = &mat
	}
	return t
}

// mixedCatalog tem sólidos (pmma em 0 g e 1 g, algodão só em 0 g), gotas em outra atmosfera e um teste sem pressão.
func mixedCatalog() *entities.Catalog {
	cat := &entities.Catalog{InvByID: map[string]*entities.Investigation{
		"X": {ID: "X", Name: "Inv X", Family: "solido", Priority: 0.5, PriorityWhy: "Cabin solids"},
		"Y": {ID: "Y", Name: "Inv Y", Family: "gota", Priority: 0.9, PriorityWhy: "Droplets"},
	}}
	cat.Invs = []*entities.Investigation{cat.InvByID["X"], cat.InvByID["Y"]}
	cat.Tests = []*entities.Test{
		matTest("p0", "solido", "pmma", 0.21, 1, 0, "queimou"),
		matTest("p0b", "solido", "pmma", 0.22, 1.1, 0, "extinguiu"),
		matTest("p1", "solido", "pmma", 0.21, 1, 1, "queimou"),
		matTest("s0", "solido", "sibal", 0.21, 1, 0, "desconhecido"),
		matTest("far", "solido", "pmma", 0.40, 1, 0, "queimou"),
	}
	d := matTest("d0", "gota", "", 0.30, 0.7, 0, "disrupcao")
	d.Inv = "Y"
	noP := &entities.Test{ID: "nop", Inv: "X", Label: "nop", Family: "solido", O2: func() *float64 { v := 0.21; return &v }(), Outcome: "queimou"}
	cat.Tests = append(cat.Tests, d, noP)
	return cat
}

func TestVerdictTiers(t *testing.T) {
	cat := mixedCatalog()
	cases := []struct {
		name             string
		s                entities.Scenario
		tier             string
		same, zero, one  int
		inAtm            int
		headlineContains string
	}{
		{"0 g com teste de 0 g", entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "pmma"}, "medido", 2, 2, 1, 3, "Measured: 2 NASA tests with acrylic (PMMA)"},
		{"1 g com teste de 1 g", entities.Scenario{O2: 0.21, P: 1, G: 1, Family: "solido", Material: "pmma"}, "medido", 1, 2, 1, 3, "Measured: 1 NASA test "},
		{"gravidade lunar entre os dois lados", entities.Scenario{O2: 0.21, P: 1, G: 0.165, Family: "solido", Material: "pmma"}, "delimitado", 0, 2, 1, 3, "Bounded: no test at"},
		{"algodão só tem 0 g", entities.Scenario{O2: 0.21, P: 1, G: 0.165, Family: "solido", Material: "sibal"}, "parcial", 0, 1, 0, 1, "One side only: 1 test at 0 g"},
		{"oxigênio fora da tolerância", entities.Scenario{O2: 0.30, P: 1, G: 0, Family: "solido", Material: "pmma"}, "sem_evidencia", 0, 0, 0, 0, "Uncharted: no NASA test with acrylic (PMMA)"},
		{"pressão fora da tolerância", entities.Scenario{O2: 0.21, P: 0.5, G: 0, Family: "solido", Material: "pmma"}, "sem_evidencia", 0, 0, 0, 0, "Uncharted"},
		{"borda da tolerância de O₂ (2 pontos)", entities.Scenario{O2: 0.23, P: 1, G: 0, Family: "solido", Material: "pmma"}, "medido", 2, 2, 1, 3, "Measured"},
		{"sólidos sem material contam todos", entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido"}, "medido", 3, 3, 1, 4, "solid materials"},
		{"gotas na atmosfera delas", entities.Scenario{O2: 0.30, P: 0.7, G: 0, Family: "gota"}, "medido", 1, 1, 0, 1, "fuel droplets"},
		{"gotas: o material não filtra", entities.Scenario{O2: 0.30, P: 0.7, G: 0, Family: "gota", Material: "pmma"}, "medido", 1, 1, 0, 1, "fuel droplets"},
		{"todas as famílias", entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "todas"}, "medido", 3, 3, 1, 4, "any fuel"},
		{"família vazia vale sólidos", entities.Scenario{O2: 0.21, P: 1, G: 0}, "medido", 3, 3, 1, 4, "solid materials"},
		{"família que não existe", entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "plasma"}, "sem_evidencia", 0, 0, 0, 0, "Uncharted"},
		{"material desconhecido aparece como veio", entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "kevlar"}, "sem_evidencia", 0, 0, 0, 0, "kevlar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := Verdict(cat, c.s, nil, 8)
			if v.Tier != c.tier || v.SameG != c.same || v.ZeroG != c.zero || v.OneG != c.one || v.InAtm != c.inAtm {
				t.Fatalf("veredito %s (same %d, 0 g %d, 1 g %d, atm %d), want %s (%d, %d, %d, %d)",
					v.Tier, v.SameG, v.ZeroG, v.OneG, v.InAtm, c.tier, c.same, c.zero, c.one, c.inAtm)
			}
			if !strings.Contains(v.Headline, c.headlineContains) {
				t.Errorf("título %q, faltou %q", v.Headline, c.headlineContains)
			}
			if v.Label == "" || v.Explanation == "" || v.Outcomes == nil {
				t.Errorf("veredito incompleto: %+v", v)
			}
			if (v.Empirical != nil) != (c.tier == "medido") {
				t.Errorf("taxa empírica só existe no medido: %+v", v.Empirical)
			}
		})
	}
}

func TestVerdictNeighborsAndEmpirical(t *testing.T) {
	cat := mixedCatalog()
	v := Verdict(cat, entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "pmma"}, nil, 2)
	if len(v.Nearest) != 2 { // k corta a lista
		t.Fatalf("vizinhos = %d, want 2", len(v.Nearest))
	}
	if v.Nearest[0].Test.ID != "p0" || !v.Nearest[0].Inside || v.Nearest[0].InvName != "Inv X" || v.Nearest[0].Distance != 0 {
		t.Errorf("primeiro vizinho: %+v", v.Nearest[0])
	}
	if all := Verdict(cat, entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "pmma"}, nil, 0); len(all.Nearest) != 4 {
		t.Errorf("k = 0 não corta: vieram %d vizinhos, want 4 (o teste sem pressão não entra)", len(all.Nearest))
	}
	e := v.Empirical
	if e == nil || e.Total != 2 || e.Known != 2 || e.Burned != 1 || e.Extinguished != 1 || e.Rate == nil || *e.Rate != 0.5 {
		t.Fatalf("taxa empírica: %+v", e)
	}
	if !strings.Contains(e.Headline, "1 of 2 tests") || len(e.By) != 1 || e.By[0].Name != "Inv X" {
		t.Errorf("taxa empírica: %q, %+v", e.Headline, e.By)
	}
	// algodão em 0 g: há teste, mas sem desfecho na tabela, então não há taxa
	s := Verdict(cat, entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "sibal"}, nil, 2)
	if s.Empirical == nil || s.Empirical.Rate != nil || s.Empirical.Unknown != 1 || !strings.Contains(s.Empirical.Headline, "none has its outcome") {
		t.Errorf("sem desfecho: %+v", s.Empirical)
	}
	if v.Outcomes["queimou"] != 1 || v.Outcomes["extinguiu"] != 1 {
		t.Errorf("desfechos: %v", v.Outcomes)
	}
}

// Um teste hipotético (próximos testes) muda o veredito como um teste real mudaria, sem entrar no acervo.
func TestVerdictWithHypotheticalTests(t *testing.T) {
	cat := mixedCatalog()
	lua := entities.Scenario{O2: 0.30, P: 0.694, G: 0.165, Family: "solido", Material: "pmma"}
	if v := Verdict(cat, lua, nil, 1); v.Tier != "sem_evidencia" {
		t.Fatalf("antes: %s", v.Tier)
	}
	zero := matTest("hipotetico", "solido", "pmma", 0.30, 0.694, 0, "")
	if v := Verdict(cat, lua, []*entities.Test{zero}, 1); v.Tier != "parcial" {
		t.Fatalf("com um lado: %s", v.Tier)
	}
	one := matTest("hipotetico", "solido", "pmma", 0.30, 0.694, 1, "")
	if v := Verdict(cat, lua, []*entities.Test{zero, one}, 1); v.Tier != "delimitado" {
		t.Fatalf("com os dois lados: %s", v.Tier)
	}
	if len(cat.Tests) != 7 {
		t.Fatalf("o acervo foi alterado: %d testes", len(cat.Tests))
	}
}

// O cenário é limpo antes da conta: valores fora da faixa não quebram nada (nem dividem por zero).
func TestVerdictCleansScenario(t *testing.T) {
	v := Verdict(mixedCatalog(), entities.Scenario{O2: -5, P: 0, G: 9, Family: " solido\n"}, nil, 3)
	if v.Scenario.O2 != 0.01 || v.Scenario.P != 0.1 || v.Scenario.G != 1 || v.Scenario.Family != "solido" {
		t.Fatalf("cenário = %+v", v.Scenario)
	}
	if v.Tier != "sem_evidencia" {
		t.Fatalf("tier = %s", v.Tier)
	}
	if empty := Verdict(&entities.Catalog{}, entities.Scenario{O2: 0.21, P: 1}, nil, 3); empty.Tier != "sem_evidencia" ||
		!strings.Contains(empty.Explanation, "no test in this family") {
		t.Fatalf("acervo vazio: %+v", empty)
	}
}

func TestRank(t *testing.T) {
	cat := mixedCatalog()
	cat.InvByID["X"].Vec, cat.InvByID["Y"].Vec = []float32{1, 0}, []float32{0, 1}
	// uma investigação sem teste no mapa, só com a faixa de O₂ declarada, e outra sem nada
	z := &entities.Investigation{ID: "Z", Name: "Inv Z", Family: "solido", PriorityWhy: "Declared"}
	z.StatsData.O2 = []float64{0.18, 0.25}
	w := &entities.Investigation{ID: "W", Name: "Inv W", Family: "gas", PriorityWhy: "Nothing"}
	cat.Invs = append(cat.Invs, z, w)
	cat.InvByID["Z"], cat.InvByID["W"] = z, w

	s := entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido"}
	r := Rank(cat, s, []float32{1, 0})
	if len(r) != 4 {
		t.Fatalf("ranking com %d investigações, want 4", len(r))
	}
	for i := 1; i < len(r); i++ {
		if r[i].Score > r[i-1].Score {
			t.Fatalf("fora de ordem na posição %d: %v > %v", i, r[i].Score, r[i-1].Score)
		}
	}
	by := map[string]entities.Ranked{}
	for _, x := range r {
		by[x.ID] = x
	}
	if r[0].ID != "X" {
		t.Errorf("X tem teste nesta atmosfera, é da família e é o mais parecido com a pergunta: devia vir primeiro, veio %s", r[0].ID)
	}
	if x := by["X"]; x.Parts.Proximity != 1 || x.Parts.Relevance != 1 || !strings.Contains(x.Reason, "Has a test in this atmosphere") {
		t.Errorf("X: %+v", x)
	}
	if y := by["Y"]; y.Parts.Relevance != 0 || !strings.Contains(y.Reason, "Nearest test differs by") || y.Parts.Proximity >= 1 {
		t.Errorf("Y: %+v", y)
	}
	if zz := by["Z"]; zz.Parts.Proximity != 0.6 || !strings.Contains(zz.Reason, "Covers 18% to 25% O₂") {
		t.Errorf("Z (cenário dentro da faixa declarada): %+v", zz)
	}
	if ww := by["W"]; ww.Parts.Proximity != 0 || !strings.Contains(ww.Reason, "No O₂ tabulated per test") {
		t.Errorf("W: %+v", ww)
	}
	for _, x := range r {
		if !strings.HasSuffix(x.Reason, ".") || x.Reason[:1] != strings.ToUpper(x.Reason[:1]) {
			t.Errorf("motivo mal formado: %q", x.Reason)
		}
	}

	// sem vetor (embedder fora do ar) o ranking sai sem a parte semântica, sem dividir por zero
	for _, x := range Rank(cat, s, nil) {
		if x.Parts.Relevance != 0 {
			t.Errorf("sem vetor a relevância é zero: %+v", x)
		}
	}
	// investigação de outra família perde 20% da nota; em "todas" ninguém perde
	gota := map[string]float64{}
	for _, x := range Rank(cat, entities.Scenario{O2: 0.30, P: 0.7, Family: "gota"}, nil) {
		gota[x.ID] = x.Score
	}
	todas := map[string]float64{}
	for _, x := range Rank(cat, entities.Scenario{O2: 0.30, P: 0.7, Family: "todas"}, nil) {
		todas[x.ID] = x.Score
	}
	if gota["Y"] != todas["Y"] || !(gota["X"] < todas["X"]) {
		t.Errorf("penalidade de família: gota %v, todas %v", gota, todas)
	}
}

func TestCosine(t *testing.T) {
	cases := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{1, 0}, []float32{1, 0}, 1},
		{[]float32{1, 0}, []float32{0, 1}, 0},
		{[]float32{1, 0}, []float32{-1, 0}, -1},
		{nil, []float32{1, 0}, 0},
		{[]float32{1}, []float32{1, 0}, 0},    // tamanhos diferentes
		{[]float32{0, 0}, []float32{1, 0}, 0}, // vetor nulo
	}
	for _, c := range cases {
		if got := cosine(c.a, c.b); got != c.want {
			t.Errorf("cosine(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestScenarioText(t *testing.T) {
	cases := []struct {
		s    entities.Scenario
		want []string
	}{
		{entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido"}, []string{"solid cabin materials", "21% oxygen at 14.7 psi", "in microgravity"}},
		{entities.Scenario{O2: 0.30, P: 0.694, G: 0.165, Family: "gota"}, []string{"liquid fuel droplets", "30% oxygen at 10.2 psi", "partial gravity"}},
		{entities.Scenario{O2: 0.21, P: 1, G: 1, Family: "gas"}, []string{"gaseous fuel flames", "normal gravity"}},
	}
	for _, c := range cases {
		got := ScenarioText(c.s)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("ScenarioText(%+v) = %q, faltou %q", c.s, got, w)
			}
		}
	}
}

// --- próximos testes ---

// Com o acervo de teste (sólidos só a 21% / 1 atm), a estação já está medida e os outros três alvos não têm nada.
func TestNextTests(t *testing.T) {
	cat := smallCatalog()
	next := NextTests(cat, "solido", "", 5)
	if len(next) != 4 {
		t.Fatalf("vieram %d sugestões, want 4: %+v", len(next), next)
	}
	// 1º: 34% a 8,2 psi em 0 g dá um lado ao rover e a Marte de uma vez; 2º: o mesmo em 1 g fecha os dois lados
	want := []struct {
		o2, p, g, gain float64
		closes         int
	}{{0.34, 0.558, 0, 1, 2}, {0.34, 0.558, 1, 1, 2}, {0.30, 0.694, 0, 0.5, 1}, {0.30, 0.694, 1, 0.5, 1}}
	for i, w := range want {
		n := next[i]
		if n.O2 != w.o2 || n.P != w.p || n.G != w.g || n.Gain != w.gain || len(n.Closes) != w.closes {
			t.Errorf("sugestão %d: %+v, want %+v", i+1, n, w)
		}
		if n.Why == "" || n.Platform == "" {
			t.Errorf("sugestão %d sem texto: %+v", i+1, n)
		}
	}
	if next[0].Platform != "ISS or orbital flight (0 g)" || next[1].Platform != "Ground laboratory (1 g)" {
		t.Errorf("plataformas: %q, %q", next[0].Platform, next[1].Platform)
	}
	if !strings.Contains(next[0].Closes[0], "Uncharted → One side only") || !strings.Contains(next[1].Closes[0], "One side only → Bounded") {
		t.Errorf("o que fecha: %v / %v", next[0].Closes, next[1].Closes)
	}
	if got := NextTests(cat, "solido", "", 2); len(got) != 2 || !reflect.DeepEqual(got, next[:2]) {
		t.Errorf("n = 2: %+v", got)
	}
	// família vazia e "todas" valem sólidos
	for _, fam := range []string{"", "todas"} {
		if got := NextTests(cat, fam, "", 5); !reflect.DeepEqual(got, next) {
			t.Errorf("família %q devia dar o mesmo que solido", fam)
		}
	}
	if len(cat.Tests) != 12 {
		t.Fatalf("os testes hipotéticos entraram no acervo: %d", len(cat.Tests))
	}
}

func TestNextTestsUseCaseCachesKnownKeys(t *testing.T) {
	cat := mixedCatalog()
	uc := &NextTestsUseCase{Catalog: cat}
	want := NextTests(cat, "solido", "pmma", 5)
	if len(want) == 0 {
		t.Fatal("o acervo de teste devia ter sugestões para pmma")
	}
	a := uc.Next("solido", "pmma")
	b := uc.Next("solido", "pmma")
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("o resultado guardado difere do calculado:\n%+v\n%+v", a, want)
	}
	if &a[0] != &b[0] {
		t.Error("a segunda chamada recalculou em vez de usar o resultado guardado")
	}
	if n := len(uc.cache); n != 1 {
		t.Fatalf("entradas guardadas = %d, want 1", n)
	}
	// "" e "todas" valem sólidos: dividem uma entrada só
	x, y, z := uc.Next("", ""), uc.Next("todas", ""), uc.Next("solido", "")
	if &x[0] != &y[0] || &y[0] != &z[0] {
		t.Error(`"", "todas" e "solido" deviam dividir a mesma entrada`)
	}
	if n := len(uc.cache); n != 2 {
		t.Fatalf("entradas guardadas = %d, want 2", n)
	}
	if !reflect.DeepEqual(x, NextTests(cat, "", "", 5)) {
		t.Error("o resultado guardado para a família vazia difere do calculado")
	}
}

// Família e material chegam livres no pedido: valores que não existem no acervo são respondidos, mas não guardados.
func TestNextTestsUseCaseDoesNotCacheUnknownKeys(t *testing.T) {
	cat := mixedCatalog()
	uc := &NextTestsUseCase{Catalog: cat}
	for i := 0; i < 300; i++ {
		junk := strings.Repeat("x", i%7+1) + string(rune('a'+i%26)) + strings.Repeat("y", i)
		got := uc.Next("solido", junk)
		if i < 3 && !reflect.DeepEqual(got, NextTests(cat, "solido", junk, 5)) {
			t.Fatalf("material inventado: resultado diferente do calculado")
		}
		uc.Next(junk, "")
		uc.Next(junk, junk)
	}
	if n := len(uc.cache); n != 0 {
		t.Fatalf("%d entradas guardadas para chaves que não existem no acervo", n)
	}
	// o material tem de bater exatamente com o do acervo: variações não ocupam memória
	for _, m := range []string{"PMMA", " pmma", "pmma ", "pmma\n"} {
		uc.Next("solido", m)
	}
	if n := len(uc.cache); n != 0 {
		t.Fatalf("%d entradas guardadas para variações de material", n)
	}
	uc.Next("gota", "pmma") // família e material do acervo: guarda
	if n := len(uc.cache); n != 1 {
		t.Fatalf("entradas guardadas = %d, want 1", n)
	}
}

// Mesmo com chaves do acervo, o mapa não passa do teto.
func TestNextTestsUseCaseCacheIsCapped(t *testing.T) {
	cat := mixedCatalog()
	uc := &NextTestsUseCase{Catalog: cat}
	uc.Next("solido", "pmma")
	for i := 0; len(uc.cache) < nextTestsCacheMax; i++ { // enche o resto com entradas quaisquer
		uc.cache[nextKey{"cheio", strings.Repeat("m", i+1)}] = &nextEntry{}
	}
	got := uc.Next("solido", "sibal") // chave válida, mas sem espaço
	if n := len(uc.cache); n != nextTestsCacheMax {
		t.Fatalf("o mapa passou do teto: %d entradas", n)
	}
	if !reflect.DeepEqual(got, NextTests(cat, "solido", "sibal", 5)) {
		t.Error("sem espaço no mapa a resposta ainda tem de ser a calculada")
	}
	a, b := uc.Next("solido", "pmma"), uc.Next("solido", "pmma") // o que já estava guardado continua valendo
	if &a[0] != &b[0] {
		t.Error("a entrada antiga deixou de ser usada")
	}
}

func TestNextTestsUseCaseConcurrent(t *testing.T) {
	cat := mixedCatalog()
	uc := &NextTestsUseCase{Catalog: cat}
	want := map[string][]entities.NextTest{}
	mats := []string{"pmma", "sibal", "", "inventado"}
	for _, m := range mats {
		want[m] = NextTests(cat, "solido", m, 5)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := mats[i%len(mats)]
			if got := uc.Next("solido", m); !reflect.DeepEqual(got, want[m]) {
				t.Errorf("material %q: resultado diferente sob concorrência", m)
			}
		}(i)
	}
	wg.Wait()
	if n := len(uc.cache); n != 3 {
		t.Fatalf("entradas guardadas = %d, want 3 (o material inventado não entra)", n)
	}
}

// --- achados ---

type fakeMetrics struct {
	calls atomic.Int32
	fail  atomic.Bool
	data  map[string]map[string]float64 // investigação -> teste -> valor
}

func (f *fakeMetrics) MetricFloat(_ context.Context, inv, _ string) (map[string]float64, error) {
	f.calls.Add(1)
	if f.fail.Load() {
		return nil, errors.New("dial tcp 10.0.0.5:5432: connection refused")
	}
	return f.data[inv], nil
}

func flexCatalog() *entities.Catalog {
	cat := &entities.Catalog{InvByID: map[string]*entities.Investigation{"PSI-69": {ID: "PSI-69", Name: "FLEX"}}}
	fuel := "Heptane"
	add := func(id string, co2, he *float64, outcome string) {
		o2, p := 0.21, 1.0
		cat.Tests = append(cat.Tests, &entities.Test{ID: id, Inv: "PSI-69", Label: id, Family: "gota", Fuel: &fuel, O2: &o2, P: &p,
			CO2: co2, He: he, Outcome: outcome})
	}
	v := 0.2
	add("n1", nil, nil, "extinguiu")
	add("n2", nil, nil, "queimou")
	add("c1", &v, nil, "extinguiu")
	add("h1", nil, &v, "disrupcao")
	return cat
}

func flexMetrics() *fakeMetrics {
	return &fakeMetrics{data: map[string]map[string]float64{"PSI-69": {"n1": 1.0, "n2": 2.0, "c1": 0.5, "h1": 3.0}}}
}

func insightByID(list []entities.Insight, id string) *entities.Insight {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func TestInsightsUsesMetricsFromDatabase(t *testing.T) {
	uc := &InsightsUseCase{Catalog: flexCatalog(), Metrics: flexMetrics()}
	list, err := uc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flex := insightByID(list, "flex_desfecho")
	if flex == nil {
		t.Fatalf("faltou o achado do FLEX: %+v", list)
	}
	if want := "Across 4 FLEX tests, the mean burning rate drops from 1.50 mm²/s (N₂ only) to 0.50 with CO₂ and rises to 3.00 with helium"; !strings.Contains(flex.Finding, want) {
		t.Errorf("achado = %q", flex.Finding)
	}
	if insightByID(list, "cobertura") == nil {
		t.Error("faltou o achado de cobertura")
	}
}

// Banco fora do ar: erro, e não um achado dizendo "from 0.00 mm²/s". O erro nunca fica guardado.
func TestInsightsDatabaseErrorIsNotPublishedNorCached(t *testing.T) {
	m := flexMetrics()
	m.fail.Store(true)
	uc := &InsightsUseCase{Catalog: flexCatalog(), Metrics: m}
	for i := 0; i < 2; i++ {
		list, err := uc.List(context.Background())
		if err == nil || list != nil {
			t.Fatalf("tentativa %d: esperava erro e nenhuma lista, veio %+v, %v", i, list, err)
		}
	}
	if m.calls.Load() != 2 { // cada tentativa para na primeira leitura que falha, e tenta de novo na vez seguinte
		t.Fatalf("leituras = %d, want 2", m.calls.Load())
	}
	m.fail.Store(false) // o banco voltou
	list, err := uc.List(context.Background())
	if err != nil || insightByID(list, "flex_desfecho") == nil {
		t.Fatalf("depois de o banco voltar: %+v, %v", list, err)
	}
}

func TestInsightsAreComputedOnce(t *testing.T) {
	m := flexMetrics()
	uc := &InsightsUseCase{Catalog: flexCatalog(), Metrics: m}
	first, err := uc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := m.calls.Load()
	if n != 3 {
		t.Fatalf("o primeiro cálculo fez %d leituras, want 3", n)
	}
	m.fail.Store(true) // depois do primeiro sucesso o banco nem é consultado
	for i := 0; i < 5; i++ {
		again, err := uc.List(context.Background())
		if err != nil || len(again) != len(first) || &again[0] != &first[0] {
			t.Fatalf("chamada %d: devia vir a lista guardada (%v)", i, err)
		}
	}
	if m.calls.Load() != n {
		t.Fatalf("o banco foi consultado de novo: %d leituras", m.calls.Load())
	}
}

func TestInsightsConcurrent(t *testing.T) {
	uc := &InsightsUseCase{Catalog: flexCatalog(), Metrics: flexMetrics()}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if list, err := uc.List(context.Background()); err != nil || insightByID(list, "flex_desfecho") == nil {
				t.Errorf("got %+v, %v", list, err)
			}
		}()
	}
	wg.Wait()
}

func TestGraph(t *testing.T) {
	nodes, edges := Graph(mixedCatalog(), entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "pmma"}, 2)
	if nodes[0].ID != "cenario" || nodes[0].Type != "cenario" {
		t.Fatalf("o primeiro nó é o cenário: %+v", nodes[0])
	}
	types := map[string]int{}
	for _, n := range nodes {
		types[n.Type]++
	}
	if types["teste"] != 2 || types["investigacao"] != 1 || types["material"] != 1 || types["gravidade"] != 1 || types["desfecho"] != 2 {
		t.Errorf("nós por tipo: %v", types)
	}
	ids := map[string]bool{}
	for _, n := range nodes {
		if ids[n.ID] {
			t.Errorf("nó repetido: %s", n.ID)
		}
		ids[n.ID] = true
	}
	for _, e := range edges {
		if !ids[e.Source] || !ids[e.Target] {
			t.Errorf("aresta para nó que não existe: %+v", e)
		}
	}
}
