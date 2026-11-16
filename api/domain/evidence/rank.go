package evidence

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// Pesos do ranking. Ficam visíveis na interface: o usuário vê por que cada investigação subiu.
const (
	WProx, WRel, WData, WPrio = 0.35, 0.25, 0.15, 0.25
)

// Embedder gera o embedding de um texto (serviço de embeddings da ingestão).
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// RankUseCase ordena as investigações para o cenário, pela pergunta ou pela descrição do cenário.
type RankUseCase struct {
	Catalog  *entities.Catalog
	Embedder Embedder
}

// Rank devolve o ranking e o texto usado na busca semântica.
func (uc *RankUseCase) Rank(ctx context.Context, s entities.Scenario, question string) ([]entities.Ranked, string, error) {
	s = s.Clean()
	q := strings.TrimSpace(question)
	if q == "" {
		q = ScenarioText(s)
	}
	vec, err := uc.Embedder.Embed(ctx, q)
	if err != nil {
		// embedder fora do ar: o ranking segue sem a parte semântica (Rank aceita vetor vazio)
		log.Printf("ranking: embedder indisponível, sem a parte semântica: %v", err)
		vec = nil
	}
	return Rank(uc.Catalog, s, vec), q, nil
}

// ScenarioText descreve o cenário em inglês (o acervo é em inglês) para a busca semântica.
func ScenarioText(s entities.Scenario) string {
	what := map[string]string{"solido": "solid cabin materials such as fabrics and plastics", "gota": "liquid fuel droplets",
		"gas": "gaseous fuel flames", "todas": "materials and fuels"}[s.Family]
	g := "microgravity"
	if s.G >= 0.95 {
		g = "normal gravity"
	} else if s.G > 0.05 {
		g = "partial gravity (Moon or Mars surface)"
	}
	return fmt.Sprintf("Spacecraft fire safety: flammability limits, flame spread and extinction of %s in a crewed habitat with %.0f%% oxygen at %.1f psi in %s.",
		what, s.O2*100, s.P*14.696, g)
}

// Rank ordena as investigações para um cenário. qvec é o embedding da pergunta (ou do cenário descrito).
func Rank(cat *entities.Catalog, s entities.Scenario, qvec []float32) []entities.Ranked {
	s = s.Clean()
	best := map[string]float64{} // menor distância de atmosfera por investigação
	bestT := map[string]*entities.Test{}
	inT := map[string]*entities.Test{} // o teste mais perto dentro da tolerância do veredito, se houver
	for _, t := range cat.Tests {
		if !t.Plot() {
			continue
		}
		d := s.DAtm(t)
		if cur, ok := best[t.Inv]; !ok || d < cur {
			best[t.Inv], bestT[t.Inv] = d, t
		}
		if s.NearAtm(t) {
			if cur := inT[t.Inv]; cur == nil || d < s.DAtm(cur) {
				inT[t.Inv] = t
			}
		}
	}
	rel := map[string]float64{}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range cat.Invs {
		c := cosine(qvec, v.Vec)
		rel[v.ID] = c
		lo, hi = math.Min(lo, c), math.Max(hi, c)
	}
	var out []entities.Ranked
	for _, v := range cat.Invs {
		p := entities.RankParts{Priority: v.Priority}
		var why []string
		if d, ok := best[v.ID]; ok {
			p.Proximity = math.Exp(-d / 4)
			t := bestT[v.ID]
			// "nesta atmosfera" é a mesma tolerância do veredito (Scenario.NearAtm), para os dois não se contradizerem
			if in := inT[v.ID]; in != nil {
				why = append(why, fmt.Sprintf("has a test in this atmosphere (%s, %s O₂ at %s)", in.Label, ptbr.Pct(*in.O2), ptbr.Psi(*in.P)))
			} else {
				why = append(why, fmt.Sprintf("nearest test differs by %s of O₂ and %s of pressure", ptbr.SignedPP(s.DO2(t)*100), ptbr.SignedPct((math.Exp(s.DP(t))-1)*100)))
			}
		} else if r := o2Range(v); r != nil {
			gap := 0.0
			if s.O2 < r[0] {
				gap = r[0] - s.O2
			} else if s.O2 > r[1] {
				gap = s.O2 - r[1]
			}
			p.Proximity = 0.6 * math.Exp(-(gap/entities.TolO2)/4)
			why = append(why, fmt.Sprintf("covers %s to %s O₂, with no pressure tabulated per test", ptbr.Pct(r[0]), ptbr.Pct(r[1])))
		} else {
			why = append(why, "no O₂ tabulated per test")
		}
		if hi > lo {
			p.Relevance = (rel[v.ID] - lo) / (hi - lo)
		}
		p.Data = dataReadiness(v)
		score := WProx*p.Proximity + WRel*p.Relevance + WData*p.Data + WPrio*p.Priority
		if s.Family != "todas" && s.Family != "" && v.Family != s.Family {
			score *= 0.8
		}
		if v.StatsData.NTests > 0 {
			why = append(why, ptbr.Plural(v.StatsData.NTests, "tabulated row", "tabulated rows"))
		}
		why = append(why, ptbr.LowerFirst(v.PriorityWhy))
		out = append(out, entities.Ranked{ID: v.ID, Name: v.Name, Family: v.Family, Role: v.Role, Score: ptbr.Round3(score), Parts: entities.RankParts{
			Proximity: ptbr.Round3(p.Proximity), Relevance: ptbr.Round3(p.Relevance), Data: ptbr.Round3(p.Data), Priority: ptbr.Round3(p.Priority)},
			Reason: ptbr.UpperFirst(strings.Join(why, "; ")) + "."})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

func o2Range(v *entities.Investigation) []float64 {
	if len(v.StatsData.O2) == 2 {
		return v.StatsData.O2
	}
	if v.StatsData.Declared != nil && len(v.StatsData.Declared.O2) == 2 {
		return v.StatsData.Declared.O2
	}
	return nil
}

func dataReadiness(v *entities.Investigation) float64 {
	s := 0.0
	if v.StatsData.HasO2 {
		s += 0.3
	}
	if v.StatsData.HasP {
		s += 0.2
	}
	if v.StatsData.HasOutcome {
		s += 0.3
	}
	if v.StatsData.NTests > 0 {
		s += 0.2 * math.Min(1, math.Log10(float64(v.StatsData.NTests)+1)/math.Log10(300))
	}
	return s
}
