package evidence

import (
	"fmt"
	"math"
	"sort"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// External resume os ensaios de fora da NASA na atmosfera do cenário (mesmas tolerâncias do veredito).
// v é o veredito já decidido só com os testes da NASA; ele não muda aqui.
func External(cat *entities.Catalog, s entities.Scenario, v *entities.Verdict) *entities.ExternalEvidence {
	d := cat.External
	if d == nil {
		return nil
	}
	s = s.Clean()
	e := &entities.ExternalEvidence{ID: d.ID, Name: d.Name, Agency: d.Agency, License: d.License, Records: d.Records}
	type near struct {
		t    *entities.ExternalTest
		dist float64
	}
	var in []near
	lo, hi := math.Inf(1), math.Inf(-1)
	o2lo, o2hi, plo, phi := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1) // onde ficam os ensaios com velocidade medida
	for i := range d.Tests {
		x := &d.Tests[i]
		t := x.AsTest(d.ID)
		if !s.Matches(t) || !s.NearAtm(t) {
			continue
		}
		in = append(in, near{x, s.DAtm(t)})
		switch x.Outcome {
		case "queimou":
			e.Burned++
		case "extinguiu":
			e.Stopped++
		default:
			e.Unknown++
		}
		if x.Spread != nil {
			e.NSpread++
			lo, hi = math.Min(lo, *x.Spread), math.Max(hi, *x.Spread)
			o2lo, o2hi, plo, phi = math.Min(o2lo, x.O2), math.Max(o2hi, x.O2), math.Min(plo, x.P), math.Max(phi, x.P)
		}
	}
	if len(in) == 0 {
		return nil
	}
	e.N = len(in)
	if e.NSpread > 0 {
		e.Spread = &[2]float64{lo, hi}
	}
	sort.SliceStable(in, func(i, j int) bool { return in[i].dist < in[j].dist })
	for _, n := range in[:min(4, len(in))] {
		e.Nearest = append(e.Nearest, entities.ExternalNear{ID: n.t.ID, Label: n.t.Label, O2: n.t.O2, P: n.t.P, Flow: n.t.Flow,
			Outcome: n.t.Outcome, Raw: n.t.Raw, Spread: n.t.Spread})
	}
	// o nível que seria, se estes ensaios contassem: todos são de 0 g
	same := v.SameG
	if s.G <= entities.TolG {
		same += e.N
	}
	e.TierWith = Classify(s.G, same, v.ZeroG+e.N, v.OneG)
	e.TierLabel = ptbr.Tier[e.TierWith]

	e.Headline = fmt.Sprintf("Outside NASA, %s ignited thin acrylic film in this air, in weightlessness.", ptbr.Plural(e.N, "drop-tower test", "drop-tower tests"))
	parts := []string{}
	if e.Burned > 0 {
		parts = append(parts, fmt.Sprintf("%d burned to the end", e.Burned))
	}
	if e.Stopped > 0 {
		parts = append(parts, fmt.Sprintf("%d went out at a metal firebreak", e.Stopped))
	}
	if e.Unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d ignited but have no outcome read yet", e.Unknown))
	}
	e.Detail = d.Agency + ": " + ptbr.JoinList(parts) + "."
	if e.Spread != nil {
		where := fmt.Sprintf("%s, at %s to %s O₂ and %s to %s", ptbr.Plural(e.NSpread, "test", "tests"), ptbr.Pct(o2lo), ptbr.Pct(o2hi), ptbr.Num(plo*14.696, 1), ptbr.Psi(phi))
		if hi-lo < 0.05 {
			e.Detail += fmt.Sprintf(" Measured spread speed: %s mm/s (%s).", ptbr.Num(lo, 1), where)
		} else {
			e.Detail += fmt.Sprintf(" Measured spread speed: %s to %s mm/s (%s).", ptbr.Num(lo, 1), ptbr.Num(hi, 1), where)
		}
	}
	e.Caveat = "One material (0.075 mm PMMA film), a few seconds of 0 g per drop, flame against a 9 to 20 cm/s airflow. Not NASA data, so the evidence level above does not count them."
	switch {
	case e.TierWith == v.Tier:
	case s.G > entities.TolG:
		e.Caveat += fmt.Sprintf(" If they counted, the level would be \"%s\": tests in 0 g only, none in %s.", e.TierLabel, ptbr.GLabel(s.G))
	default:
		e.Caveat += fmt.Sprintf(" If they counted, the level would be \"%s\".", e.TierLabel)
	}
	return e
}
