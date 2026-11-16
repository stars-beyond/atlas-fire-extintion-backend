// Package evidence decide o nível de evidência de um cenário e o que dele deriva: veredito, taxa empírica,
// ranking das investigações, próximos testes, grafo de relações e achados. É a regra central do Atlas.
package evidence

import (
	"fmt"
	"math"
	"sort"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// Classify decide o nível de evidência a partir de contagens. É a regra central do Atlas.
func Classify(g float64, same, zero, one int) string {
	switch {
	case same > 0:
		return "medido"
	case zero > 0 && one > 0:
		return "delimitado"
	case zero > 0 || one > 0:
		return "parcial"
	}
	return "sem_evidencia"
}

// TierScore dá peso a cada nível, para medir quanto um ensaio melhora os vereditos.
func TierScore(t string) float64 {
	switch t {
	case "medido":
		return 2
	case "delimitado":
		return 1
	case "parcial":
		return 0.5
	}
	return 0
}

// Verdict monta o veredito do cenário. extra são testes hipotéticos (próximos testes); k limita os vizinhos.
func Verdict(cat *entities.Catalog, s entities.Scenario, extra []*entities.Test, k int) entities.Verdict {
	s = s.Clean()
	v := entities.Verdict{Scenario: s, Outcomes: map[string]int{}}
	var cand []entities.Neighbor
	var same []*entities.Test
	consider := func(t *entities.Test) {
		if !s.Matches(t) {
			return
		}
		in := s.NearAtm(t)
		if in {
			v.InAtm++
			switch {
			case math.Abs(t.G-s.G) <= entities.TolG:
				v.SameG++
				v.Outcomes[t.Outcome]++
				same = append(same, t)
			}
			if t.G <= entities.TolG {
				v.ZeroG++
			}
			if t.G >= 1-entities.TolG {
				v.OneG++
			}
		}
		cand = append(cand, entities.Neighbor{Test: t, Distance: s.DAtm(t), DO2pp: s.DO2(t) * 100,
			DPpct: (math.Exp(s.DP(t)) - 1) * 100, DG: t.G - s.G, Inside: in})
	}
	for _, t := range cat.Tests {
		consider(t)
	}
	for _, t := range extra {
		consider(t)
	}
	// Primeiro os testes que decidem o veredito: os desta atmosfera e desta gravidade, depois os desta
	// atmosfera em outra gravidade, e só então os de fora. Dentro de cada grupo, o mais perto na atmosfera.
	sort.SliceStable(cand, func(i, j int) bool {
		if gi, gj := group(cand[i]), group(cand[j]); gi != gj {
			return gi < gj
		}
		if math.Abs(cand[i].Distance-cand[j].Distance) > 1e-9 {
			return cand[i].Distance < cand[j].Distance
		}
		return math.Abs(cand[i].DG) < math.Abs(cand[j].DG)
	})
	if v.SameG == 0 && v.ZeroG > 0 && v.OneG > 0 {
		// "delimitado": os dois lados (0 g e 1 g) sustentam o veredito, então os dois aparecem na lista
		cand = interleaveSides(cand, v.InAtm)
	}
	if k > 0 && len(cand) > k {
		cand = cand[:k]
	}
	for i := range cand {
		if inv := cat.InvByID[cand[i].Test.Inv]; inv != nil {
			cand[i].InvName = inv.Name
		}
	}
	v.Nearest = cand
	v.Tier = Classify(s.G, v.SameG, v.ZeroG, v.OneG)
	v.Label = ptbr.Tier[v.Tier]
	if v.Tier == "medido" {
		v.Empirical = empirical(cat, same)
	}
	what := map[string]string{"solido": "solid materials", "gota": "fuel droplets", "gas": "gas flames", "todas": "any fuel"}[s.Family]
	if s.Material != "" && s.Family == "solido" {
		what = map[string]string{"sibal": "cotton fabric (SIBAL)", "pmma": "acrylic (PMMA)", "nomex": "Nomex", "silicone": "silicone"}[s.Material]
		if what == "" {
			what = s.Material
		}
	}
	cond := fmt.Sprintf("%s O₂ at %s in %s", ptbr.Pct(s.O2), ptbr.Psi(s.P), ptbr.GLabel(s.G))
	switch v.Tier {
	case "medido":
		v.Headline = fmt.Sprintf("Measured: %s with %s at this condition.", ptbr.Plural(v.SameG, "NASA test", "NASA tests"), what)
		v.Explanation = fmt.Sprintf("A real test exists at %s (within ±2 O₂ points and ±20%% pressure).", cond)
	case "delimitado":
		v.Headline = fmt.Sprintf("Bounded: no test at %s, but there are tests at 0 g (%d) and at 1 g (%d) in this atmosphere.", ptbr.GLabel(s.G), v.ZeroG, v.OneG)
		v.Explanation = "PSI has no data at partial gravity. The microgravity and normal-gravity results serve as bounds for what happens in between."
	case "parcial":
		side := "0 g"
		n := v.ZeroG
		if v.ZeroG == 0 {
			side, n = "1 g", v.OneG
		}
		v.Headline = fmt.Sprintf("One side only: %s at %s in this atmosphere, and nothing on the other side.", ptbr.Plural(n, "test", "tests"), side)
		v.Explanation = fmt.Sprintf("For %s at %s, the matching test at the other gravity is missing, so the result cannot be bounded.", what, cond)
	default:
		v.Headline = fmt.Sprintf("Uncharted: no NASA test with %s at %s.", what, cond)
		if len(cand) > 0 {
			n := cand[0]
			v.Explanation = fmt.Sprintf("The nearest test (%s %s) differs from the scenario by %s of O₂ and %s of pressure.",
				n.InvName, n.Test.Label, ptbr.SignedPP(n.DO2pp), ptbr.SignedPct(n.DPpct))
		} else {
			v.Explanation = "There is no test in this family with tabulated O₂ and pressure."
		}
	}
	return v
}

// group ordena os vizinhos: 0 = nesta atmosfera e nesta gravidade; 1 = nesta atmosfera, outra gravidade; 2 = fora.
func group(n entities.Neighbor) int {
	switch {
	case n.Inside && math.Abs(n.DG) <= entities.TolG:
		return 0
	case n.Inside:
		return 1
	}
	return 2
}

// interleaveSides alterna, entre os n primeiros vizinhos (os desta atmosfera, já ordenados), os de 0 g e os de
// outra gravidade, para que um corte curto da lista mostre os dois lados.
func interleaveSides(cand []entities.Neighbor, n int) []entities.Neighbor {
	if n > len(cand) {
		n = len(cand)
	}
	var zero, other []entities.Neighbor
	for _, c := range cand[:n] {
		if c.Test.G <= entities.TolG {
			zero = append(zero, c)
		} else {
			other = append(other, c)
		}
	}
	out := make([]entities.Neighbor, 0, len(cand))
	for i := 0; i < len(zero) || i < len(other); i++ {
		if i < len(zero) {
			out = append(out, zero[i])
		}
		if i < len(other) {
			out = append(out, other[i])
		}
	}
	return append(out, cand[n:]...)
}

// empirical conta o que aconteceu nos testes reais desta condição (ver entities.Empirical).
func empirical(cat *entities.Catalog, same []*entities.Test) *entities.Empirical {
	e := &entities.Empirical{Total: len(same)}
	by := map[string]*entities.EmpInv{}
	var order []string
	for _, t := range same {
		b := by[t.Inv]
		if b == nil {
			name := t.Inv
			if inv := cat.InvByID[t.Inv]; inv != nil {
				name = inv.Name
			}
			b = &entities.EmpInv{ID: t.Inv, Name: name}
			by[t.Inv] = b
			order = append(order, t.Inv)
		}
		b.Total++
		switch t.Outcome {
		case "queimou":
			e.Burned++
			b.Burned++
		case "extinguiu":
			e.Extinguished++
		case "disrupcao":
			e.Disrupted++
		case "nao_acendeu":
			e.NoIgnition++
		default:
			e.Unknown++
			continue
		}
		b.Known++
	}
	e.Known = e.Total - e.Unknown
	for _, id := range order {
		e.By = append(e.By, *by[id])
	}
	sort.SliceStable(e.By, func(i, j int) bool { return e.By[i].Total > e.By[j].Total })
	if e.Known == 0 {
		e.Headline = fmt.Sprintf("Found %s at this condition, but none has its outcome in the table.", ptbr.Plural(e.Total, "test", "tests"))
		e.Caveat = "Without a tabulated outcome there is no rate. The results of these tests are in reports and videos, not in the experimental table."
		return e
	}
	r := float64(e.Burned) / float64(e.Known)
	e.Rate = &r
	// "queimou" é a chama que se sustentou na condição de partida (brain/atlas/outcomes.py). No BASS-II a maioria
	// desses testes terminou quando o vento foi reduzido ou aumentado, não com a amostra consumida.
	verb := "sustained a flame"
	e.Headline = fmt.Sprintf("%d of %s with a tabulated outcome %s at this condition.", e.Burned, ptbr.Plural(e.Known, "test", "tests"), verb)
	e.Caveat = "This is a count of real tests, not a probability: it pools different samples, sizes and setups. " +
		"It counts flames that held at the starting condition, whether or not the sample burned to the end."
	if e.Unknown > 0 {
		e.Caveat += fmt.Sprintf(" Left out: %s at this condition with no outcome in the table.", ptbr.Plural(e.Unknown, "other test", "other tests"))
	}
	return e
}
