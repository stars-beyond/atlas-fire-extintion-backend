package evidence

import (
	"encoding/json"
	"strings"
	"testing"

	"atlas-api/domain/entities"
)

func fakeCatalog(changes, ingest string) *entities.Catalog {
	inv := &entities.Investigation{ID: "PSI-180", Acronym: "Saffire-VII", Name: "Saffire-VII", Title: "Spacecraft Fire Safety VII",
		Stats: json.RawMessage(`{"n_tests":12,"n_plot":10,"o2":[0.17,0.3],"pressure_atm":[0.5,1],"outcomes":{"queimou":9,"extinguiu":3},
		"varies":["Air Flow"],"fuels":["SIBAL"]}`)}
	return &entities.Catalog{InvByID: map[string]*entities.Investigation{"PSI-180": inv, "PSI-99": {ID: "PSI-99", Name: "SAFFIRE-II"}},
		Changes: json.RawMessage(changes), Ingest: json.RawMessage(ingest)}
}

func TestNewsInsight(t *testing.T) {
	cat := fakeCatalog(`{"since":"2026-11-14T04:00:00+00:00","new_investigations":[{"id":"PSI-180","name":"Saffire-VII","title":"x"}],
		"new_tests":{"PSI-180":12},"new_documents":[{"inv":"PSI-99","name":"SAFFIRE-II","file":"a.pdf"},{"inv":"PSI-99","name":"SAFFIRE-II","file":"b.pdf"}]}`, `{}`)
	n := NewsInsight(cat)
	if n == nil {
		t.Fatal("esperava o cartão de novidades")
	}
	for _, want := range []string{"1 new investigation: Saffire-VII (PSI-180)", "12 new tests (12 in Saffire-VII)", "2 new documents (2 from SAFFIRE-II)", "14/11/2026"} {
		if !strings.Contains(n.Finding+n.Source, want) {
			t.Errorf("faltou %q em %q", want, n.Finding+" | "+n.Source)
		}
	}
	if NewsInsight(fakeCatalog(`{"since":null}`, `{}`)) != nil {
		t.Error("primeira carga não deveria ter cartão de novidades")
	}
	if NewsInsight(fakeCatalog(`{"since":"2026-11-14","new_investigations":[],"new_tests":{},"new_documents":[]}`, `{}`)) != nil {
		t.Error("sem nada novo não deveria ter cartão")
	}
}

func TestAutoInvInsights(t *testing.T) {
	cards := AutoInvInsights(fakeCatalog(`{}`, `{"investigations_without_profile":["PSI-180"]}`))
	if len(cards) != 1 || cards[0].ID != "auto_PSI-180" {
		t.Fatalf("esperava um cartão auto_PSI-180, veio %+v", cards)
	}
	for _, want := range []string{"12 tests (10 with O₂ and pressure", "O₂ from 17% to 30%", "pressure from 0.50 to 1.00 atm", "3 went out", "9 burned", "Air Flow"} {
		if !strings.Contains(cards[0].Finding, want) {
			t.Errorf("faltou %q em %q", want, cards[0].Finding)
		}
	}
}
