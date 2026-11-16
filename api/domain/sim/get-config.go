// Package sim prepara o que o Laboratório de chama 3D (lab3d) precisa da API: a configuração
// (materiais, gases, constantes, ambientes) e a validação do simulador contra a curva da NASA.
//
// O simulador não guarda esses valores no código: ao abrir, pede /api/sim/config e confere cada
// número contra as faixas que aceita (formato "lab3d.config/1", ver public/lab3d/model.js no atlas-fire-extintion-front).
// O conteúdo vem de um arquivo JSON (SIM_CONFIG), lido a cada pedido, então editar o arquivo
// e recarregar a página basta. Sem o arquivo a resposta é 404 e o simulador usa o padrão embutido.
//
// Três coisas não vêm do arquivo, para existirem num lugar só:
//   - a fronteira de inflamabilidade de cada material é a curva publicada pela NASA, a mesma que a
//     vista "Limites" desenha (desfechos do BASS-II); material sem curva publicada fica sem fronteira;
//   - o vento natural da gravidade segue a mesma regra da avaliação dos cenários (Buoyant);
//   - os ambientes são os da API (entities.Habitats).
package sim

import (
	"math"

	"atlas-api/domain/entities"
	"atlas-api/domain/flammability"
)

// Files lê os arquivos do simulador (configuração e validação).
type Files interface {
	LoadConfig() (map[string]any, error)
	LoadValidation() ([]byte, error)
	LoadSpreadCheck() ([]byte, error)
}

// simMaterial liga a forma da amostra dos desfechos ao material do simulador.
var simMaterial = map[string]string{"bastão de acrílico": "pmma"}

// GetConfigUseCase monta /api/sim/config.
type GetConfigUseCase struct {
	Catalog *entities.Catalog
	Files   Files
}

// simBoundary é a curva de um material no formato que o simulador lê.
func simBoundary(b *entities.Boundary) map[string]any {
	c := b.Published.Coef
	return map[string]any{"form": "nasa-6.1", "A": c.A, "k": c.K, "B": c.B, "C": c.C, "r0_cm": c.R0, "band_o2": b.Band,
		"source": b.Published.Source, "url": b.Published.URL}
}

// Get devolve a configuração pronta. O erro vem do arquivo (inexistente: errors.Is(err, fs.ErrNotExist)).
func (uc *GetConfigUseCase) Get() (map[string]any, error) {
	cfg, err := uc.Files.LoadConfig()
	if err != nil {
		return nil, err
	}
	bounds := map[string]*entities.Boundary{}
	if d := uc.Catalog.Outcomes[flammability.BassII]; d != nil {
		for i := range d.Boundaries {
			if id, ok := simMaterial[d.Boundaries[i].Form]; ok {
				bounds[id] = &d.Boundaries[i]
			}
		}
	}
	if mats, ok := cfg["materials"].([]any); ok {
		for _, m := range mats {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			id, _ := mm["id"].(string)
			if b := bounds[id]; b != nil {
				mm["boundary"] = simBoundary(b)
			} else {
				mm["boundary"] = nil // sem curva publicada: o simulador não dá veredito analítico
			}
		}
	}
	for _, b := range bounds {
		cfg["buoyancy"] = map[string]any{"flow_1g_cm_s": b.Buoyancy.Flow1g, "superposition": "linear", "sources": b.Buoyancy.Sources}
		break
	}
	habs := []map[string]any{}
	for _, h := range entities.Habitats {
		if h.O2 > 0.6 { // o simulador aceita até 60% de oxigênio
			continue
		}
		habs = append(habs, map[string]any{"name": h.Name, "g": h.G, "o2": math.Round(h.O2*1000) / 10, "press": math.Round(h.P*146.96) / 10, "wind": entities.VentForced})
	}
	cfg["habitats"] = habs
	cfg["source"] = "Atlas API"
	return cfg, nil
}
