// Package search é a busca híbrida no acervo: vetor + palavra no pgvector.
package search

import (
	"context"
	"log"
	"time"

	"atlas-api/domain/entities"
)

// Embedder gera o embedding de um texto.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// ChunkQuery é uma busca no acervo do Atlas: o vetor da pergunta, a consulta por palavra e os filtros.
type ChunkQuery struct {
	Vec   []float32
	FTS   string // termos para to_tsquery('english', ...), já em inglês
	Kinds []string
	Inv   string // filtra por investigação; vazio = todas
	K     int
}

// ChunkSearcher busca os trechos do Atlas (pgvector), fundindo vetor e palavra.
type ChunkSearcher interface {
	SearchChunks(ctx context.Context, q ChunkQuery) ([]entities.Hit, error)
}

// HybridSearchUseCase busca no acervo do Atlas (pgvector), fundindo vetor e palavra.
type HybridSearchUseCase struct {
	Embedder     Embedder
	Chunks       ChunkSearcher
	EmbedTimeout time.Duration // prazo do embedding da pergunta; zero = EmbedBudget
}

// EmbedBudget é o prazo do embedding da pergunta. Ele tem um contexto próprio, derivado do pedido: se o embedder
// travar, a busca por palavra ainda roda com o tempo que sobra, em vez de herdar um contexto já vencido.
const EmbedBudget = 5 * time.Second

// Search devolve os trechos e quantos vieram de cada fonte.
func (uc *HybridSearchUseCase) Search(ctx context.Context, q string, kinds []string, inv string, k int) ([]entities.Hit, map[string]int, error) {
	budget := uc.EmbedTimeout
	if budget <= 0 {
		budget = EmbedBudget
	}
	ectx, cancel := context.WithTimeout(ctx, budget)
	qvec, err := uc.Embedder.Embed(ectx, ToEnglish(q))
	cancel()
	if err != nil {
		// embedder fora do ar: segue só com a busca por palavra, em vez de falhar a busca inteira
		log.Printf("busca: embedder indisponível, só busca por palavra: %v", err)
		qvec = nil
	}
	pg, err := uc.Chunks.SearchChunks(ctx, ChunkQuery{Vec: qvec, FTS: FTSQuery(q), Kinds: kinds, Inv: inv, K: k})
	if err != nil {
		return nil, nil, err
	}
	return pg, map[string]int{"atlas": len(pg)}, nil
}
