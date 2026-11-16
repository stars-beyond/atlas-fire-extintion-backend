package flammability

import (
	"sort"

	"atlas-api/domain/entities"
)

// GetFlammabilityUseCase devolve os testes com desfecho de uma investigação (por omissão, o BASS-II),
// a contagem por forma da amostra, a lista de divergências entre a tabela do PSI e o relatório e as curvas.
type GetFlammabilityUseCase struct {
	Catalog *entities.Catalog
}

func (uc *GetFlammabilityUseCase) Get(acc string) map[string]any {
	if acc == "" {
		acc = BassII
	}
	d := uc.Catalog.Outcomes[acc]
	if d == nil {
		return map[string]any{"available": false, "investigation": acc}
	}
	forms := map[string]int{}
	for _, t := range d.Tests {
		if string(t.Points) != "[]" && t.Points != nil {
			forms[t.Form]++
		}
	}
	list := make([]entities.FormCount, 0, len(forms))
	for f, n := range forms {
		list = append(list, entities.FormCount{Form: f, N: n})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].N > list[j].N || list[i].N == list[j].N && list[i].Form < list[j].Form
	})
	name := acc
	if inv := uc.Catalog.InvByID[acc]; inv != nil {
		name = inv.Name
	}
	bounds := make([]entities.Boundary, len(d.Boundaries))
	for i := range d.Boundaries {
		bounds[i] = d.Boundaries[i]
		bounds[i].Curve = bounds[i].SampledCurve()
	}
	return map[string]any{"available": true, "investigation": acc, "name": name, "report": d.Report, "method": d.Method,
		"summary": d.Summary, "forms": list, "tests": d.Tests, "audit": d.Audit, "boundaries": bounds}
}
