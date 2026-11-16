// Package evidence tem os handlers do nível de evidência: veredito, ranking, próximos testes, grafo e achados.
package evidence

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
	"unicode/utf8"

	"atlas-api/domain/entities"
	"atlas-api/extensions/zweb"
)

const maxQuestion = 2000 // caracteres da pergunta que acompanha o ranking

type VerdictUseCase interface {
	Get(s entities.Scenario) (entities.Verdict, []entities.MatrixCell)
}

type RankUseCase interface {
	Rank(ctx context.Context, s entities.Scenario, question string) ([]entities.Ranked, string, error)
}

type NextTestsUseCase interface {
	Next(family, material string) []entities.NextTest
}

type GraphUseCase interface {
	Build(s entities.Scenario) ([]entities.GNode, []entities.GEdge)
}

type InsightsUseCase interface {
	List(ctx context.Context) ([]entities.Insight, error)
}

// insightsTimeout é o prazo das leituras do banco no primeiro cálculo dos achados (depois ele fica guardado).
const insightsTimeout = 10 * time.Second

// VerdictHandler: POST /api/verdict — corpo: o cenário.
func VerdictHandler(uc VerdictUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var s entities.Scenario
		if err := zweb.DecodeJSON(r, &s); err != nil {
			zweb.Error(w, 400, "invalid scenario")
			return
		}
		v, matrix := uc.Get(s)
		zweb.JSON(w, 200, map[string]any{"verdict": v, "matrix": matrix})
	}
}

// RankHandler: POST /api/rank — corpo: {scenario, question}.
func RankHandler(uc RankUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Scenario entities.Scenario `json:"scenario"`
			Question string            `json:"question"`
		}
		if err := zweb.DecodeJSON(r, &in); err != nil {
			zweb.Error(w, 400, "invalid input")
			return
		}
		// a pergunta vai para o serviço de embeddings: mesmo limite de POST /api/ask
		if utf8.RuneCountInString(in.Question) > maxQuestion {
			zweb.Error(w, 400, fmt.Sprintf("the question is too long (at most %d characters)", maxQuestion))
			return
		}
		ranking, q, err := uc.Rank(r.Context(), in.Scenario, in.Question)
		if err != nil {
			log.Printf("rank: %v", err)
			zweb.Error(w, 502, "embedding service unavailable")
			return
		}
		zweb.JSON(w, 200, map[string]any{"ranking": ranking, "query": q})
	}
}

// NextTestsHandler: POST /api/next-tests — corpo: {family, material}.
func NextTestsHandler(uc NextTestsUseCase, method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Family   string `json:"family"`
			Material string `json:"material"`
		}
		_ = zweb.DecodeJSON(r, &in)
		zweb.JSON(w, 200, map[string]any{"next": uc.Next(in.Family, in.Material), "method": method})
	}
}

// GraphHandler: POST /api/graph — corpo: o cenário.
func GraphHandler(uc GraphUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var s entities.Scenario
		if err := zweb.DecodeJSON(r, &s); err != nil {
			zweb.Error(w, 400, "invalid scenario")
			return
		}
		nodes, edges := uc.Build(s)
		zweb.JSON(w, 200, map[string]any{"nodes": nodes, "edges": edges})
	}
}

// InsightsHandler: GET /api/insights
func InsightsHandler(uc InsightsUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), insightsTimeout)
		defer cancel()
		insights, err := uc.List(ctx)
		if err != nil {
			// banco fora do ar: melhor dizer isso do que publicar achados com números zerados. A causa fica no log.
			log.Printf("insights: %v", err)
			zweb.Error(w, 503, "the archive database did not answer; try again in a few seconds")
			return
		}
		zweb.JSON(w, 200, map[string]any{"insights": insights})
	}
}
