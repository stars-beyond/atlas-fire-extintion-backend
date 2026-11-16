// Package catalog tem os handlers do acervo: saúde, meta, investigações e testes.
package catalog

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"atlas-api/domain/entities"
	"atlas-api/extensions/zweb"
)

type MetaUseCase interface {
	Get(ctx context.Context) map[string]any
}

type ListInvestigationsUseCase interface {
	List() []*entities.Investigation
}

type GetInvestigationUseCase interface {
	Get(ctx context.Context, id string) (entities.InvestigationDetail, error)
}

type ListTestsUseCase interface {
	List() entities.CompactTests
}

type GetTestUseCase interface {
	Get(ctx context.Context, id string) (entities.TestDetail, error)
}

type ListExternalUseCase interface {
	List() map[string]any
}

// detailTimeout é o prazo das consultas de uma ficha (investigação ou teste): leituras por chave, que em condições
// normais levam milissegundos. Sem ele, um banco travado prende o pedido até o cliente desistir.
const detailTimeout = 10 * time.Second

// O que o cliente lê quando o banco não responde. A causa fica só no log.
const dbDown = "the archive database did not answer; try again in a few seconds"

// HealthHandler: GET /api/health
func HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { zweb.JSON(w, 200, map[string]any{"ok": true}) }
}

// MetaHandler: GET /api/meta — contagens, serviços, habitats, tolerâncias e pesos.
func MetaHandler(uc MetaUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { zweb.JSON(w, 200, uc.Get(r.Context())) }
}

// InvestigationsHandler: GET /api/investigations
func InvestigationsHandler(uc ListInvestigationsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { zweb.JSON(w, 200, uc.List()) }
}

// InvestigationHandler: GET /api/investigations/{id}
func InvestigationHandler(uc GetInvestigationUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), detailTimeout)
		defer cancel()
		d, err := uc.Get(ctx, r.PathValue("id"))
		if errors.Is(err, entities.ErrNotFound) {
			zweb.Error(w, 404, "investigation not found")
			return
		}
		if err != nil {
			log.Printf("investigation: %v", err) // a causa (erro do banco) fica no log
			zweb.Error(w, 503, dbDown)
			return
		}
		zweb.JSON(w, 200, d)
	}
}

// TestsHandler: GET /api/tests — todos os testes em colunas + linhas.
func TestsHandler(uc ListTestsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { zweb.JSON(w, 200, uc.List()) }
}

// TestHandler: GET /api/tests/{id}
func TestHandler(uc GetTestUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), detailTimeout)
		defer cancel()
		d, err := uc.Get(ctx, r.PathValue("id"))
		if errors.Is(err, entities.ErrNotFound) {
			zweb.Error(w, 404, "test not found")
			return
		}
		if err != nil {
			log.Printf("test: %v", err) // a causa (erro do banco) fica no log
			zweb.Error(w, 503, dbDown)
			return
		}
		zweb.JSON(w, 200, d)
	}
}

// ExternalHandler: GET /api/external — ensaios de fora da NASA (torre de queda do ZARM), sempre rotulados.
func ExternalHandler(uc ListExternalUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { zweb.JSON(w, 200, uc.List()) }
}
