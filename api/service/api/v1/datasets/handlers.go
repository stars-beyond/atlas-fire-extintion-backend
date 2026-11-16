// Package datasets tem os handlers do construtor de gráficos: conjuntos, registros, gráfico e CSV.
package datasets

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"

	"atlas-api/domain/entities"
	"atlas-api/extensions/zweb"
)

type ListUseCase interface {
	List() []entities.Dataset
}

type RecordsUseCase interface {
	Get(id string) (entities.Dataset, []entities.Record, error)
}

type ChartUseCase interface {
	Exists(id string) bool
	Build(id string, cfg entities.ChartConfig) (entities.BuiltChart, []string, error)
}

type CSVUseCase interface {
	Export(id, filterKey, filterValue string) (string, [][]string, error)
}

const notFound = "dataset not found"

// DatasetsHandler: GET /api/datasets
func DatasetsHandler(uc ListUseCase, note string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zweb.JSON(w, 200, map[string]any{"datasets": uc.List(), "note": note})
	}
}

// RecordsHandler: GET /api/datasets/{id}/records
func RecordsHandler(uc RecordsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ds, recs, err := uc.Get(r.PathValue("id"))
		if err != nil {
			zweb.Error(w, 404, notFound)
			return
		}
		zweb.JSON(w, 200, map[string]any{"dataset": ds, "records": recs})
	}
}

// ChartHandler: POST /api/datasets/{id}/chart — corpo: a configuração do gráfico.
func ChartHandler(uc ChartUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !uc.Exists(id) {
			zweb.Error(w, 404, notFound)
			return
		}
		var cfg entities.ChartConfig
		if err := zweb.DecodeJSON(r, &cfg); err != nil {
			zweb.Error(w, 400, "invalid chart configuration")
			return
		}
		chart, opts, err := uc.Build(id, cfg)
		switch {
		case errors.Is(err, entities.ErrNotFound):
			zweb.Error(w, 404, notFound)
			return
		case err != nil:
			zweb.Error(w, 400, "unknown chart type: "+cfg.Type)
			return
		}
		zweb.JSON(w, 200, map[string]any{"chart": chart, "filter_options": opts})
	}
}

// CSVHandler: GET /api/datasets/{id}/csv?filter_key=&filter_value=
func CSVHandler(uc CSVUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		name, rows, err := uc.Export(r.PathValue("id"), q.Get("filter_key"), q.Get("filter_value"))
		if err != nil {
			zweb.Error(w, 404, notFound)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="atlas-%s.csv"`, name))
		cw := csv.NewWriter(w)
		for _, row := range rows {
			_ = cw.Write(row)
		}
		cw.Flush()
	}
}
