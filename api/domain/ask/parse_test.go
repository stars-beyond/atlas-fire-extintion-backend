package ask

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"atlas-api/domain/entities"
)

var station = entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido"}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// As regras (sem modelo de linguagem) leem o que está escrito na pergunta e mantêm o resto do cenário atual.
func TestParseScenario(t *testing.T) {
	cases := []struct {
		name string
		q    string
		want entities.Scenario
		got  []string // prefixos do que foi entendido, na ordem
	}{
		{"nada sobre o cenário", "tell me about soot formation", station, nil},
		{"base lunar e oxigênio", "What about a lunar base with 30% oxygen?",
			entities.Scenario{O2: 0.30, P: 0.694, G: 0.165, Family: "solido", Habitat: "lua"}, []string{"habitat: Lunar base", "O₂: 30%"}},
		{"marte em português, com algodão", "Um incêndio em Marte com tecido de algodão",
			entities.Scenario{O2: 0.34, P: 0.558, G: 0.378, Family: "solido", Material: "sibal", Habitat: "marte"}, []string{"habitat: Mars base", "material: sibal"}},
		{"estação pela sigla", "is a fire on the ISS dangerous?",
			entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Habitat: "iss"}, []string{"habitat: Space station"}},
		{"iss dentro de outra palavra não conta", "the mission plan", station, nil},
		{"sala na Terra", "a battery room on earth",
			entities.Scenario{O2: 0.21, P: 1, G: 1, Family: "solido", Habitat: "terra"}, []string{"habitat: Closed room on Earth"}},
		{"traje", "inside a spacesuit",
			entities.Scenario{O2: 1, P: 0.2925, G: 0, Family: "solido", Habitat: "traje"}, []string{"habitat: Spacesuit"}},
		{"O₂, psi, gravidade e material", "acrylic at 34% O2 and 8.2 psi in 0.38 g",
			entities.Scenario{O2: 0.34, P: 8.2 / 14.696, G: 0.38, Family: "solido", Material: "pmma"},
			[]string{"O₂: 34%", "pressure: 8.2 psi", "gravity: Mars gravity", "material: pmma"}},
		{"oxigênio antes do número", "oxygen at 40 percent", entities.Scenario{O2: 0.40, P: 1, G: 0, Family: "solido"}, []string{"O₂: 40%"}},
		{"vírgula decimal", "30,5% de oxigênio", entities.Scenario{O2: 0.305, P: 1, G: 0, Family: "solido"}, []string{"O₂: 30.5%"}},
		{"pressão em kPa", "pressão de 70 kPa", entities.Scenario{O2: 0.21, P: 70 / 101.325, G: 0, Family: "solido"}, []string{"pressure: "}},
		{"pressão em atm", "at 0.5 atm", entities.Scenario{O2: 0.21, P: 0.5, G: 0, Family: "solido"}, []string{"pressure: "}},
		{"gravidade por extenso", "with a gravity of 0.16", entities.Scenario{O2: 0.21, P: 1, G: 0.16, Family: "solido"}, []string{"gravity: lunar gravity"}},
		{"microgravidade e gotas", "microgravity droplets of heptane", entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "gota"}, []string{"gravity: 0 g", "family: droplets"}},
		{"gramas não são gravidade", "a 5 g sample of nomex", entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "nomex"}, []string{"material: nomex"}},
		{"gás", "a gas leak with methane", entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "gas"}, []string{"family: gas flames"}},
		{"sólidos sem material", "which plastic burns?", station, []string{"family: solids"}},
		{"valor absurdo é posto na faixa", "200% oxygen at 900 psi", entities.Scenario{O2: 1, P: 6, G: 0, Family: "solido"}, []string{"O₂: ", "pressure: "}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, got := ParseScenario(c.q, station)
			if !near(s.O2, c.want.O2) || !near(s.P, c.want.P) || !near(s.G, c.want.G) ||
				s.Family != c.want.Family || s.Material != c.want.Material || s.Habitat != c.want.Habitat {
				t.Errorf("cenário = %+v, want %+v", s, c.want)
			}
			if len(got) != len(c.got) {
				t.Fatalf("entendido = %q, want %d itens %q", got, len(c.got), c.got)
			}
			for i, p := range c.got {
				if !strings.HasPrefix(got[i], p) {
					t.Errorf("entendido[%d] = %q, want começar com %q", i, got[i], p)
				}
			}
		})
	}
}

// Citar um material põe a família em sólidos; uma pergunta sem cenário devolve o cenário atual como veio.
func TestParseScenarioFamilyAndMaterial(t *testing.T) {
	drops := entities.Scenario{O2: 0.21, P: 1, Family: "gota"}
	if s, got := ParseScenario("and for cotton?", drops); s.Family != "solido" || s.Material != "sibal" || len(got) != 1 {
		t.Errorf("algodão: %+v, %q", s, got)
	}
	if s, got := ParseScenario("and for droplets?", station); s.Family != "gota" || s.Material != "" || len(got) != 1 {
		t.Errorf("gotas: %+v, %q", s, got)
	}
	cur := entities.Scenario{O2: 0.21, P: 1, Family: "solido", Material: "pmma", Habitat: "iss"}
	if s, got := ParseScenario("is it safe?", cur); s != cur || got != nil {
		t.Errorf("pergunta sem cenário alterou o cenário: %+v, %q", s, got)
	}
}

func TestRenumber(t *testing.T) {
	all := []entities.Citation{{Ref: "T1", Title: "t1"}, {Ref: "T2", Title: "t2"}, {Ref: "D1", Title: "d1"}, {Ref: "D2", Title: "d2"}}
	cases := []struct {
		name   string
		answer string
		want   string
		refs   []string // citações usadas, na ordem
	}{
		{"na ordem em que aparecem", "A [D2]. B [T1]. C [D1].", "A [1]. B [2]. C [3].", []string{"D2", "T1", "D1"}},
		{"referência repetida mantém o número", "A [T2]. B [T2] and [D1]. C [T2].", "A [1]. B [1] and [2]. C [1].", []string{"T2", "D1"}},
		{"referência que não existe some", "A [T9]. B [D1][D7].", "A . B [1].", []string{"D1"}},
		{"sem referências", "Nothing to cite.", "Nothing to cite.", []string{}},
		{"só o formato [T1]/[D1] conta", "A [1] [X1] [t1] [T1a] [T1].", "A [1] [X1] [t1] [T1a] [1].", []string{"T1"}},
		{"resposta vazia", "", "", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, used := renumber(c.answer, all)
			if out != c.want {
				t.Errorf("texto = %q, want %q", out, c.want)
			}
			if used == nil {
				t.Fatal("as citações usadas têm de ser [] e não null no JSON")
			}
			var refs []string
			for i, u := range used {
				refs = append(refs, u.Ref)
				if u.N != i+1 {
					t.Errorf("citação %s com n = %d, want %d", u.Ref, u.N, i+1)
				}
			}
			if len(refs) != len(c.refs) || (len(refs) > 0 && !reflect.DeepEqual(refs, c.refs)) {
				t.Errorf("usadas = %v, want %v", refs, c.refs)
			}
		})
	}
	if all[0].N != 0 { // a lista de entrada não é alterada
		t.Error("renumber alterou a lista original")
	}
}

func TestRagCitations(t *testing.T) {
	yes, no := true, false
	page, row := 12, 7
	long := strings.Repeat("flame spread ", 80)
	sources := []entities.RagSource{
		{Citation: 1, Title: "BASS-II final report", Experiment: "BASS-II", SourceType: "pdf", Page: &page, Cited: &yes, Excerpt: "  spaced \n  text  ", URL: "https://x/1"},
		{Citation: 2, Title: "Conditions", Experiment: "FLEX", SourceType: "csv", Row: &row, Cited: &no, RetrievalMethod: "conditions_table"},
		{Citation: 3, Title: "Equation 6.1", SourceType: "", Page: &page, Cited: &yes, FormulaValidation: "machine_transcription_unreviewed", Excerpt: long,
			DataWarnings: []string{"units unclear"}},
		{Citation: 4, Title: "No flag"},
	}
	cited := ragCitations(sources)
	if len(cited) != 2 || cited[0].Ref != "R1" || cited[1].Ref != "R3" {
		t.Fatalf("com alguma fonte citada só as citadas entram: %+v", cited)
	}
	a := cited[0]
	if a.N != 1 || a.Kind != "documento" || a.Title != "BASS-II final report" || a.Where != "pdf, p. 12" || a.Text != "spaced text" || a.URL != "https://x/1" || a.Note != "" {
		t.Errorf("R1: %+v", a)
	}
	b := cited[1]
	if b.Where != "p. 12" { // sem tipo de fonte não sobra vírgula na frente
		t.Errorf("R3 where = %q", b.Where)
	}
	if !strings.Contains(b.Note, "not reviewed") || len(b.Warnings) != 1 || len([]rune(b.Text)) > 620 || !strings.HasSuffix(b.Text, "…") {
		t.Errorf("R3: nota %q, avisos %v, texto de %d caracteres", b.Note, b.Warnings, len([]rune(b.Text)))
	}

	// nenhuma marcada como citada: entram todas
	for i := range sources {
		sources[i].Cited = nil
	}
	all := ragCitations(sources)
	if len(all) != 4 {
		t.Fatalf("sem marcação entram todas: %d", len(all))
	}
	if all[1].Title != "FLEX · Conditions" || all[1].Where != "csv, row 7" || !strings.Contains(all[1].Note, "Conditions table") {
		t.Errorf("R2: %+v", all[1])
	}
	if empty := ragCitations(nil); empty == nil || len(empty) != 0 {
		t.Errorf("sem fontes = %v, want lista vazia", empty)
	}
	notes := map[string]string{"visual_transcription_available": "checked against the PDF image", "other_state": "Formula: other_state", "": ""}
	for state, want := range notes {
		c := ragCitations([]entities.RagSource{{Citation: 1, FormulaValidation: state}})
		if !strings.Contains(c[0].Note, want) || (want == "" && c[0].Note != "") {
			t.Errorf("formula_validation %q: nota %q", state, c[0].Note)
		}
	}
}

// A pergunta que aponta para "este material" ou "aqui" leva o cenário da tela junto para o RAG.
func TestRagContent(t *testing.T) {
	sc := entities.Scenario{O2: 0.30, P: 0.694, G: 0.165, Family: "solido", Material: "pmma"}
	cases := []struct {
		q    string
		got  []string
		with bool
	}{
		{"Is this material safe here?", nil, true},
		{"Este cenário é seguro?", nil, true},
		{"What is the BASS-II experiment?", nil, false},
		{"Is this material safe at 30% oxygen?", []string{"O₂: 30%"}, false}, // o cenário já veio escrito na pergunta
	}
	for _, c := range cases {
		out := ragContent(entities.AskIn{Question: c.q}, sc, c.got)
		has := strings.Contains(out, "(Scenario under analysis: acrylic (PMMA), 30% oxygen, pressure of 10.2 psi, lunar gravity (0.17 g).)")
		if has != c.with || !strings.HasPrefix(out, c.q) {
			t.Errorf("ragContent(%q) = %q", c.q, out)
		}
	}
}

func TestApplyInferred(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	s := func(v string) *string { return &v }
	cur := entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "sibal", Habitat: "iss"}
	cases := []struct {
		name string
		in   inferred
		want entities.Scenario
		n    int
	}{
		{"nada", inferred{}, cur, 0},
		{"habitat", inferred{Habitat: s("lua")}, entities.Scenario{O2: 0.30, P: 0.694, G: 0.165, Family: "solido", Material: "sibal", Habitat: "lua"}, 1},
		{"habitat que não existe", inferred{Habitat: s("venus")}, cur, 0},
		{"valor próprio tira o nome do habitat", inferred{O2: f(40)}, entities.Scenario{O2: 0.40, P: 1, G: 0, Family: "solido", Material: "sibal"}, 1},
		{"valores que batem com um habitat mantêm o nome", inferred{O2: f(30), PSI: f(10.2), G: f(0.165)},
			entities.Scenario{O2: 0.30, P: 10.2 / 14.696, G: 0.165, Family: "solido", Material: "sibal", Habitat: "lua"}, 3},
		{"fora da faixa é ignorado", inferred{O2: f(0.5), PSI: f(500), G: f(3)}, cur, 0},
		{"material sólido", inferred{Material: s("nomex")}, entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "nomex", Habitat: "iss"}, 1},
		{"gotas trocam a família", inferred{Material: s("gota")}, entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "gota", Habitat: "iss"}, 1},
		{"material desconhecido é ignorado", inferred{Material: s("uranium")}, cur, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, understood := applyInferred(cur, c.in)
			if !near(got.O2, c.want.O2) || !near(got.P, c.want.P) || !near(got.G, c.want.G) ||
				got.Family != c.want.Family || got.Material != c.want.Material || got.Habitat != c.want.Habitat {
				t.Errorf("cenário = %+v, want %+v", got, c.want)
			}
			if len(understood) != c.n {
				t.Errorf("entendido = %q, want %d itens", understood, c.n)
			}
		})
	}
}

func TestInferMethod(t *testing.T) {
	cases := []struct {
		name   string
		llm    *fakeLLM
		method string
		o2     float64
		calls  int32
	}{
		{"modelo responde JSON", &fakeLLM{name: "anthropic", jsonOut: `{"o2_percent": 34, "material": null}`}, "modelo", 0.34, 1},
		{"JSON no meio de texto", &fakeLLM{name: "openai", jsonOut: "Sure:\n```json\n{\"o2_percent\": 34}\n```"}, "modelo", 0.34, 1},
		{"resposta que não é JSON cai nas regras", &fakeLLM{name: "anthropic", jsonOut: "I cannot help"}, "regras", 0.30, 1},
		{"JSON quebrado cai nas regras", &fakeLLM{name: "anthropic", jsonOut: `{"o2_percent": }`}, "regras", 0.30, 1},
		{"modelo falha", &fakeLLM{name: "anthropic", jsonErr: errors.New("HTTP 529")}, "regras", 0.30, 1},
		{"sem modelo ele nem é chamado", &fakeLLM{name: "nenhum"}, "regras", 0.30, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uc := &InferScenarioUseCase{LLM: c.llm}
			s, _, method := uc.Infer(context.Background(), "a cabin with 30% oxygen", station)
			if method != c.method || !near(s.O2, c.o2) {
				t.Errorf("método %s, O₂ %v; want %s, %v", method, s.O2, c.method, c.o2)
			}
			if c.llm.jsonCalls.Load() != c.calls {
				t.Errorf("chamadas ao modelo = %d, want %d", c.llm.jsonCalls.Load(), c.calls)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	uc := &InferScenarioUseCase{LLM: &fakeLLM{name: "nenhum"}}
	r := uc.Describe(context.Background(), "hello there", nil)
	if r.Understood == nil || len(r.Understood) != 0 || r.Changed || r.Method != "regras" || r.Scenario.Habitat != "iss" || r.Scenario.Material != "sibal" {
		t.Errorf("sem cenário e sem nada entendido: %+v", r)
	}
	cur := entities.Scenario{O2: 0.21, P: 1, Family: "solido"}
	r = uc.Describe(context.Background(), "a lunar base", &cur)
	if !r.Changed || r.Scenario.Habitat != "lua" || len(r.Understood) != 1 {
		t.Errorf("base lunar: %+v", r)
	}
}

func TestExtractive(t *testing.T) {
	o2, p := 0.21, 1.0
	nb := func(label string) entities.Neighbor {
		return entities.Neighbor{Test: &entities.Test{Label: label, O2: &o2, P: &p, Outcome: "queimou"}, InvName: "BASS-II"}
	}
	out := entities.AskOut{Verdict: entities.Verdict{Headline: "Measured: 4 NASA tests.", Explanation: "A real test exists.",
		Nearest: []entities.Neighbor{nb("a"), nb("b"), nb("c"), nb("d")}},
		Ranking: []entities.Ranked{{Name: "BASS-II", Reason: "Has a test."}}}
	hits := []entities.Hit{{Title: "Doc 1", Text: "first  excerpt"}, {Title: "Doc 2", Text: "second"}, {Title: "Doc 3", Text: "third"}, {Title: "Doc 4", Text: "fourth"}}

	vt := verdictText(out.Verdict)
	if !strings.HasPrefix(vt, "Measured: 4 NASA tests. A real test exists.") || strings.Count(vt, "\n- ") != 3 ||
		!strings.Contains(vt, "- BASS-II a: 21% O₂ at 14.7 psi in 0 g, outcome: burned [T1]") || strings.Contains(vt, "[T4]") {
		t.Errorf("verdictText = %q", vt)
	}
	full := extractive(out, hits)
	if !strings.HasPrefix(full, vt) || !strings.Contains(full, "- Doc 1: “first excerpt” [D1]") || strings.Contains(full, "[D4]") ||
		!strings.Contains(full, "Most relevant investigation for this scenario: BASS-II. Has a test.") {
		t.Errorf("extractive = %q", full)
	}
	out.Abstained = true
	abst := extractive(out, nil)
	if !strings.HasPrefix(abst, vt) || !strings.Contains(abst, "found no excerpts close to this question") || strings.Contains(abst, "What the archive says") {
		t.Errorf("abstenção = %q", abst)
	}
	if got := verdictText(entities.Verdict{Headline: "Uncharted.", Explanation: "Nothing."}); got != "Uncharted. Nothing." {
		t.Errorf("sem vizinhos = %q", got)
	}
}
