package ask

import (
	"fmt"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

func scenarioSubject(s entities.Scenario) string {
	names := map[string]string{"sibal": "cotton fabric (SIBAL)", "pmma": "acrylic (PMMA)", "nomex": "Nomex", "silicone": "silicone"}
	if s.Family == "solido" && names[s.Material] != "" {
		return names[s.Material]
	}
	return map[string]string{"solido": "solid cabin materials", "gota": "liquid fuel droplets", "gas": "gas flames", "todas": "any fuel"}[s.Family]
}

func testWhere(t *entities.Test) string {
	return fmt.Sprintf("%s, row %d", t.File, t.Row)
}

// testCitation é a citação [Tn] do n-ésimo teste vizinho (i a partir de zero), com a linha que o descreve.
func testCitation(i int, n entities.Neighbor) entities.Citation {
	return entities.Citation{Ref: fmt.Sprintf("T%d", i+1), Kind: "teste", Title: n.InvName + " · test " + n.Test.Label,
		Where: testWhere(n.Test), Inv: n.Test.Inv, Test: n.Test.ID, Text: testLine(n)}
}

func testLine(n entities.Neighbor) string {
	t := n.Test
	b := fmt.Sprintf("%s %s: %s", n.InvName, t.Label, ptbr.Family[t.Family])
	if t.Fuel != nil && *t.Fuel != "" {
		b += " (" + ptbr.Shorten(*t.Fuel, 40) + ")"
	}
	b += fmt.Sprintf(", %s O₂ at %s in %s", ptbr.Pct(*t.O2), ptbr.Psi(*t.P), ptbr.GLabel(t.G))
	if t.Flow != nil {
		b += fmt.Sprintf(", flow of %s cm/s", ptbr.Num(*t.Flow, 0))
	}
	b += "; outcome: " + ptbr.Outcome[t.Outcome]
	if t.Raw != nil && *t.Raw != "" {
		b += " (" + *t.Raw + ")"
	}
	if len(t.Assumed) > 0 {
		b += "; pressure assumed to be cabin pressure"
	}
	return b
}
