package flammability

import (
	"math"
	"strings"
	"testing"

	"atlas-api/domain/entities"
)

// boundary monta uma fronteira no formato do relatório: limite = A·e^(−k·a) + B·a + C, com a = 1,5·U/r0.
// Com A = 0 e B = 0 o limite é C em qualquer vento, o que deixa as contas dos testes à vista.
func boundary(form string, b, c float64) entities.Boundary {
	var bd entities.Boundary
	bd.Form = form
	bd.Band = 1
	bd.Published.Source = "BASS-II report, Eq. 6.1"
	bd.Published.Coef.B, bd.Published.Coef.C, bd.Published.Coef.R0 = b, c, 1.5
	bd.Tested.O2 = []float64{14, 22}
	bd.Tested.FlowMax, bd.Tested.P = 40, 1
	bd.Buoyancy.Flow1g = []float64{20, 30}
	return bd
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAssessStatus(t *testing.T) {
	b := boundary("bastão de acrílico", 0, 17) // precisa de 17% de O₂, mais ou menos 1 ponto
	cases := []struct {
		name     string
		o2       float64
		status   string
		headline string
	}{
		{"bem acima do limite", 0.21, "queima", "The flame sustains itself"},
		{"logo acima da faixa", 0.181, "queima", "The flame sustains itself"},
		{"dentro da faixa, acima", 0.175, "no_limite", "Right at the limit"},
		{"em cima do limite", 0.17, "no_limite", "Right at the limit"},
		{"dentro da faixa, abaixo", 0.165, "no_limite", "Right at the limit"},
		{"logo abaixo da faixa", 0.159, "nao_sustenta", "The flame does not sustain itself"},
		{"bem abaixo", 0.14, "nao_sustenta", "The flame does not sustain itself"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			as := Assess(&b, entities.Scenario{O2: c.o2, P: 1, G: 0, Family: "solido"}, 10)
			if as.Status != c.status || as.Headline != c.headline {
				t.Fatalf("status %s (%q), want %s", as.Status, as.Headline, c.status)
			}
			if !approx(as.O2, c.o2*100) || !approx(as.LimitO2[0], 17) || !approx(as.LimitO2[1], 17) ||
				!approx(as.Margin[0], c.o2*100-17) || !approx(as.Margin[1], c.o2*100-17) {
				t.Errorf("O₂ %v, limite %v, margem %v", as.O2, as.LimitO2, as.Margin)
			}
			if !as.Inside || len(as.Outside) != 0 || as.Outside == nil {
				t.Errorf("dentro da faixa testada não há ressalva: %v", as.Outside)
			}
			if as.Form != "bastão de acrílico" || as.Band != 1 || as.Forced != 10 || as.Source == "" ||
				!strings.Contains(as.Detail, "needs 17.0% oxygen") || strings.Contains(as.Detail, "outside the tested range") {
				t.Errorf("leitura: %+v", as)
			}
		})
	}
}

// Fora do que a NASA testou a leitura sai, mas com cada ressalva dita.
func TestAssessOutsideTestedRange(t *testing.T) {
	b := boundary("bastão de acrílico", 0, 17)
	cases := []struct {
		name    string
		s       entities.Scenario
		forced  float64
		outside []string
		buoyant [2]float64
	}{
		{"oxigênio acima do testado", entities.Scenario{O2: 0.30, P: 1, G: 0}, 10, []string{"above 22.0% oxygen"}, [2]float64{0, 0}},
		{"outra pressão", entities.Scenario{O2: 0.21, P: 0.694, G: 0}, 10, []string{"this environment is at 10.2 psi"}, [2]float64{0, 0}},
		{"gravidade parcial", entities.Scenario{O2: 0.21, P: 1, G: 0.165}, 5, []string{"from 0 to 30 cm/s", "partial gravity is not known"}, [2]float64{0, 30}},
		{"1 g", entities.Scenario{O2: 0.21, P: 1, G: 1}, 5, []string{"from 20 to 30 cm/s"}, [2]float64{20, 30}},
		{"vento acima do testado", entities.Scenario{O2: 0.21, P: 1, G: 0}, 60, []string{"exceeds the highest tested (40 cm/s)"}, [2]float64{0, 0}},
		{"base lunar: tudo de uma vez", entities.Scenario{O2: 0.30, P: 0.694, G: 0.165}, 20,
			[]string{"above 22.0% oxygen", "10.2 psi", "partial gravity is not known", "exceeds the highest tested"}, [2]float64{0, 30}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			as := Assess(&b, c.s, c.forced)
			if as.Inside {
				t.Fatal("devia estar fora da faixa testada")
			}
			all := strings.Join(as.Outside, " | ")
			for _, w := range c.outside {
				if !strings.Contains(all, w) {
					t.Errorf("faltou a ressalva %q em %q", w, all)
				}
			}
			if as.Buoyant != c.buoyant || !approx(as.Flow[0], c.forced+c.buoyant[0]) || !approx(as.Flow[1], c.forced+c.buoyant[1]) {
				t.Errorf("vento natural %v, vento total %v", as.Buoyant, as.Flow)
			}
			if !strings.Contains(as.Detail, "outside the tested range, not a measurement") {
				t.Errorf("a leitura fora da faixa tem de dizer que não é medição: %q", as.Detail)
			}
		})
	}
	// em 1 g a gravidade é a única ressalva quando o resto está na faixa; o texto de gravidade parcial não aparece
	as := Assess(&b, entities.Scenario{O2: 0.21, P: 1, G: 1}, 5)
	if len(as.Outside) != 1 || strings.Contains(as.Outside[0], "partial gravity") {
		t.Errorf("1 g: %v", as.Outside)
	}
}

// Quando a curva varia com o vento, a faixa de vento vira uma faixa de limite, e a margem acompanha.
func TestAssessLimitRangeFollowsFlow(t *testing.T) {
	b := boundary("bastão de acrílico", 0.1, 15) // limite = 15 + 0,1·a, com a = U (r = r0 = 1,5 cm)
	as := Assess(&b, entities.Scenario{O2: 0.21, P: 1, G: 1}, 10)
	if !approx(as.Flow[0], 30) || !approx(as.Flow[1], 40) || !approx(as.LimitO2[0], 18) || !approx(as.LimitO2[1], 19) {
		t.Fatalf("vento %v, limite %v", as.Flow, as.LimitO2)
	}
	if !approx(as.Margin[0], 2) || !approx(as.Margin[1], 3) || as.Status != "queima" {
		t.Errorf("margem %v, status %s", as.Margin, as.Status)
	}
	if !strings.Contains(as.Detail, "between 18.0% and 19.0% oxygen") {
		t.Errorf("detalhe = %q", as.Detail)
	}
	// diluição: de (limite alto + faixa) a (limite baixo − faixa), como fração do ar
	if !approx(as.Dilution[0], 1-20.0/21) || !approx(as.Dilution[1], 1-17.0/21) {
		t.Errorf("diluição = %v", as.Dilution)
	}
	// abaixo do limite não há o que diluir: nunca negativo
	low := Assess(&b, entities.Scenario{O2: 0.12, P: 1, G: 0}, 10)
	if low.Dilution[0] != 0 || low.Dilution[1] != 0 || low.Status != "nao_sustenta" {
		t.Errorf("abaixo do limite: diluição %v, status %s", low.Dilution, low.Status)
	}
}

func TestAssessUseCase(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	doc := &entities.FlamDoc{Boundaries: []entities.Boundary{boundary("bastão de acrílico", 0, 17), boundary("placa", 0, 19)}}
	uc := &AssessUseCase{Catalog: &entities.Catalog{Outcomes: map[string]*entities.FlamDoc{BassII: doc}}}
	station := entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido"}
	cases := []struct {
		name   string
		forced *float64
		form   string
		want   float64 // vento forçado usado
		limit  float64
	}{
		{"sem vento informado vale 10 cm/s", nil, "", 10, 17},
		{"vento informado", f(25), "", 25, 17},
		{"vento negativo vira zero", f(-3), "", 0, 17},
		{"vento absurdo é limitado a 200", f(1e9), "", 200, 17},
		{"outra forma de amostra", nil, "placa", 10, 19},
		{"forma desconhecida vale a primeira", nil, "esfera", 10, 17},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			as, ok := uc.Assess(station, c.forced, c.form)
			if !ok || as.Forced != c.want || !approx(as.LimitO2[0], c.limit) {
				t.Fatalf("ok %v, vento %v, limite %v", ok, as.Forced, as.LimitO2)
			}
		})
	}
	// o cenário é limpo antes da leitura: O₂ fora da faixa não vira NaN
	if as, ok := uc.Assess(entities.Scenario{O2: -1, P: 0, G: 5}, nil, ""); !ok || math.IsNaN(as.Margin[0]) || as.O2 != 1 {
		t.Errorf("cenário absurdo: %+v", as)
	}
	for name, cat := range map[string]*entities.Catalog{
		"sem desfechos":   {},
		"sem o BASS-II":   {Outcomes: map[string]*entities.FlamDoc{"PSI-99": doc}},
		"sem curva":       {Outcomes: map[string]*entities.FlamDoc{BassII: {}}},
		"desfechos vazio": {Outcomes: map[string]*entities.FlamDoc{}},
	} {
		if _, ok := (&AssessUseCase{Catalog: cat}).Assess(station, nil, ""); ok {
			t.Errorf("%s: não há curva, a avaliação não pode existir", name)
		}
	}
}

func TestGetFlammability(t *testing.T) {
	doc := &entities.FlamDoc{Method: "regras", Boundaries: []entities.Boundary{boundary("bastão", 0, 17)},
		Tests: []entities.FlamTest{
			{Test: "a", Form: "bastão", Points: []byte(`[{"o2":17}]`)},
			{Test: "b", Form: "bastão", Points: []byte(`[{"o2":18}]`)},
			{Test: "c", Form: "placa", Points: []byte(`[{"o2":19}]`)},
			{Test: "d", Form: "placa", Points: []byte(`[]`)}, // sem pontos no mapa: não conta
			{Test: "e", Form: "filme"},
		}}
	cat := &entities.Catalog{Outcomes: map[string]*entities.FlamDoc{BassII: doc},
		InvByID: map[string]*entities.Investigation{BassII: {ID: BassII, Name: "BASS-II"}}}
	uc := &GetFlammabilityUseCase{Catalog: cat}
	out := uc.Get("") // por omissão, o BASS-II
	if out["available"] != true || out["investigation"] != BassII || out["name"] != "BASS-II" || out["method"] != "regras" {
		t.Fatalf("out = %v", out)
	}
	forms := out["forms"].([]entities.FormCount)
	if len(forms) != 2 || forms[0] != (entities.FormCount{Form: "bastão", N: 2}) || forms[1] != (entities.FormCount{Form: "placa", N: 1}) {
		t.Errorf("formas = %+v", forms)
	}
	bounds := out["boundaries"].([]entities.Boundary)
	if len(bounds) != 1 || len(bounds[0].Curve) != 160 || !approx(bounds[0].Curve[0].O2, 17) {
		t.Errorf("a curva vai amostrada para a tela: %d pontos", len(bounds[0].Curve))
	}
	if len(doc.Boundaries[0].Curve) != 0 {
		t.Error("a curva amostrada não pode ficar guardada no acervo")
	}
	if miss := uc.Get("PSI-404"); miss["available"] != false || miss["investigation"] != "PSI-404" {
		t.Errorf("investigação sem desfechos: %v", miss)
	}
}
