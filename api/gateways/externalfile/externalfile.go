// Package externalfile lê os ensaios de fora do PSI (atlas.external/1) de um arquivo JSON, uma vez na subida.
package externalfile

import (
	"encoding/json"
	"fmt"
	"os"

	"atlas-api/domain/entities"
)

// Load lê o arquivo. Sem arquivo devolve (nil, nil): a API sobe sem a evidência de fora da NASA.
func Load(path string) (*entities.ExternalDoc, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d entities.ExternalDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if d.Schema != "atlas.external/1" || d.ID == "" {
		return nil, fmt.Errorf("%s: unknown format", path)
	}
	return &d, nil
}
