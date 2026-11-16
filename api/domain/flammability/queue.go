package flammability

// O próximo fogo: a fila de experimentos que mais encurtam a distância entre o que a NASA já queimou
// e os ambientes de exploração. Cada item muda um fator de cada vez em relação ao que foi testado
// (gravidade, oxigênio ou pressão), ou testa o ambiente inteiro, e diz o que a curva publicada prevê,
// o que o resultado decidiria e quais ressalvas das leituras dos habitats ele tira.
//
// A ordem é uma regra fixa, não uma opinião: ressalvas fechadas por fator novo; no empate, quantos
// pontos de O₂ de incerteza somem; depois, menos fatores novos. Nada disto confere custo nem agenda.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"atlas-api/domain/entities"
	"atlas-api/domain/predict"
	"atlas-api/domain/ptbr"
)

// QueueMethod explica a ordem da fila na interface.
const QueueMethod = "Each test changes one factor at a time relative to what BASS-II already did (gravity, oxygen or pressure), or tests a whole environment. " +
	"The order is: caveats in the target-environment readings that the test closes, divided by the number of new factors; on a tie, how many O₂ points of band disappear; then, fewer new factors. " +
	"The rule does not look at cost, schedule or available equipment."

// GetQueueUseCase monta a fila do Próximo fogo a partir da curva do BASS-II.
type GetQueueUseCase struct {
	Catalog *entities.Catalog
	Spread  predict.SpreadFiles // tabela do modelo de propagação; nil ou sem arquivo = sem a segunda fila
}

func (uc *GetQueueUseCase) Get() map[string]any {
	d := uc.Catalog.Outcomes[BassII]
	if d == nil || len(d.Boundaries) == 0 {
		return map[string]any{"available": false}
	}
	b := &d.Boundaries[0]
	targets, queue := Queue(b)
	model := []entities.Proposal{}
	if uc.Spread != nil {
		if t, err := predict.LoadTable(uc.Spread); err == nil {
			model = ModelQueue(t)
		}
	}
	return map[string]any{
		"available": true, "form": b.Form, "source": b.Published.Source, "url": b.Published.URL, "band_o2": b.Band,
		"targets": targets, "queue": queue, "method": QueueMethod, "model_queue": model, "model_method": ModelQueueMethod,
	}
}

var gapLabel = map[string]string{"gravidade": "gravity", "oxigenio": "oxygen above what was tested", "pressao": "pressure different from what was tested"}

// habitatGaps lista o que no habitat fica fora do que foi testado: gravidade, oxigênio ou pressão.
func habitatGaps(b *entities.Boundary, h entities.Habitat) []string {
	var g []string
	if h.G > 0.005 {
		g = append(g, "gravidade")
	}
	if len(b.Tested.O2) == 2 && h.O2*100 > b.Tested.O2[1]+0.05 {
		g = append(g, "oxigenio")
	}
	if math.Abs(h.P-b.Tested.P) > 0.05*b.Tested.P {
		g = append(g, "pressao")
	}
	return g
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func gName(g float64) string {
	switch {
	case math.Abs(g-0.165) < 0.01:
		return "lunar"
	case math.Abs(g-0.378) < 0.01:
		return "Martian"
	}
	return "partial"
}

// Queue monta a fila de experimentos e a leitura atual de cada habitat-alvo.
func Queue(b *entities.Boundary) ([]entities.HabitatGap, []entities.Proposal) {
	var targets []entities.HabitatGap
	for _, h := range entities.Habitats {
		if !h.Target {
			continue
		}
		as := Assess(b, entities.Scenario{O2: h.O2, P: h.P, G: h.G}, entities.VentForced)
		gaps := habitatGaps(b, h)
		if gaps == nil {
			gaps = []string{}
		}
		targets = append(targets, entities.HabitatGap{Habitat: h, As: as, Gaps: gaps, Width: as.LimitO2[1] - as.LimitO2[0]})
	}
	c := b.Published.Coef
	blowAt := func(o2pct float64) float64 { return (o2pct - c.C) / c.B * c.R0 / 1.5 } // vento que sopra a chama, pelo ramo linear
	var lastBlow float64
	var chk struct {
		Blowoff struct {
			Points []struct {
				O2 float64 `json:"o2"`
			} `json:"points"`
		} `json:"blowoff"`
	}
	if json.Unmarshal(b.Check, &chk) == nil {
		for _, p := range chk.Blowoff.Points {
			lastBlow = math.Max(lastBlow, p.O2)
		}
	}
	rod := "6.35 mm acrylic rod, with the flame spreading with the airflow: the same as in BASS-II"
	near := "Start at 18% and lower it until the flame goes out (without gravity, the limit lies between 13.5% and 17%)"
	sofie := "SoFIE, the successor to BASS on the Station, was planned to vary oxygen and pressure. Those data are not in the PSI archive the Atlas reads: worth checking whether they already cover this condition."
	src := []string{b.Published.Source}

	var out []entities.Proposal
	closesFor := func(what string, match func(entities.Habitat) bool) (cl []entities.Closed, gain float64) {
		for _, t := range targets {
			if has(t.Gaps, what) && match(t.Habitat) {
				cl = append(cl, entities.Closed{Habitat: t.Habitat.ID, Name: t.Habitat.Name, What: what, Label: gapLabel[what]})
				if what == "gravidade" {
					gain += t.Width
				}
			}
		}
		return
	}
	seen := map[string]bool{}

	// 1. gravidade parcial, no ar e na pressão que já foram testados
	for _, t := range targets {
		g := t.Habitat.G
		key := fmt.Sprintf("g%.3f", g)
		if !has(t.Gaps, "gravidade") || seen[key] {
			continue
		}
		seen[key] = true
		as := Assess(b, entities.Scenario{O2: 0.16, P: 1, G: g}, entities.VentForced)
		cl, gain := closesFor("gravidade", func(h entities.Habitat) bool { return math.Abs(h.G-g) < 0.01 })
		est := [2]float64{b.LimitO2(entities.VentForced + as.BuoyantEst[0]), b.LimitO2(entities.VentForced + as.BuoyantEst[1])}
		out = append(out, entities.Proposal{
			ID: key, Kind: "gravidade", Title: "Find the limit in " + gName(g) + " gravity",
			Material: rod, O2: near, Pressure: "14.7 psi, as in the tests", Gravity: ptbr.GLabel(g),
			Flow:    fmt.Sprintf("%s cm/s of ventilation, the same cabin airflow used in the readings", ptbr.Num(entities.VentForced, 0)),
			Measure: "The oxygen level at which the flame goes out, with three repetitions.",
			Prediction: fmt.Sprintf("If gravity creates no airflow at all, the flame goes out near %s%% O₂. If it creates as much as on Earth, near %s%%. If the airflow grows with the cube root of gravity, between %s%% and %s%%.",
				ptbr.Num(as.LimitO2[0], 1), ptbr.Num(as.LimitO2[1], 1), ptbr.Num(est[0], 1), ptbr.Num(est[1], 1)),
			Decides: fmt.Sprintf("The oxygen level at which the flame goes out tells how much airflow %s gravity creates next to the flame. Today that number is a band from 0 to %s cm/s, and NASA says it is not known.", gName(g), ptbr.Num(as.Buoyant[1], 0)),
			Closes:  cl, GainO2: gain, Factors: []string{"partial gravity"},
			Platform: "Needs partial gravity for tens of seconds: a parabolic flight, a centrifuge in orbit or a module on the surface. The BASS-II duct alone will not do.",
			Sources:  append(src, "NASA/TM-20210011385, p. 89 (adding natural airflow to forced airflow)", "Solid Fuel Ignition Testing for Lunar Applications, NTRS 20250002114 (natural airflow on the Moon not known)"),
		})
	}
	// 2. oxigênio acima do testado, sem gravidade e a 14,7 psi
	for _, t := range targets {
		o2 := t.Habitat.O2
		key := fmt.Sprintf("o%.2f", o2)
		if !has(t.Gaps, "oxigenio") || seen[key] {
			continue
		}
		seen[key] = true
		cl, _ := closesFor("oxigenio", func(h entities.Habitat) bool { return math.Abs(h.O2-o2) < 0.005 })
		u := blowAt(o2 * 100)
		pred := fmt.Sprintf("The curve predicts a flame at any airflow: it would only be blown off at about %s cm/s, and the duct reaches %s.", ptbr.Num(u, 0), ptbr.Num(b.Tested.FlowMax, 0))
		if u <= b.Tested.FlowMax {
			pred = fmt.Sprintf("The curve predicts the flame is blown off at about %s cm/s.", ptbr.Num(u, 0))
		}
		out = append(out, entities.Proposal{
			ID: key, Kind: "oxigenio", Title: "Burn at " + ptbr.Pct(o2) + " oxygen, without gravity",
			Material: rod, O2: ptbr.Pct(o2) + ", fixed", Pressure: "14.7 psi, as in the tests", Gravity: "0 g",
			Flow:       fmt.Sprintf("From nearly still air up to %s cm/s, in steps", ptbr.Num(b.Tested.FlowMax, 0)),
			Measure:    "Whether the flame sustains itself at each airflow, and at which airflow it goes out, if it does.",
			Prediction: pred,
			Decides:    fmt.Sprintf("If the flame goes out at some airflow, the curve does not hold above %s%% O₂ and the readings for the habitats with enriched air fall. If it does not go out, the curve gains its first point in that band.", ptbr.Num(b.Tested.O2[1], 1)),
			Closes:     cl, Factors: []string{"oxygen above " + ptbr.Num(b.Tested.O2[1], 1) + "%"},
			Platform:   "Space Station, in an airflow duct like the BASS-II one, with an oxygen-enriched atmosphere.",
			CheckFirst: sofie, Sources: src,
		})
	}
	// 3. pressão de exploração, sem gravidade, perto do limite conhecido
	for _, t := range targets {
		p := t.Habitat.P
		key := fmt.Sprintf("p%.3f", p)
		if !has(t.Gaps, "pressao") || seen[key] {
			continue
		}
		seen[key] = true
		cl, _ := closesFor("pressao", func(h entities.Habitat) bool { return math.Abs(h.P-p) < 0.01 })
		out = append(out, entities.Proposal{
			ID: key, Kind: "pressao", Title: "Find the limit at " + ptbr.Psi(p),
			Material: rod, O2: near, Pressure: ptbr.Psi(p), Gravity: "0 g",
			Flow:       "Three airflows: 5, 15 and 30 cm/s",
			Measure:    "The oxygen level at which the flame goes out at each airflow.",
			Prediction: "No prediction: the published curve has no pressure term, because all tests were at 14.7 psi.",
			Decides:    "Tells whether the boundary measured at 14.7 psi can be used at " + ptbr.Psi(p) + ", or how far it shifts.",
			Closes:     cl, Factors: []string{"pressure at " + ptbr.Psi(p)},
			Platform:   "Space Station, in a chamber that holds reduced pressure. The BASS-II glovebox works at cabin pressure.",
			CheckFirst: sofie, Sources: src,
		})
	}
	// 4. o ambiente inteiro
	for _, t := range targets {
		if len(t.Gaps) < 2 {
			continue
		}
		h := t.Habitat
		var cl []entities.Closed
		var factors []string
		for _, g := range t.Gaps {
			cl = append(cl, entities.Closed{Habitat: h.ID, Name: h.Name, What: g, Label: gapLabel[g]})
			factors = append(factors, map[string]string{"gravidade": "partial gravity", "oxigenio": "oxygen at " + ptbr.Pct(h.O2), "pressao": "pressure at " + ptbr.Psi(h.P)}[g])
		}
		gain := 0.0
		if has(t.Gaps, "gravidade") {
			gain = t.Width
		}
		out = append(out, entities.Proposal{
			ID: "h-" + h.ID, Kind: "habitat", Title: "Test the air and gravity of: " + h.Name,
			Material: rod, O2: ptbr.Pct(h.O2) + ", fixed", Pressure: ptbr.Psi(h.P), Gravity: ptbr.GLabel(h.G),
			Flow:       fmt.Sprintf("From nearly still air up to %s cm/s, in steps", ptbr.Num(b.Tested.FlowMax, 0)),
			Measure:    "Whether the flame sustains itself at each airflow, and at which airflow it goes out, if it does.",
			Prediction: t.As.Headline + ". " + t.As.Detail,
			Decides:    "Replaces the curve reading with a direct measurement of this environment. Because it changes several factors at once, it does not say which one mattered if the result disagrees with the curve.",
			Closes:     cl, GainO2: gain, Factors: factors,
			Platform:   "Needs partial gravity and a controlled atmosphere at the same time: the hardest item in the queue.",
			CheckFirst: sofie, Sources: src,
		})
	}
	// 5. conferir a reta do vento onde o duto ainda alcança
	if c.B > 0 && lastBlow > 0 {
		u := 0.9 * b.Tested.FlowMax
		o2 := c.C + c.B*1.5*u/c.R0
		out = append(out, entities.Proposal{
			ID: "curva", Kind: "curva", Title: "Blow off the flame at " + ptbr.Num(o2, 1) + "% oxygen",
			Material: rod, O2: ptbr.Num(o2, 1) + "%, fixed", Pressure: "14.7 psi, as in the tests", Gravity: "0 g",
			Flow:       fmt.Sprintf("Raise the airflow until the flame is blown off (the duct reaches %s cm/s)", ptbr.Num(b.Tested.FlowMax, 0)),
			Measure:    "The airflow at which the flame is blown off.",
			Prediction: fmt.Sprintf("The curve predicts the flame is blown off at about %s cm/s. The highest-oxygen blowoff measured so far was at %s%%.", ptbr.Num(u, 0), ptbr.Num(lastBlow, 1)),
			Decides:    "Checks whether the straight line of the airflow branch still holds with more oxygen. Every reading above what was tested rests on that line.",
			Closes:     []entities.Closed{}, Factors: []string{},
			Platform: "Space Station, with the same duct and the same rod as BASS-II. It is the only item in the queue that needs no new equipment.",
			Sources:  src,
		})
	}

	for i := range out {
		p := &out[i]
		p.Score = float64(len(p.Closes)) / math.Max(1, float64(len(p.Factors)))
		p.Remains, p.Lunar = remains(targets, p), lunar(targets, p, b.Band)
		if p.Remains == nil {
			p.Remains = []string{}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.Score != c.Score {
			return a.Score > c.Score
		}
		if math.Abs(a.GainO2-c.GainO2) > 1e-9 {
			return a.GainO2 > c.GainO2
		}
		return len(a.Factors) < len(c.Factors)
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return targets, out
}

func closed(p *entities.Proposal, habitat, what string) bool {
	for _, c := range p.Closes {
		if c.Habitat == habitat && c.What == what {
			return true
		}
	}
	return false
}

// remains lista, por habitat, as ressalvas que continuam depois deste teste.
func remains(targets []entities.HabitatGap, p *entities.Proposal) []string {
	var out []string
	for _, t := range targets {
		var left []string
		for _, g := range t.Gaps {
			if !closed(p, t.Habitat.ID, g) {
				left = append(left, gapLabel[g])
			}
		}
		if len(left) > 0 {
			out = append(out, t.Habitat.Name+": "+ptbr.JoinList(left))
		}
	}
	return out
}

func lunar(targets []entities.HabitatGap, p *entities.Proposal, band float64) entities.LunarEffect {
	for _, t := range targets {
		if t.Habitat.ID != "lua" {
			continue
		}
		e := entities.LunarEffect{CaveatsBefore: len(t.Gaps), CaveatsAfter: len(t.Gaps), WidthBefore: t.Width, WidthAfter: t.Width}
		for _, g := range t.Gaps {
			if closed(p, "lua", g) {
				e.CaveatsAfter--
				if g == "gravidade" {
					e.WidthAfter = 0
				}
			}
		}
		switch {
		case e.CaveatsAfter == e.CaveatsBefore:
			e.Text = "Does not change the lunar base reading."
			if p.Kind == "curva" {
				e.Text = "Removes no caveat from the lunar base, but tests the line its reading rests on."
			}
		case e.WidthAfter < e.WidthBefore:
			e.Text = fmt.Sprintf("The lunar base oxygen-limit band, now %s points wide, shrinks to the scatter of the tests themselves (plus or minus %s point). %s.",
				ptbr.Num(e.WidthBefore, 1), ptbr.Num(band, 1), ressalvas(e.CaveatsAfter, e.CaveatsBefore))
		default:
			e.Text = ressalvas(e.CaveatsAfter, e.CaveatsBefore) + " in the lunar base reading. The oxygen-limit band does not change, because it comes from gravity."
		}
		return e
	}
	return entities.LunarEffect{Text: "The lunar base is not among the target environments."}
}

// ressalvas diz quantas ressalvas continuam, numa frase inteira.
func ressalvas(after, before int) string {
	switch after {
	case 0:
		return "No caveat remains"
	case 1:
		return fmt.Sprintf("1 of the %d caveats remains", before)
	}
	return fmt.Sprintf("%d of the %d caveats remain", after, before)
}
