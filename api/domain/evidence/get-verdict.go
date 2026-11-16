package evidence

import "atlas-api/domain/entities"

// GetVerdictUseCase devolve o veredito do cenário e a matriz por família de combustível.
type GetVerdictUseCase struct {
	Catalog *entities.Catalog
}

func (uc *GetVerdictUseCase) Get(s entities.Scenario) (entities.Verdict, []entities.MatrixCell) {
	v := Verdict(uc.Catalog, s, nil, 8)
	v.External = External(uc.Catalog, s, &v)
	// matriz: o mesmo cenário para cada família, para mostrar onde há e onde não há evidência
	var matrix []entities.MatrixCell
	for _, f := range []string{"solido", "gota", "gas"} {
		fv := Verdict(uc.Catalog, entities.Scenario{O2: s.O2, P: s.P, G: s.G, Family: f}, nil, 1)
		matrix = append(matrix, entities.MatrixCell{Family: f, Tier: fv.Tier, Label: fv.Label, N: fv.InAtm})
	}
	return v, matrix
}
