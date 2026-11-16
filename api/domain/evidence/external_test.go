package evidence

import (
	"testing"

	"atlas-api/domain/entities"
	"atlas-api/gateways/externalfile"
)

// Os ensaios do ZARM aparecem ao lado do veredito, sem mudar o nível de evidência da NASA.
func TestExternalSeed(t *testing.T) {
	d, err := externalfile.Load("../../../data/seed/external/zarm.json")
	if err != nil || d == nil {
		t.Skipf("sem data/seed/external/zarm.json: %v", err)
	}
	cat := &entities.Catalog{InvByID: map[string]*entities.Investigation{}, External: d}
	lua := entities.Scenario{O2: 0.30, P: 0.694, G: 0.165, Family: "solido", Material: "pmma"}
	v := Verdict(cat, lua, nil, 8)
	if v.Tier != "sem_evidencia" {
		t.Fatalf("sem testes da NASA o nível tem de ser sem_evidencia, veio %s", v.Tier)
	}
	e := External(cat, lua, &v)
	if e == nil || e.N == 0 || e.TierWith != "parcial" || e.Spread == nil {
		t.Fatalf("base lunar devia ter ensaios do ZARM por perto: %+v", e)
	}
	t.Logf("%s %s | %s", e.Headline, e.Detail, e.Caveat)
	// outro material: nada
	lua.Material = "sibal"
	if e := External(cat, lua, &v); e != nil {
		t.Fatalf("algodão não tem ensaio do ZARM: %+v", e)
	}
	// traje (O₂ puro): nada por perto
	if e := External(cat, entities.Scenario{O2: 1, P: 0.2925, Family: "solido", Material: "pmma"}, &v); e != nil {
		t.Fatalf("traje: %+v", e)
	}
	// estação em 0 g: contaria como medido
	st := entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "pmma"}
	vs := Verdict(cat, st, nil, 8)
	if e := External(cat, st, &vs); e == nil || e.TierWith != "medido" {
		t.Fatalf("estação: %+v", e)
	}
}
