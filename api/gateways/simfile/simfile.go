// Package simfile lê os arquivos do simulador de chama 3D: a configuração (lab3d.config/1) e a
// validação medida no banco de testes (lab3d.validation/1). Lê também a tabela do modelo neural de
// propagação (atlas.spread/1), que fica na mesma pasta. Todos são lidos a cada pedido.
package simfile

import (
	"encoding/json"
	"fmt"
	"os"
)

// Files aponta para os arquivos.
type Files struct {
	ConfigPath      string
	ValidationPath  string
	SpreadPath      string
	SpreadCheckPath string // medição da propagação no simulador (lab3d.spread/1)
}

// LoadConfig lê e confere a configuração. Arquivo ausente devolve um erro com fs.ErrNotExist.
func (f Files) LoadConfig() (map[string]any, error) {
	raw, err := os.ReadFile(f.ConfigPath)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if s, _ := cfg["schema"].(string); s != "lab3d.config/1" {
		return nil, fmt.Errorf("unknown format")
	}
	return cfg, nil
}

// LoadValidation lê o arquivo de validação como veio (o domínio confere o formato).
func (f Files) LoadValidation() ([]byte, error) {
	return os.ReadFile(f.ValidationPath)
}

// LoadSpread lê a tabela de velocidade de propagação como veio (o domínio confere o formato).
func (f Files) LoadSpread() ([]byte, error) {
	return os.ReadFile(f.SpreadPath)
}

// LoadSpreadCheck lê a medição da velocidade de propagação no simulador, como veio.
func (f Files) LoadSpreadCheck() ([]byte, error) {
	return os.ReadFile(f.SpreadCheckPath)
}
