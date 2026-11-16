// Package flammability lê a fronteira de inflamabilidade publicada pela NASA (BASS-II): o mapa de limites,
// a avaliação de um cenário e a fila de experimentos do "Próximo fogo". Nada aqui ajusta curva nova.
package flammability

import (
	"math"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// BASS-II é a investigação com desfechos lidos do relatório e a curva publicada.
const BassII = "PSI-25"

// AssessUseCase avalia um cenário contra a fronteira publicada.
type AssessUseCase struct {
	Catalog *entities.Catalog
}

// Assess devolve (avaliação, true), ou false quando não há curva carregada. form escolhe a forma da amostra;
// forced é o vento da ventilação em cm/s (nil = 10 cm/s).
func (uc *AssessUseCase) Assess(s entities.Scenario, forced *float64, form string) (entities.Assessment, bool) {
	d := uc.Catalog.Outcomes[BassII]
	if d == nil || len(d.Boundaries) == 0 {
		return entities.Assessment{}, false
	}
	b := &d.Boundaries[0]
	for i := range d.Boundaries {
		if d.Boundaries[i].Form == form {
			b = &d.Boundaries[i]
		}
	}
	f := 10.0
	if forced != nil {
		f = math.Min(200, math.Max(0, *forced))
	}
	return Assess(b, s.Clean(), f), true
}

// Assess lê a curva publicada na velocidade do ar do cenário (vento forçado + vento natural da gravidade).
func Assess(b *entities.Boundary, s entities.Scenario, forced float64) entities.Assessment {
	o2 := s.O2 * 100
	as := entities.Assessment{Form: b.Form, O2: o2, Band: b.Band, Forced: forced, Outside: []string{}, Source: b.Published.Source}
	as.Buoyant, as.BuoyantEst = b.Buoyant(s.G)
	as.Flow = [2]float64{forced + as.Buoyant[0], forced + as.Buoyant[1]}
	lo, hi := b.LimitRange(as.Flow[0], as.Flow[1], 0)
	as.LimitO2 = [2]float64{lo, hi}
	as.Margin = [2]float64{o2 - hi, o2 - lo}
	if o2 > 0 {
		as.Dilution = [2]float64{math.Max(0, 1-(hi+b.Band)/o2), math.Max(0, 1-(lo-b.Band)/o2)}
	}

	if len(b.Tested.O2) == 2 && o2 > b.Tested.O2[1]+0.05 {
		as.Outside = append(as.Outside, "No solid test was done above "+ptbr.Num(b.Tested.O2[1], 1)+"% oxygen.")
	}
	if math.Abs(s.P-b.Tested.P) > 0.05*b.Tested.P {
		as.Outside = append(as.Outside, "All tests were at 14.7 psi; this environment is at "+ptbr.Num(s.P*14.696, 1)+" psi.")
	}
	if s.G > 0.005 {
		g := "All tests were without gravity. The natural airflow that gravity creates enters as a band from " +
			ptbr.Num(as.Buoyant[0], 0) + " to " + ptbr.Num(as.Buoyant[1], 0) + " cm/s"
		if s.G < 0.95 {
			g += ", between the space case and the Earth case: NASA says the value in partial gravity is not known"
		}
		as.Outside = append(as.Outside, g+".")
	}
	if as.Flow[1] > b.Tested.FlowMax+0.5 {
		as.Outside = append(as.Outside, "The airflow exceeds the highest tested ("+ptbr.Num(b.Tested.FlowMax, 0)+" cm/s).")
	}
	as.Inside = len(as.Outside) == 0

	lim := ptbr.Num(lo, 1) + "%"
	if hi-lo >= 0.1 {
		lim = "between " + ptbr.Num(lo, 1) + "% and " + ptbr.Num(hi, 1) + "%"
	}
	switch {
	case as.Margin[0] > b.Band:
		as.Status, as.Headline = "queima", "The flame sustains itself"
	case as.Margin[1] < -b.Band:
		as.Status, as.Headline = "nao_sustenta", "The flame does not sustain itself"
	default:
		as.Status, as.Headline = "no_limite", "Right at the limit"
	}
	as.Detail = "At this airflow, acrylic needs " + lim + " oxygen to burn (plus or minus " + ptbr.Num(b.Band, 1) +
		" point). This air has " + ptbr.Num(o2, 1) + "%."
	if !as.Inside {
		as.Detail += " This is a reading of the curve outside the tested range, not a measurement."
	}
	return as
}
