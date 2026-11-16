// Package ask tem os handlers de linguagem natural: perguntas (com e sem streaming), busca e inferência de cenário.
package ask

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	domainask "atlas-api/domain/ask"
	"atlas-api/domain/entities"
	"atlas-api/extensions/zweb"
)

type AskUseCase interface {
	Ask(ctx context.Context, in entities.AskIn) (entities.AskOut, error)
	Stream(ctx context.Context, in entities.AskIn, emit domainask.Emitter)
}

type SearchUseCase interface {
	Search(ctx context.Context, q string, kinds []string, inv string, k int) ([]entities.Hit, map[string]int, error)
}

type InferUseCase interface {
	Describe(ctx context.Context, text string, cur *entities.Scenario) domainask.InferResult
}

// Cada pergunta vira chamadas pagas (Claude, OpenAI, RAG): textos longos demais são recusados antes disso.
const (
	maxQuestion     = 2000 // caracteres de uma pergunta ou descrição de ambiente
	maxConversation = 128  // id de conversa do RAG
	maxSearch       = 500  // caracteres de uma busca
)

// O que o cliente lê quando a busca ou o banco falham. A causa (erro do banco, endereço interno) fica só no log.
const unavailable = "The answer service is unavailable right now. Try again in a moment."

// searchTimeout é o prazo de uma busca: o embedding da pergunta (até 5 s, domain/search) e a consulta no pgvector
// (que o próprio Postgres corta em 15 s, extensions/zpostgres).
const searchTimeout = 20 * time.Second

func tooLong(s string, n int) bool { return utf8.RuneCountInString(s) > n }

func decodeQuestion(w http.ResponseWriter, r *http.Request) (entities.AskIn, bool) {
	var in entities.AskIn
	if err := zweb.DecodeJSON(r, &in); err != nil || strings.TrimSpace(in.Question) == "" {
		zweb.Error(w, 400, "write a question")
		return in, false
	}
	if tooLong(in.Question, maxQuestion) {
		zweb.Error(w, 400, fmt.Sprintf("the question is too long (at most %d characters)", maxQuestion))
		return in, false
	}
	if tooLong(in.Conversation, maxConversation) {
		zweb.Error(w, 400, "invalid conversation_id")
		return in, false
	}
	return in, true
}

// AskHandler: POST /api/ask — corpo: {question, scenario, conversation_id}.
func AskHandler(uc AskUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeQuestion(w, r)
		if !ok {
			return
		}
		// a resposta precisa sair antes dos 100 s em que o Cloudflare corta (com uma página HTML 524). Dentro
		// destes 90 s cada etapa tem o seu prazo (domain/ask, Budgets), para o caminho local nunca ficar sem tempo.
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		out, err := uc.Ask(ctx, in)
		if err != nil {
			log.Printf("ask: %v", err)
			zweb.Error(w, 502, unavailable)
			return
		}
		zweb.JSON(w, 200, out)
	}
}

// AskStreamHandler: POST /api/ask/stream — a mesma pergunta em Server-Sent Events (ver domain/ask/ask-stream.go).
func AskStreamHandler(uc AskUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeQuestion(w, r)
		if !ok {
			return
		}
		sse, ok := zweb.NewSSE(w)
		if !ok {
			zweb.Error(w, 500, "streaming is not available on this server")
			return
		}
		stop := sse.KeepAlive(r.Context(), 15*time.Second)
		defer stop()
		uc.Stream(r.Context(), in, sse)
	}
}

// SearchHandler: POST /api/search — corpo: {query, k, investigation, kinds}.
func SearchHandler(uc SearchUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Query string   `json:"query"`
			K     int      `json:"k"`
			Inv   string   `json:"investigation"`
			Kinds []string `json:"kinds"`
		}
		if err := zweb.DecodeJSON(r, &in); err != nil || strings.TrimSpace(in.Query) == "" {
			zweb.Error(w, 400, "provide a query")
			return
		}
		if tooLong(in.Query, maxSearch) {
			zweb.Error(w, 400, fmt.Sprintf("the query is too long (at most %d characters)", maxSearch))
			return
		}
		if in.K <= 0 || in.K > 30 {
			in.K = 8
		}
		if len(in.Kinds) == 0 {
			in.Kinds = []string{"catalogo", "documento", "recurso", "teste"}
		}
		ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
		defer cancel()
		hits, src, err := uc.Search(ctx, in.Query, in.Kinds, in.Inv, in.K)
		if err != nil {
			log.Printf("search: %v", err)
			zweb.Error(w, 502, unavailable)
			return
		}
		if hits == nil {
			hits = []entities.Hit{}
		}
		zweb.JSON(w, 200, map[string]any{"hits": hits, "sources": src})
	}
}

// InferHandler: POST /api/scenario/infer — corpo: {text, scenario}.
func InferHandler(uc InferUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text     string             `json:"text"`
			Scenario *entities.Scenario `json:"scenario"`
		}
		if err := zweb.DecodeJSON(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
			zweb.Error(w, 400, "describe the environment")
			return
		}
		if tooLong(in.Text, maxQuestion) {
			zweb.Error(w, 400, fmt.Sprintf("the description is too long (at most %d characters)", maxQuestion))
			return
		}
		zweb.JSON(w, 200, uc.Describe(r.Context(), in.Text, in.Scenario))
	}
}
