package evidence

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// Achados: agregados calculados sobre os testes, com a frase que resume cada um.

// MetricReader lê um campo numérico de tests.metrics para os testes de uma investigação.
// Investigação sem o campo devolve um mapa vazio; o erro é só o do banco.
type MetricReader interface {
	MetricFloat(ctx context.Context, inv, key string) (map[string]float64, error)
}

// InsightsUseCase calcula os achados da página Achados: os cinco escritos à mão e os automáticos (insights-auto.go).
// O acervo não muda depois da subida, então a lista é calculada uma vez e guardada (ver List).
type InsightsUseCase struct {
	Catalog *entities.Catalog
	Metrics MetricReader

	mu     sync.RWMutex
	cached []entities.Insight
	done   bool
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// List devolve os achados. O primeiro cálculo que dá certo fica guardado até o processo reiniciar; um erro do
// banco nunca é guardado (o próximo pedido tenta de novo) e nunca vira estatística zerada. A lista devolvida é
// a mesma para todos os pedidos: quem a recebe só lê.
func (uc *InsightsUseCase) List(ctx context.Context) ([]entities.Insight, error) {
	uc.mu.RLock()
	out, done := uc.cached, uc.done
	uc.mu.RUnlock()
	if done {
		return out, nil
	}
	// Fora da trava: enquanto o banco não responde, cada pedido espera só o próprio prazo. Dois pedidos podem
	// calcular ao mesmo tempo antes do primeiro sucesso; o resultado é o mesmo.
	out, err := uc.compute(ctx)
	if err != nil {
		return nil, err
	}
	uc.mu.Lock()
	uc.cached, uc.done = out, true
	uc.mu.Unlock()
	return out, nil
}

func (uc *InsightsUseCase) compute(ctx context.Context) ([]entities.Insight, error) {
	st := uc.Catalog
	// as três leituras do banco primeiro: se uma falha, nada é calculado
	kOf, err := uc.Metrics.MetricFloat(ctx, "PSI-69", "k_mm2_s")
	if err != nil {
		return nil, fmt.Errorf("reading FLEX burning rates: %w", err)
	}
	tad, err := uc.Metrics.MetricFloat(ctx, "PSI-159", "tad_k")
	if err != nil {
		return nil, fmt.Errorf("reading CFI-G adiabatic temperatures: %w", err)
	}
	rate, err := uc.Metrics.MetricFloat(ctx, "PSI-107", "fuel_mg_s")
	if err != nil {
		return nil, fmt.Errorf("reading SPICE fuel rates: %w", err)
	}
	var out []entities.Insight

	// 1. cobertura dos sólidos de cabine
	var sol []*entities.Test
	lo, hi, off := 1.0, 0.0, 0
	for _, t := range st.Tests {
		if t.Family == "solido" && t.Plot() && t.G <= entities.TolG {
			sol = append(sol, t)
			lo, hi = math.Min(lo, *t.O2), math.Max(hi, *t.O2)
			if math.Abs(*t.P-1) > 0.05 {
				off++
			}
		}
	}
	type fam struct {
		Family string  `json:"family"`
		N      int     `json:"n"`
		O2Min  float64 `json:"o2_min"`
		O2Max  float64 `json:"o2_max"`
		PMin   float64 `json:"p_min"`
		PMax   float64 `json:"p_max"`
	}
	fams := map[string]*fam{}
	for _, t := range st.Tests {
		if !t.Plot() {
			continue
		}
		f := fams[t.Family]
		if f == nil {
			f = &fam{Family: t.Family, O2Min: 1, PMin: 99}
			fams[t.Family] = f
		}
		f.N++
		f.O2Min, f.O2Max = math.Min(f.O2Min, *t.O2), math.Max(f.O2Max, *t.O2)
		f.PMin, f.PMax = math.Min(f.PMin, *t.P), math.Max(f.PMax, *t.P)
	}
	var famList []fam
	for _, k := range []string{"solido", "gota", "gas"} {
		if f := fams[k]; f != nil {
			famList = append(famList, *f)
		}
	}
	out = append(out, entities.Insight{ID: "cobertura", Title: "Cabin solids were only tested near Earth's air",
		Finding: fmt.Sprintf("The %d tests of solid materials in microgravity lie between %s and %s O₂, and %d of them depart from 1 atm. The atmosphere planned for the Moon (30%% O₂ or more, 10.2 psi or less) is outside all of them.",
			len(sol), ptbr.Pct(lo), ptbr.Pct(hi), off),
		Source: "BASS-II (PSI-25), Saffire-I and II (PSI-98, PSI-99)", Data: famList})

	// 2. FLEX: como as gotas param de queimar
	type grp struct {
		Group      string  `json:"group"`
		N          int     `json:"n"`
		Extinction float64 `json:"extinguiu"`
		Disruption float64 `json:"disrupcao"`
		Complete   float64 `json:"queimou"`
		KMean      float64 `json:"k_mean"`
	}
	fuelName := map[string]string{"Methanol": "Methanol", "Heptane": "Heptane"} // só os dois combustíveis do resumo
	dilName := map[string]string{"só N₂": "N₂ only", "com CO₂": "with CO₂", "com He": "with He"}
	dil := func(t *entities.Test) string {
		switch {
		case t.He != nil && *t.He > 0:
			return "com He"
		case t.CO2 != nil && *t.CO2 > 0:
			return "com CO₂"
		}
		return "só N₂"
	}
	groups := map[string]*grp{}
	ks := map[string][]float64{}
	kByDil := map[string][]float64{}
	flex := st.ByInv("PSI-69")
	type kpt struct {
		O2   float64 `json:"o2"`
		K    float64 `json:"k"`
		Dil  string  `json:"dil"`
		Fuel string  `json:"fuel"`
		ID   string  `json:"id"`
	}
	var kpts []kpt
	for _, t := range flex {
		if t.Fuel == nil {
			continue
		}
		key := fuelName[*t.Fuel] + " · " + dil(t)
		g := groups[key]
		if g == nil {
			g = &grp{Group: fuelName[*t.Fuel] + " · " + dilName[dil(t)]}
			groups[key] = g
		}
		g.N++
		switch t.Outcome {
		case "extinguiu":
			g.Extinction++
		case "disrupcao":
			g.Disruption++
		case "queimou":
			g.Complete++
		}
		if k, ok := kOf[t.ID]; ok && k > 0 && t.O2 != nil {
			ks[key] = append(ks[key], k)
			kByDil[dil(t)] = append(kByDil[dil(t)], k)
			kpts = append(kpts, kpt{O2: *t.O2, K: k, Dil: dil(t), Fuel: fuelName[*t.Fuel], ID: t.ID})
		}
	}
	var gl []grp
	for _, f := range []string{"Methanol", "Heptane"} {
		for _, d := range []string{"só N₂", "com CO₂", "com He"} {
			if g := groups[f+" · "+d]; g != nil {
				n := float64(g.N)
				g.Extinction, g.Disruption, g.Complete = ptbr.Round3(g.Extinction/n), ptbr.Round3(g.Disruption/n), ptbr.Round3(g.Complete/n)
				g.KMean = ptbr.Round3(mean(ks[f+" · "+d]))
				gl = append(gl, *g)
			}
		}
	}
	out = append(out, entities.Insight{ID: "flex_desfecho", Title: "How a fuel droplet stops burning",
		Finding: fmt.Sprintf("Across %d FLEX tests, the mean burning rate drops from %s mm²/s (N₂ only) to %s with CO₂ and rises to %s with helium. CO₂ slows the flame; helium, which conducts more heat, speeds up burning.",
			len(flex), ptbr.Num(mean(kByDil["só N₂"]), 2), ptbr.Num(mean(kByDil["com CO₂"]), 2), ptbr.Num(mean(kByDil["com He"]), 2)),
		Source: "FLEX (PSI-69)", Data: map[string]any{"groups": gl, "points": kpts}})

	// 3. CFI-G: temperatura adiabática e ignição
	type band struct {
		Band     string  `json:"band"`
		From     int     `json:"from"`
		N        int     `json:"n"`
		Ignition float64 `json:"ignition"`
	}
	bands := map[int]*band{}
	for _, t := range st.ByInv("PSI-159") {
		v, ok := tad[t.ID]
		if !ok || t.Outcome == "desconhecido" {
			continue
		}
		k := int(v/200) * 200
		b := bands[k]
		if b == nil {
			b = &band{Band: fmt.Sprintf("%d–%d", k, k+200), From: k}
			bands[k] = b
		}
		b.N++
		if t.Outcome != "nao_acendeu" {
			b.Ignition++
		}
	}
	var bl []band
	for _, b := range bands {
		if b.N >= 5 {
			b.Ignition = ptbr.Round3(b.Ignition / float64(b.N))
			bl = append(bl, *b)
		}
	}
	sort.Slice(bl, func(i, j int) bool { return bl[i].From < bl[j].From })
	if len(bl) >= 2 {
		out = append(out, entities.Insight{ID: "cfig_ignicao", Title: "Flame temperature predicts ignition better than O₂ does",
			Finding: fmt.Sprintf("In CFI-G, the fraction of tests that ignite goes from %s (adiabatic temperature of %s K) to %s (%s K).",
				ptbr.Pct(bl[0].Ignition), bl[0].Band, ptbr.Pct(bl[len(bl)-1].Ignition), bl[len(bl)-1].Band),
			Source: "ACME CFI-G (PSI-159)", Data: bl})
	}

	// 4. Saffire-II: o mesmo material em 0 g e em 1 g
	type pair struct {
		Sample   string `json:"sample"`
		Material string `json:"material"`
		ZeroG    string `json:"zero_g"`
		OneG     string `json:"one_g"`
		Outcome0 string `json:"outcome_0g"`
		Outcome1 string `json:"outcome_1g"`
	}
	var pairs []pair
	saf := st.ByInv("PSI-99")
	idx := map[int]*pair{}
	for _, t := range saf {
		p := idx[t.Row]
		if p == nil {
			p = &pair{}
			idx[t.Row] = p
		}
		raw := ""
		if t.Raw != nil {
			raw = *t.Raw
		}
		if t.G >= 1-entities.TolG {
			p.OneG, p.Outcome1 = raw, t.Outcome
		} else {
			p.Sample, p.ZeroG, p.Outcome0 = t.Label, raw, t.Outcome
			if t.Fuel != nil {
				p.Material = *t.Fuel
			}
		}
	}
	rowsIdx := make([]int, 0, len(idx))
	for r := range idx {
		rowsIdx = append(rowsIdx, r)
	}
	sort.Ints(rowsIdx)
	flip := 0
	for _, r := range rowsIdx {
		p := idx[r]
		if p.Sample != "" && p.OneG != "" {
			pairs = append(pairs, *p)
			// só conta quando os dois lados dizem o que o título diz: mal queimou em 0 g e queimou inteira em 1 g
			if p.Outcome0 == "extinguiu" && p.Outcome1 == "queimou" {
				flip++
			}
		}
	}
	if len(pairs) > 0 {
		out = append(out, entities.Insight{ID: "saffire_gravidade", Title: "What burns on the ground may not burn in orbit",
			Finding: fmt.Sprintf("In Saffire-II, %d of the %d samples with a comparison barely burned in microgravity and burned completely at 1 g. It is the only pair of limits (0 g and 1 g) for solids in the archive.",
				flip, len(pairs)),
			Source: "Saffire-II (PSI-99)", Data: pairs})
	}

	// 5. SPICE: ponto de fuligem
	type sp struct {
		Fuel string  `json:"fuel"`
		Air  float64 `json:"air_cm_s"`
		Rate float64 `json:"fuel_mg_s"`
	}
	var sps []sp
	fuels := map[string]bool{}
	for _, t := range st.ByInv("PSI-107") {
		if t.Flow == nil || t.Fuel == nil {
			continue
		}
		if r, ok := rate[t.ID]; ok && r > 0 {
			sps = append(sps, sp{Fuel: *t.Fuel, Air: *t.Flow, Rate: r})
			fuels[*t.Fuel] = true
		}
	}
	if len(sps) > 0 {
		out = append(out, entities.Insight{ID: "spice_fuligem", Title: "When the flame starts to give off smoke",
			Finding: fmt.Sprintf("SPICE measured %d soot points across %d fuels: the fuel flow rate at which the flame starts emitting smoke, for each air velocity. Smoke is what the spacecraft's detectors see first.",
				len(sps), len(fuels)),
			Source: "SPICE (PSI-107)", Data: sps})
	}
	// cartões automáticos: novidades da última atualização na frente, investigações novas no fim
	if n := NewsInsight(st); n != nil {
		out = append([]entities.Insight{*n}, out...)
	}
	return append(out, AutoInvInsights(st)...), nil
}
