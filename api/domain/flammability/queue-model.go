package flammability

// A segunda fila do Próximo fogo: os ensaios que o modelo neural de propagação da equipe pede.
// Cada ficha sai de um limite do próprio modelo, lido da tabela que a API serve em /api/predict/spread:
// onde ele não dá número, onde só extrapola, e onde não separa o efeito do oxigênio do da pressão
// (nos ensaios do ZARM os dois variam juntos). Não mexe na fila da curva da NASA nem nos vereditos.
//
// A ordem é uma regra fixa: primeiro onde o modelo não dá número nenhum; depois, onde só extrapola;
// dentro de cada grupo, quanto as redes discordam (da mais lenta à mais rápida, sobre a média).

import (
	"fmt"
	"sort"

	"atlas-api/domain/entities"
	"atlas-api/domain/predict"
	"atlas-api/domain/ptbr"
)

// ModelQueueMethod explica a ordem da segunda fila na interface.
const ModelQueueMethod = "These tests come from the limits of the team's spread-speed model, which learned from drop-tower tests where oxygen and pressure always changed together and the airflow stayed between 9 and 20 cm/s. " +
	"The order is: first the conditions where the model gives no number at all; then the ones where it only extrapolates; within each group, how much its eight networks disagree. " +
	"They do not change the NASA evidence level."

const kPaPerAtm, kPaPerPsi = 101.325, 6.894757

// ModelQueue monta as fichas pedidas pelo modelo. Sem a tabela devolve nil.
func ModelQueue(t *entities.SpreadTable) []entities.Proposal {
	if t == nil {
		return nil
	}
	rp, rf := t.TrainRange["pressure_kpa"], t.TrainRange["flow_cm_s"]
	film := fmt.Sprintf("%s mm acrylic (PMMA) film with the flame moving against the airflow: the same sample as the drop-tower tests", ptbr.Num(t.Sample.Thickness, 3))
	tower := "A drop tower, with the same rig as the ZARM tests: each drop gives a few seconds of 0 g."
	src := append([]string{t.Model.Name + " (team model), table of " + t.Date}, t.Attribution...)
	at := func(o2, atm, wind float64) entities.SpreadOut {
		return predict.Spread(t, entities.SpreadIn{Scenario: entities.Scenario{O2: o2, P: atm, Family: "solido", Material: t.Sample.Material}, Wind: &wind})
	}
	say := func(o entities.SpreadOut) string {
		if !o.Available {
			return "The model gives no number here"
		}
		return fmt.Sprintf("The model gives %s mm/s (its networks say %s to %s)", ptbr.Num(o.V, 1), ptbr.Num(o.Nets[0], 1), ptbr.Num(o.Nets[1], 1))
	}
	station := at(0.21, 1, entities.VentForced)
	var lua, rover entities.Habitat
	for _, h := range entities.Habitats {
		switch h.ID {
		case "lua":
			lua = h
		case "rover":
			rover = h
		}
	}
	lowP := rp[0] / kPaPerAtm // a menor pressão dos ensaios
	var out []entities.Proposal
	add := func(p entities.Proposal, o entities.SpreadOut) {
		p.Kind, p.Gravity, p.Material, p.Platform, p.Sources = "modelo", "0 g", film, tower, src
		p.Closes, p.Remains = []entities.Closed{}, []string{}
		p.Lunar = entities.LunarEffect{Text: "Does not change the NASA evidence level. It tells how far the spread-speed number shown for the lunar base can be trusted."}
		// nota da ordem: 2 = sem número; 1 = extrapola; mais a discordância entre as redes
		switch {
		case !o.Available:
			p.Score = 2
		case !o.InRange:
			p.Score = 1
		}
		if o.Available && o.V > 0 {
			p.GainO2 = ptbr.Round3((o.Nets[1] - o.Nets[0]) / o.V) // aqui: discordância relativa entre as redes
		}
		out = append(out, p)
	}

	// 1. pressão baixa com o oxigênio da estação: separa os dois efeitos
	o := at(0.21, lowP, entities.VentForced)
	add(entities.Proposal{
		ID: "m-pressao", Title: "Lower the pressure without adding oxygen",
		O2: "21%, as in station air", Pressure: ptbr.Psi(lowP) + ", the lowest pressure of the drop-tower tests",
		Flow:    fmt.Sprintf("%s cm/s against the flame", ptbr.Num(entities.VentForced, 0)),
		Measure: "The spread speed of the flame front, from the infrared recording.",
		Prediction: fmt.Sprintf("%s, against %s mm/s in station air: about %s times faster from pressure alone. That is not a measured effect: in every test the model saw, lower pressure came with more oxygen.",
			say(o), ptbr.Num(station.V, 1), ptbr.Num(o.V/station.V, 1)),
		Decides: "Whether the faster spread in exploration air comes from the extra oxygen or from the lower pressure. Today the model cannot tell them apart, so it cannot be trusted off the line its tests follow.",
		Factors: []string{"low pressure without extra oxygen"},
	}, o)

	// 2. oxigênio alto na pressão da estação: o outro lado da mesma pergunta
	o = at(0.36, 1, entities.VentForced)
	add(entities.Proposal{
		ID: "m-oxigenio", Title: "Add oxygen without lowering the pressure",
		O2: "36%, the richest air of the drop-tower tests", Pressure: "14.7 psi, as in station air",
		Flow:    fmt.Sprintf("%s cm/s against the flame", ptbr.Num(entities.VentForced, 0)),
		Measure: "The spread speed of the flame front, from the infrared recording.",
		Prediction: fmt.Sprintf("%s, against %s mm/s in station air. In the tests, 36%% oxygen always came with about 9 psi, where the measured speed was between 4 and 6 mm/s.",
			say(o), ptbr.Num(station.V, 1)),
		Decides: "Together with the low-pressure test, it gives the model the two corners it never saw: oxygen and pressure moved one at a time.",
		Factors: []string{"extra oxygen at station pressure"},
	}, o)

	// 3. ar mais lento que os ensaios, no ar da base lunar: o lado em que a chama morre por falta de ar
	o = at(lua.O2, lua.P, 2)
	add(entities.Proposal{
		ID: "m-vento-baixo", Title: "Slow the airflow below what was tested",
		O2: ptbr.Pct(lua.O2) + ", lunar base air", Pressure: ptbr.Psi(lua.P),
		Flow:       fmt.Sprintf("2 and 5 cm/s against the flame (the tests went from %s to %s cm/s)", ptbr.Num(rf[0], 0), ptbr.Num(rf[1], 0)),
		Measure:    "Whether the flame keeps spreading, and how fast; if it goes out, when.",
		Prediction: say(o) + ": it has no test below " + ptbr.Num(rf[0], 0) + " cm/s. Asked anyway, its networks keep the flame moving even with no airflow, the opposite of BASS-II on the Station, where flames went out when the airflow dropped.",
		Decides:    "What happens to a fire in exploration air when the ventilation is cut. It is the condition where the model is known to be wrong.",
		Factors:    []string{"airflow below " + ptbr.Num(rf[0], 0) + " cm/s"},
	}, o)

	// 4. ar mais rápido que os ensaios
	o = at(lua.O2, lua.P, 40)
	edge := at(lua.O2, lua.P, rf[1])
	add(entities.Proposal{
		ID: "m-vento-alto", Title: "Speed the airflow above what was tested",
		O2: ptbr.Pct(lua.O2) + ", lunar base air", Pressure: ptbr.Psi(lua.P),
		Flow:    fmt.Sprintf("30 and 40 cm/s against the flame (the tests stopped at %s cm/s)", ptbr.Num(rf[1], 0)),
		Measure: "The spread speed, and the airflow at which the flame is blown off, if it is.",
		Prediction: fmt.Sprintf("%s. At %s cm/s, the fastest airflow it knows, it gives %s mm/s, slower than at %s cm/s: the flame spreads against the wind.",
			say(o), ptbr.Num(rf[1], 0), ptbr.Num(edge.V, 1), ptbr.Num(entities.VentForced, 0)),
		Decides: "Whether a strong airflow stops the spread in enriched air, and at what speed. The model slows the flame down but almost never puts it out.",
		Factors: []string{"airflow above " + ptbr.Num(rf[1], 0) + " cm/s"},
	}, o)

	// 5. o ar do rover: só um pouco abaixo da pressão dos ensaios
	o = at(rover.O2, rover.P, entities.VentForced)
	add(entities.Proposal{
		ID: "m-rover", Title: "Burn the film in rover and Mars-base air",
		O2: ptbr.Pct(rover.O2) + ", fixed", Pressure: ptbr.Psi(rover.P) + fmt.Sprintf(" (the tests went down to %s psi)", ptbr.Num(rp[0]/kPaPerPsi, 1)),
		Flow:    fmt.Sprintf("%s cm/s against the flame", ptbr.Num(entities.VentForced, 0)),
		Measure: "The spread speed of the flame front, from the infrared recording.",
		Prediction: fmt.Sprintf("%s, %s times the speed in station air. The pressure is just below the lowest one tested, so this is an extrapolation.",
			say(o), ptbr.Num(o.V/station.V, 1)),
		Decides: "Turns the extrapolated number shown for the rover and the Mars base into a measured one. It is the smallest step in the queue: the same rig, one more atmosphere.",
		Factors: []string{"pressure at " + ptbr.Psi(rover.P)},
	}, o)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].GainO2 > out[j].GainO2
	})
	for i := range out {
		out[i].Rank = i + 1
		out[i].GainO2 = 0 // o campo é da fila da curva (pontos de O₂); aqui serviu só para ordenar
	}
	return out
}
