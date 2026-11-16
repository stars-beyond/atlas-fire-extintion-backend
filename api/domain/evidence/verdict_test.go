package evidence

import (
	"fmt"
	"strings"
	"testing"

	"atlas-api/domain/entities"
)

func testAt(id string, o2, p, g float64, outcome string) *entities.Test {
	return &entities.Test{ID: id, Inv: "X", Label: id, Family: "solido", O2: &o2, P: &p, G: g, Outcome: outcome}
}

// acervo de teste: 10 testes de 0 g exatamente em 21% / 1 atm e 2 de 1 g um pouco ao lado, na mesma atmosfera
func smallCatalog() *entities.Catalog {
	cat := &entities.Catalog{InvByID: map[string]*entities.Investigation{"X": {ID: "X", Name: "X"}}}
	for i := 0; i < 10; i++ {
		cat.Tests = append(cat.Tests, testAt(fmt.Sprintf("z%d", i), 0.21, 1, 0, "queimou"))
	}
	cat.Tests = append(cat.Tests, testAt("g1", 0.209, 1, 1, "queimou"), testAt("g2", 0.209, 1, 1, "extinguiu"))
	cat.Invs = []*entities.Investigation{cat.InvByID["X"]}
	return cat
}

// Os vizinhos mostrados são os que decidem o veredito: num cenário de 1 g, os testes de 1 g vêm antes
// dos de 0 g, mesmo que estes estejam mais perto na atmosfera.
func TestNearestPutsSameGravityFirst(t *testing.T) {
	v := Verdict(smallCatalog(), entities.Scenario{O2: 0.21, P: 1, G: 1, Family: "solido"}, nil, 4)
	if v.Tier != "medido" || v.SameG != 2 {
		t.Fatalf("veredito: %s, same_g %d", v.Tier, v.SameG)
	}
	if v.Nearest[0].Test.G != 1 || v.Nearest[1].Test.G != 1 {
		t.Fatalf("os dois testes de 1 g deviam abrir a lista: %s, %s", v.Nearest[0].Test.ID, v.Nearest[1].Test.ID)
	}
	if v.Empirical == nil || !strings.Contains(v.Empirical.Headline, "1 of 2") || strings.Contains(v.Empirical.Headline, "completion") {
		t.Fatalf("taxa empírica: %+v", v.Empirical)
	}
}

// Em gravidade parcial ("delimitado") a lista curta mostra os dois lados, 0 g e 1 g.
func TestNearestShowsBothSidesWhenBounded(t *testing.T) {
	v := Verdict(smallCatalog(), entities.Scenario{O2: 0.21, P: 1, G: 0.165, Family: "solido"}, nil, 4)
	if v.Tier != "delimitado" {
		t.Fatalf("veredito: %s", v.Tier)
	}
	zero, one := 0, 0
	for _, n := range v.Nearest {
		if n.Test.G == 0 {
			zero++
		} else {
			one++
		}
	}
	if zero != 2 || one != 2 {
		t.Fatalf("esperava 2 de cada lado, veio %d de 0 g e %d de 1 g", zero, one)
	}
}

// A matriz conta cada teste da atmosfera uma vez só (antes somava mesma gravidade + 0 g + 1 g).
func TestMatrixCountsEachTestOnce(t *testing.T) {
	uc := &GetVerdictUseCase{Catalog: smallCatalog()}
	_, matrix := uc.Get(entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido"})
	if matrix[0].Family != "solido" || matrix[0].N != 12 {
		t.Fatalf("matriz: %+v", matrix[0])
	}
}

// O ranking só diz "tem teste nesta atmosfera" quando o veredito também diria.
func TestRankAgreesWithVerdict(t *testing.T) {
	cat := smallCatalog()
	s := entities.Scenario{O2: 0.237, P: 1, G: 0, Family: "solido"} // 2,7 pontos acima: fora da tolerância de 2
	if v := Verdict(cat, s, nil, 1); v.Tier != "sem_evidencia" {
		t.Fatalf("veredito: %s", v.Tier)
	}
	if r := Rank(cat, s, nil); strings.Contains(r[0].Reason, "in this atmosphere") {
		t.Fatalf("ranking contradiz o veredito: %s", r[0].Reason)
	}
	s.O2 = 0.225
	if r := Rank(cat, s, nil); !strings.Contains(r[0].Reason, "in this atmosphere") {
		t.Fatalf("dentro da tolerância o ranking devia dizer que há teste: %s", r[0].Reason)
	}
}
