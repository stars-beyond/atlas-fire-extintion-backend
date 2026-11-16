package catalog

import (
	"context"

	"atlas-api/domain/entities"
)

// ListInvestigationsUseCase lista as investigações do acervo, na ordem de prioridade.
type ListInvestigationsUseCase struct {
	Catalog *entities.Catalog
}

func (uc *ListInvestigationsUseCase) List() []*entities.Investigation {
	if uc.Catalog.Invs == nil {
		return []*entities.Investigation{} // [] e não null: o front faz .map na lista
	}
	return uc.Catalog.Invs
}

// InvestigationDetailRepository lê do banco o texto da investigação, as publicações e os documentos.
type InvestigationDetailRepository interface {
	InvestigationDetail(ctx context.Context, id string) (entities.InvestigationDetail, error)
}

// GetInvestigationUseCase monta a ficha de uma investigação.
type GetInvestigationUseCase struct {
	Catalog *entities.Catalog
	Repo    InvestigationDetailRepository
}

func (uc *GetInvestigationUseCase) Get(ctx context.Context, id string) (entities.InvestigationDetail, error) {
	inv := uc.Catalog.InvByID[id]
	if inv == nil {
		return entities.InvestigationDetail{}, entities.ErrNotFound
	}
	d, err := uc.Repo.InvestigationDetail(ctx, id)
	if err != nil {
		return entities.InvestigationDetail{}, err
	}
	d.Investigation = inv
	if d.Publications == nil {
		d.Publications = []entities.Publication{}
	}
	if d.Documents == nil {
		d.Documents = []entities.DocumentSummary{}
	}
	return d, nil
}
