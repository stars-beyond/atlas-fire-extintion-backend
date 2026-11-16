// Package catalog responde sobre o acervo: o resumo (meta), as investigações e os testes.
package catalog

import (
	"context"

	"atlas-api/domain/entities"
	"atlas-api/domain/evidence"
)

// LanguageModel é o que o meta conta do modelo de linguagem em uso.
type LanguageModel interface {
	Name() string
	Model() string
}

// RagStatusReader consulta o estado do RAG da equipe.
type RagStatusReader interface {
	Status(ctx context.Context) entities.RagStatus
}

// GetMetaUseCase resume o acervo e os serviços para a interface.
type GetMetaUseCase struct {
	Catalog *entities.Catalog
	LLM     LanguageModel
	Rag     RagStatusReader
}

func (uc *GetMetaUseCase) Get(ctx context.Context) map[string]any {
	st := uc.Catalog
	models := []map[string]any{}
	for id, m := range st.Models {
		models = append(models, map[string]any{"id": id, "label": m.Label, "n": m.N, "r2_cv": m.R2, "range": m.Range, "fuel": m.Fuel})
	}
	return map[string]any{
		"ingest": st.Ingest,
		"counts": map[string]int{"investigations": len(st.Invs), "tests": len(st.Tests), "plot": st.Plot, "chunks": st.Chunks},
		"llm":    map[string]string{"provider": uc.LLM.Name(), "model": uc.LLM.Model()},
		// o Qdrant saiu: tudo fica no pgvector. A chave continua para fronts antigos que ainda a leem.
		"qdrant": map[string]any{"enabled": false}, "habitats": entities.Habitats, "models": models, "rag": uc.Rag.Status(ctx),
		"tolerances": map[string]float64{"o2": entities.TolO2, "pressure": entities.TolP, "gravity": entities.TolG},
		"weights":    map[string]float64{"proximidade": evidence.WProx, "relevancia": evidence.WRel, "dados": evidence.WData, "enunciado": evidence.WPrio},
	}
}
