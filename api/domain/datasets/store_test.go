package datasets

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"atlas-api/domain/entities"
)

// storeCatalog tem três investigações: A (4 testes, com métrica própria), B (2 testes) e C (sem testes).
func storeCatalog() *entities.Catalog {
	cat := &entities.Catalog{InvByID: map[string]*entities.Investigation{}}
	for _, id := range []string{"B", "A", "C"} { // B antes de A no acervo: a lista sai por tamanho, não por esta ordem
		inv := &entities.Investigation{ID: id, Name: "Inv " + id}
		cat.Invs = append(cat.Invs, inv)
		cat.InvByID[id] = inv
	}
	add := func(id, inv string, o2, p float64, fuel string, k float64) {
		cat.Tests = append(cat.Tests, &entities.Test{ID: id, Inv: inv, Label: id, Family: "gota", O2: &o2, P: &p, Fuel: &fuel,
			Outcome: "queimou", File: inv + ".csv", Row: len(cat.Tests) + 2, Metrics: map[string]any{"k_mm2_s": k, "pi": "Dr. " + fuel}})
	}
	add("a1", "A", 0.21, 1, "Heptane", 0.5)
	add("a2", "A", 0.30, 1, "Heptane", 0.7)
	add("a3", "A", 0.25, 0.7, "Methanol", 0.6)
	add("a4", "A", 0.18, 1, "Methanol", 0.4)
	add("b1", "B", 0.21, 1, "Propane", 1)
	add("b2", "B", 0.34, 0.56, "Propane", 2)
	return cat
}

func TestStoreListAndGet(t *testing.T) {
	cat := storeCatalog()
	s := NewStore(cat)
	list := s.List()
	var ids []string
	for _, d := range list {
		ids = append(ids, d.ID)
	}
	if !reflect.DeepEqual(ids, []string{"todos", "A", "B"}) { // "todos" primeiro, depois do maior para o menor; C não tem testes
		t.Fatalf("lista = %v", ids)
	}
	// o que está guardado é exatamente o que Build monta
	for _, id := range []string{"todos", "A", "B"} {
		wantDS, wantRecs, _ := Build(cat, id)
		ds, recs, ok := s.Get(id)
		if !ok || !reflect.DeepEqual(ds, wantDS) || !reflect.DeepEqual(recs, wantRecs) {
			t.Errorf("conjunto %s difere do que Build monta", id)
		}
	}
	for _, id := range []string{"C", "nope", "", "TODOS"} {
		if _, recs, ok := s.Get(id); ok || recs != nil {
			t.Errorf("conjunto %q não existe", id)
		}
	}
}

// O conjunto é montado uma vez: as chamadas seguintes devolvem os mesmos registros, sem refazer nada.
func TestStoreBuildsOnce(t *testing.T) {
	s := NewStore(storeCatalog())
	_, r1, _ := s.Get("A")
	_, r2, _ := s.Get("A")
	if &r1[0] != &r2[0] {
		t.Error("os registros foram montados de novo")
	}
	l1, l2 := s.List(), s.List()
	if &l1[0] != &l2[0] {
		t.Error("a lista foi montada de novo")
	}
}

func TestStoreEmptyCatalog(t *testing.T) {
	s := NewStore(&entities.Catalog{InvByID: map[string]*entities.Investigation{}})
	if list := s.List(); list == nil || len(list) != 0 { // [] e não null no JSON
		t.Fatalf("lista = %v", list)
	}
	if _, _, ok := s.Get("todos"); ok {
		t.Error("acervo vazio não tem o conjunto todos")
	}
}

func TestStoreConcurrent(t *testing.T) {
	s := NewStore(storeCatalog())
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				if n := len(s.List()); n != 3 {
					t.Errorf("lista com %d conjuntos", n)
				}
				return
			}
			if _, recs, ok := s.Get("A"); !ok || len(recs) != 4 {
				t.Errorf("conjunto A: %d registros, ok %v", len(recs), ok)
			}
		}(i)
	}
	wg.Wait()
}

func TestUseCases(t *testing.T) {
	s := NewStore(storeCatalog())
	if list := (&ListDatasetsUseCase{Store: s}).List(); len(list) != 3 {
		t.Errorf("lista = %d", len(list))
	}

	rec := &GetRecordsUseCase{Store: s}
	if ds, recs, err := rec.Get("B"); err != nil || ds.Rows != 2 || len(recs) != 2 || ds.PSI != "B" {
		t.Errorf("registros de B: %+v, %d, %v", ds, len(recs), err)
	}
	if _, recs, err := rec.Get("nope"); !errors.Is(err, entities.ErrNotFound) || recs != nil {
		t.Errorf("conjunto inexistente: %v", err)
	}

	chart := &BuildChartUseCase{Store: s}
	if !chart.Exists("A") || chart.Exists("C") || chart.Exists("nope") {
		t.Error("Exists")
	}
	c, opts, err := chart.Build("A", entities.ChartConfig{Type: "scatter", X: "o2_pct", Y: "k_mm2_s", Group: "fuel", FilterKey: "fuel"})
	if err != nil || c.Kind != "scatter" || c.Used != 4 || len(c.Series) != 2 || !reflect.DeepEqual(opts, []string{"Heptane", "Methanol"}) {
		t.Errorf("gráfico: %+v, opções %v, %v", c, opts, err)
	}
	if _, _, err := chart.Build("A", entities.ChartConfig{Type: "pie"}); !errors.Is(err, ErrChartType) {
		t.Errorf("tipo desconhecido: %v", err)
	}
	if _, _, err := chart.Build("nope", entities.ChartConfig{Type: "bar"}); !errors.Is(err, entities.ErrNotFound) {
		t.Errorf("conjunto inexistente: %v", err)
	}
	// o filtro de um pedido não mexe nos registros guardados
	if _, recs, _ := s.Get("A"); len(recs) != 4 {
		t.Fatalf("os registros guardados foram alterados: %d", len(recs))
	}

	csv := &ExportCSVUseCase{Store: s}
	name, rows, err := csv.Export("A", "fuel", "Heptane")
	if err != nil || name != "A" || len(rows) != 3 {
		t.Fatalf("csv: %s, %d linhas, %v", name, len(rows), err)
	}
	head := rows[0]
	if head[0] != "source_file" || head[1] != "source_row" || head[2] != "test" || head[len(head)-1] != "assumed_fields" {
		t.Errorf("cabeçalho = %v", head)
	}
	for _, r := range rows[1:] {
		if len(r) != len(head) || r[0] != "A.csv" {
			t.Errorf("linha = %v", r)
		}
	}
	if _, all, _ := csv.Export("A", "", ""); len(all) != 5 {
		t.Errorf("sem filtro: %d linhas, want 5", len(all))
	}
	if _, _, err := csv.Export("nope", "", ""); !errors.Is(err, entities.ErrNotFound) {
		t.Errorf("conjunto inexistente: %v", err)
	}
}
