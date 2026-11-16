package flammability

import (
	"os"
	"testing"

	"atlas-api/domain/predict"
)

type seedFile string

func (f seedFile) LoadSpread() ([]byte, error) { return os.ReadFile(string(f)) }

// A fila do modelo, com a tabela de verdade: sem número vem antes de extrapolado.
func TestModelQueueSeed(t *testing.T) {
	tab, err := predict.LoadTable(seedFile("../../../data/seed/sim/spread.json"))
	if err != nil {
		t.Skip("sem data/seed/sim/spread.json")
	}
	q := ModelQueue(tab)
	if len(q) != 5 {
		t.Fatalf("esperava 5 fichas, vieram %d", len(q))
	}
	for i, p := range q {
		if p.Rank != i+1 || p.Kind != "modelo" || p.Prediction == "" || p.Decides == "" {
			t.Fatalf("ficha %d incompleta: %+v", i, p)
		}
		if i > 0 && p.Score > q[i-1].Score {
			t.Fatalf("fora de ordem em %d", i)
		}
		t.Logf("%d. [%v] %s — %s", p.Rank, p.Score, p.Title, p.Prediction)
	}
	if ModelQueue(nil) != nil {
		t.Fatal("sem tabela não há fila")
	}
}
