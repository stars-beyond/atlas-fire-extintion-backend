package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"atlas-api/domain/entities"
)

// ListTestsUseCase devolve todos os testes em formato compacto (colunas + linhas) para o território 3D.
type ListTestsUseCase struct {
	Catalog *entities.Catalog
}

func (uc *ListTestsUseCase) List() entities.CompactTests {
	cols := []string{"id", "inv", "label", "family", "fuel", "material", "o2", "p", "g", "flow", "co2", "he", "outcome", "assumed"}
	rows := make([][]any, 0, len(uc.Catalog.Tests))
	for _, t := range uc.Catalog.Tests {
		rows = append(rows, []any{t.ID, t.Inv, t.Label, t.Family, t.Fuel, t.Material, t.O2, t.P, t.G, t.Flow, t.CO2, t.He, t.Outcome, len(t.Assumed) > 0})
	}
	return entities.CompactTests{Columns: cols, Rows: rows}
}

// TestRawRepository lê as métricas e a linha original de um teste.
type TestRawRepository interface {
	TestRaw(ctx context.Context, id string) (metrics, raw json.RawMessage, err error)
}

// GetTestUseCase devolve um teste com a linha original da tabela do PSI.
type GetTestUseCase struct {
	Catalog *entities.Catalog
	Repo    TestRawRepository
}

func (uc *GetTestUseCase) Get(ctx context.Context, id string) (entities.TestDetail, error) {
	t := uc.Catalog.TestByID[id]
	if t == nil {
		if d, ok := externalDetail(uc.Catalog.External, id); ok {
			return d, nil
		}
		return entities.TestDetail{}, entities.ErrNotFound
	}
	metrics, raw, err := uc.Repo.TestRaw(ctx, id)
	if err != nil {
		return entities.TestDetail{}, err
	}
	return entities.TestDetail{Test: t, Investigation: uc.Catalog.InvName(t.Inv), Metrics: metrics, Raw: raw,
		Source: fmt.Sprintf("%s, row %d", t.File, t.Row)}, nil
}

// externalDetail monta a ficha de um ensaio de fora da NASA, com o registro de origem no lugar da linha do PSI.
func externalDetail(d *entities.ExternalDoc, id string) (entities.TestDetail, bool) {
	if d == nil {
		return entities.TestDetail{}, false
	}
	for i := range d.Tests {
		x := &d.Tests[i]
		if x.ID != id {
			continue
		}
		m := map[string]any{"external": true, "agency": d.Agency, "license": d.License, "sample": x.Sample, "flow_cm_s": x.Flow,
			"spread_mm_s": x.Spread, "also_in": x.AlsoIn, "not_nasa": d.NotNasa}
		src := x.Provenance
		if r := d.Record(x.Record); r != nil {
			m["record_title"], m["record_url"], m["record_authors"] = r.Title, r.URL, r.Authors
			src = r.Authors + ", " + r.ID + " · " + x.Provenance
		}
		metrics, _ := json.Marshal(m)
		return entities.TestDetail{Test: x.AsTest(d.ID), Investigation: d.Name + " (outside NASA)", Metrics: metrics, Raw: json.RawMessage("{}"), Source: src}, true
	}
	return entities.TestDetail{}, false
}

// ListExternalUseCase devolve os ensaios de fora da NASA, para o mapa e para a vista de limites.
type ListExternalUseCase struct {
	Catalog *entities.Catalog
}

func (uc *ListExternalUseCase) List() map[string]any {
	d := uc.Catalog.External
	if d == nil {
		return map[string]any{"available": false, "tests": []any{}}
	}
	rows := make([]map[string]any, 0, len(d.Tests))
	for i := range d.Tests {
		x := &d.Tests[i]
		rows = append(rows, map[string]any{"id": x.ID, "inv": d.ID, "label": x.Label, "family": "solido", "fuel": x.Fuel, "material": x.Material,
			"o2": x.O2, "p": x.P, "g": x.G, "flow": x.Flow, "co2": nil, "he": nil, "outcome": x.Outcome, "assumed": false,
			"external": true, "spread_mm_s": x.Spread, "sample": x.Sample, "outcome_raw": x.Raw})
	}
	return map[string]any{"available": true, "id": d.ID, "name": d.Name, "agency": d.Agency, "what": d.What, "not_nasa": d.NotNasa,
		"license": d.License, "records": d.Records, "summary": d.Summary, "tests": rows}
}
