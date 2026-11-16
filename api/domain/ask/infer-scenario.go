package ask

// Inferência de cenário: uma frase livre vira habitat, material, oxigênio, pressão e gravidade.
// A ideia vem do agente da equipe (agent-fire-space), que preenche o painel a partir da pergunta
// com saída estruturada do modelo. Aqui o modelo só extrai o que está escrito; valores ausentes
// ficam nulos e o cenário atual é mantido. Sem modelo, valem as expressões regulares.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// LanguageModel redige respostas e extrai dados estruturados. Name() == "nenhum" quando não há modelo.
type LanguageModel interface {
	Name() string // anthropic | openai | nenhum
	Model() string
	Complete(ctx context.Context, system, user string) (string, error)
	// JSON pede uma resposta curta em JSON (extração estruturada), com o menor esforço de raciocínio.
	JSON(ctx context.Context, system, user string) (string, error)
}

// InferScenarioUseCase transforma uma frase em cenário, pelo modelo ou por regras.
type InferScenarioUseCase struct {
	LLM LanguageModel
}

// InferResult é a resposta de POST /api/scenario/infer.
type InferResult struct {
	Scenario   entities.Scenario `json:"scenario"`
	Understood []string          `json:"understood"`
	Method     string            `json:"method"`
	Changed    bool              `json:"changed"`
}

// Describe parte do cenário atual da tela (ou do padrão, a estação com algodão) e aplica a frase.
func (uc *InferScenarioUseCase) Describe(ctx context.Context, text string, cur *entities.Scenario) InferResult {
	base := entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido", Material: "sibal", Habitat: "iss"}
	if cur != nil {
		base = *cur
	}
	s, got, method := uc.Infer(ctx, text, base.Clean())
	if got == nil { // nada entendido: lista vazia, não null (a interface lê o tamanho dela)
		got = []string{}
	}
	return InferResult{Scenario: s, Understood: got, Method: method, Changed: s != base.Clean()}
}

type inferred struct {
	Habitat  *string  `json:"habitat"`
	Material *string  `json:"material"`
	O2       *float64 `json:"o2_percent"`
	PSI      *float64 `json:"pressure_psi"`
	G        *float64 `json:"gravity_g"`
	About    bool     `json:"about_scenario"`
}

// Infer devolve o cenário ajustado, o que foi entendido e por qual método ("modelo" ou "regras").
func (uc *InferScenarioUseCase) Infer(ctx context.Context, text string, cur entities.Scenario) (entities.Scenario, []string, string) {
	if uc.LLM.Name() != "nenhum" {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if raw, err := uc.LLM.JSON(c, InferPrompt, text); err == nil {
			if i := strings.Index(raw, "{"); i >= 0 {
				if j := strings.LastIndex(raw, "}"); j > i {
					var in inferred
					if json.Unmarshal([]byte(raw[i:j+1]), &in) == nil {
						s, got := applyInferred(cur, in)
						return s, got, "modelo"
					}
				}
			}
		}
	}
	s, got := ParseScenario(text, cur)
	return s, got, "regras"
}

func applyInferred(cur entities.Scenario, in inferred) (entities.Scenario, []string) {
	s := cur
	var got []string
	if in.Habitat != nil {
		for _, h := range entities.Habitats {
			if h.ID == *in.Habitat {
				s.O2, s.P, s.G, s.Habitat = h.O2, h.P, h.G, h.ID
				got = append(got, "habitat: "+h.Name)
			}
		}
	}
	custom := false
	if in.O2 != nil && *in.O2 >= 1 && *in.O2 <= 100 {
		s.O2, custom = *in.O2/100, true
		got = append(got, "O₂: "+ptbr.Pct(s.O2))
	}
	if in.PSI != nil && *in.PSI >= 2 && *in.PSI <= 90 {
		s.P, custom = *in.PSI/14.696, true
		got = append(got, "pressure: "+ptbr.Psi(s.P))
	}
	if in.G != nil && *in.G >= 0 && *in.G <= 1 {
		s.G, custom = *in.G, true
		got = append(got, "gravity: "+ptbr.GLabel(s.G))
	}
	if custom { // valores próprios só mantêm o nome do habitat se ainda baterem com ele
		s.Habitat = ""
		for _, h := range entities.Habitats {
			if abs(h.O2-s.O2) < 0.006 && abs(h.P-s.P) < 0.012 && abs(h.G-s.G) < 0.006 {
				s.Habitat = h.ID
			}
		}
	}
	if in.Material != nil {
		names := map[string]string{"sibal": "cotton fabric", "pmma": "acrylic", "nomex": "Nomex", "silicone": "silicone", "gota": "liquid fuel", "gas": "flammable gas"}
		switch m := *in.Material; m {
		case "sibal", "pmma", "nomex", "silicone":
			s.Family, s.Material = "solido", m
			got = append(got, "material: "+names[m])
		case "gota", "gas":
			s.Family, s.Material = m, ""
			got = append(got, "material: "+names[m])
		}
	}
	return s.Clean(), got
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
