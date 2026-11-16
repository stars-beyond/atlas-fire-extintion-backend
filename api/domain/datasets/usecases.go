package datasets

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"atlas-api/domain/entities"
)

// Note explica de onde vêm os valores numéricos.
const Note = "Numeric values come from the PSI experimental table. Empty or text-only cells are left missing; when a cell holds both a number and text (\"~ 22.1\", \"145 s\"), the first number is used."

// Store guarda os conjuntos já montados. O acervo não muda depois da subida, então cada conjunto (e os seus
// registros) é montado uma vez só, no primeiro pedido, e servido da memória daí em diante: antes, cada pedido
// refazia um Build por investigação. Os valores devolvidos são os mesmos para todos os pedidos: quem os recebe só lê.
type Store struct {
	cat  *entities.Catalog
	once sync.Once
	list []entities.Dataset // "todos" e depois os conjuntos com variável numérica, do maior para o menor
	byID map[string]built
}

type built struct {
	ds   entities.Dataset
	recs []entities.Record
}

// NewStore prepara o guarda dos conjuntos do acervo. Nada é montado até o primeiro pedido.
func NewStore(cat *entities.Catalog) *Store { return &Store{cat: cat} }

func (s *Store) load() {
	s.once.Do(func() {
		s.byID = map[string]built{}
		s.list = []entities.Dataset{}
		if ds, recs, ok := Build(s.cat, "todos"); ok {
			s.byID["todos"] = built{ds, recs}
			s.list = append(s.list, ds)
		}
		var rest []entities.Dataset
		for _, inv := range s.cat.Invs {
			ds, recs, ok := Build(s.cat, inv.ID)
			if !ok {
				continue
			}
			s.byID[inv.ID] = built{ds, recs}
			if len(ds.Numeric) >= 1 {
				rest = append(rest, ds)
			}
		}
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].Rows > rest[j].Rows })
		s.list = append(s.list, rest...)
	})
}

// List devolve "todos" e um conjunto por investigação com pelo menos uma variável numérica.
func (s *Store) List() []entities.Dataset {
	s.load()
	return s.list
}

// Get devolve o conjunto e os registros; false se o conjunto não existe ou está vazio.
func (s *Store) Get(id string) (entities.Dataset, []entities.Record, bool) {
	s.load()
	b, ok := s.byID[id]
	return b.ds, b.recs, ok
}

// ListDatasetsUseCase lista "todos" e um conjunto por investigação com pelo menos uma variável numérica.
type ListDatasetsUseCase struct {
	Store *Store
}

func (uc *ListDatasetsUseCase) List() []entities.Dataset { return uc.Store.List() }

// GetRecordsUseCase devolve o conjunto e todos os registros.
type GetRecordsUseCase struct {
	Store *Store
}

func (uc *GetRecordsUseCase) Get(id string) (entities.Dataset, []entities.Record, error) {
	ds, recs, ok := uc.Store.Get(id)
	if !ok {
		return ds, nil, entities.ErrNotFound
	}
	return ds, recs, nil
}

// ErrChartType marca um tipo de gráfico desconhecido.
var ErrChartType = fmt.Errorf("unknown chart type")

// BuildChartUseCase calcula o gráfico e as opções do filtro escolhido.
type BuildChartUseCase struct {
	Store *Store
}

// Exists diz se o conjunto existe (o handler responde 404 antes de olhar o corpo, como sempre fez).
func (uc *BuildChartUseCase) Exists(id string) bool {
	_, _, ok := uc.Store.Get(id)
	return ok
}

// Build devolve o gráfico e os valores distintos da variável de filtro.
func (uc *BuildChartUseCase) Build(id string, cfg entities.ChartConfig) (entities.BuiltChart, []string, error) {
	ds, recs, ok := uc.Store.Get(id)
	if !ok {
		return entities.BuiltChart{}, nil, entities.ErrNotFound
	}
	if cfg.Type != "scatter" && cfg.Type != "bar" && cfg.Type != "histogram" {
		return entities.BuiltChart{}, nil, ErrChartType
	}
	return BuildChart(ds, recs, cfg), categoryValues(recs, cfg.FilterKey), nil
}

// ExportCSVUseCase gera o CSV dos registros filtrados: origem (arquivo e linha do PSI) e todas as variáveis.
type ExportCSVUseCase struct {
	Store *Store
}

// Export devolve o id do conjunto (para o nome do arquivo) e as linhas, com o cabeçalho primeiro.
func (uc *ExportCSVUseCase) Export(id, filterKey, filterValue string) (string, [][]string, error) {
	ds, recs, ok := uc.Store.Get(id)
	if !ok {
		return "", nil, entities.ErrNotFound
	}
	rows := filterRecords(recs, filterKey, filterValue)
	head := []string{"source_file", "source_row", "test"}
	for _, v := range ds.Numeric {
		if v.Unit != "" {
			head = append(head, fmt.Sprintf("%s (%s)", v.Label, v.Unit))
		} else {
			head = append(head, v.Label)
		}
	}
	for _, v := range ds.Categorical {
		head = append(head, v.Label)
	}
	head = append(head, "assumed_fields")
	out := [][]string{head}
	for _, rec := range rows {
		line := []string{rec.File, strconv.Itoa(rec.Row), rec.Label}
		for _, v := range ds.Numeric {
			if x := rec.Num[v.Key]; x != nil {
				line = append(line, strconv.FormatFloat(*x, 'f', -1, 64))
			} else {
				line = append(line, "")
			}
		}
		for _, v := range ds.Categorical {
			if x := rec.Cat[v.Key]; x != nil {
				line = append(line, *x)
			} else {
				line = append(line, "")
			}
		}
		line = append(line, strings.Join(rec.Assume, " "))
		out = append(out, line)
	}
	return ds.ID, out, nil
}
