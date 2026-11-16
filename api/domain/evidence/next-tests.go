package evidence

import (
	"fmt"
	"sync"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// NextTestsMethod explica a heurística na interface.
const NextTestsMethod = "Greedy heuristic: at each step, the test that most improves the verdicts of the target habitats is added (Measured counts 2, Bounded 1, One side only 0.5). It does not check whether current hardware can reach the condition."

// nextTestsCacheMax é o teto de entradas guardadas. As chaves guardáveis já são poucas (famílias × materiais do
// acervo); o teto vale para um acervo que um dia traga materiais demais.
const nextTestsCacheMax = 256

// NextTestsUseCase sugere os ensaios que mais melhoram os vereditos dos habitats-alvo.
//
// Cada resposta custa centenas de vereditos sobre o acervo inteiro, e o acervo não muda depois da subida: o
// resultado fica guardado por (família, material). Família e material chegam livres no pedido, então só se guarda
// o que existe no acervo (família de algum teste; material vazio ou de algum teste), até nextTestsCacheMax
// entradas: valores inventados são calculados e respondidos como sempre, mas não ocupam memória.
type NextTestsUseCase struct {
	Catalog *entities.Catalog

	once      sync.Once
	families  map[string]bool // famílias com teste no acervo
	materials map[string]bool // materiais com teste no acervo, mais o vazio (qualquer material)

	mu    sync.Mutex
	cache map[nextKey]*nextEntry
}

type nextKey struct{ family, material string }

// nextEntry calcula uma vez só, mesmo com vários pedidos iguais ao mesmo tempo.
type nextEntry struct {
	once sync.Once
	out  []entities.NextTest
}

// Next devolve os próximos ensaios para a família e o material. A lista devolvida pode ser a guardada, a mesma
// para todos os pedidos: quem a recebe só lê.
func (uc *NextTestsUseCase) Next(family, material string) []entities.NextTest {
	if family == "" || family == "todas" { // a mesma regra de NextTests, para as duas formas dividirem a entrada
		family = "solido"
	}
	e := uc.entry(nextKey{family, material})
	if e == nil {
		return NextTests(uc.Catalog, family, material, 5)
	}
	e.once.Do(func() { e.out = NextTests(uc.Catalog, family, material, 5) })
	return e.out
}

// entry devolve a entrada da chave, criando-a se ela for guardável e ainda houver espaço; senão, nil.
func (uc *NextTestsUseCase) entry(k nextKey) *nextEntry {
	uc.once.Do(func() {
		uc.families, uc.materials = map[string]bool{}, map[string]bool{"": true}
		for _, t := range uc.Catalog.Tests {
			uc.families[t.Family] = true
			if t.Material != nil {
				uc.materials[*t.Material] = true
			}
		}
	})
	if !uc.families[k.family] || !uc.materials[k.material] {
		return nil
	}
	uc.mu.Lock()
	defer uc.mu.Unlock()
	e := uc.cache[k]
	if e == nil && len(uc.cache) < nextTestsCacheMax {
		if uc.cache == nil {
			uc.cache = map[nextKey]*nextEntry{}
		}
		e = &nextEntry{}
		uc.cache[k] = e
	}
	return e
}

// NextTests escolhe, um a um, os ensaios que mais melhoram os vereditos dos habitats-alvo.
// É uma heurística gulosa, não um desenho experimental ótimo.
func NextTests(cat *entities.Catalog, family, material string, n int) []entities.NextTest {
	if family == "" || family == "todas" {
		family = "solido"
	}
	var targets []entities.Habitat
	for _, h := range entities.Habitats {
		if h.Target {
			targets = append(targets, h)
		}
	}
	var extra []*entities.Test
	tierOf := func(h entities.Habitat) string {
		return Verdict(cat, entities.Scenario{O2: h.O2, P: h.P, G: h.G, Family: family, Material: material}, extra, 1).Tier
	}
	var out []entities.NextTest
	for step := 0; step < n; step++ {
		before := map[string]string{}
		for _, h := range targets {
			before[h.ID] = tierOf(h)
		}
		best := entities.NextTest{Gain: 0}
		var bestTest *entities.Test
		for _, o2 := range []float64{0.21, 0.25, 0.30, 0.34, 0.40} {
			for _, p := range []float64{1.0, 0.694, 0.558} {
				for _, g := range []float64{0, 1} {
					o, pp := o2, p
					fam := family
					t := &entities.Test{ID: "hipotetico", Family: fam, O2: &o, P: &pp, G: g}
					if material != "" {
						m := material
						t.Material = &m
					}
					extra = append(extra, t)
					gain := 0.0
					var closes []string
					for _, h := range targets {
						after := tierOf(h)
						if d := TierScore(after) - TierScore(before[h.ID]); d > 0 {
							gain += d
							closes = append(closes, fmt.Sprintf("%s: %s → %s", h.Name, ptbr.Tier[before[h.ID]], ptbr.Tier[after]))
						}
					}
					extra = extra[:len(extra)-1]
					// desempate: prefere o ensaio mais barato (solo antes de órbita, pressão de cabine antes de reduzida)
					if gain > best.Gain+1e-9 {
						platform := "ISS or orbital flight (0 g)"
						if g == 1 {
							platform = "Ground laboratory (1 g)"
						}
						best = entities.NextTest{O2: o2, P: p, G: g, Platform: platform, Gain: gain, Closes: closes}
						bestTest = t
					}
				}
			}
		}
		if bestTest == nil {
			break
		}
		best.Why = fmt.Sprintf("A test at %s O₂ and %s in %s improves %s.", ptbr.Pct(best.O2), ptbr.Psi(best.P), ptbr.GLabel(best.G),
			ptbr.Plural(len(best.Closes), "verdict", "verdicts"))
		out = append(out, best)
		extra = append(extra, bestTest)
	}
	return out
}
