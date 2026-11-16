// Package llm fala com os modelos de linguagem: Claude (SDK oficial), um endpoint compatível com OpenAI ou nenhum.
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"atlas-api/extensions/zhttp"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Options vem da configuração. A chave da Anthropic tem prioridade; depois um endpoint compatível com OpenAI.
type Options struct {
	AnthropicKey, AnthropicModel       string
	OpenAIKey, OpenAIModel, OpenAIBase string
}

// Model é a camada de linguagem do Diário de bordo. O Atlas funciona sem ela (modo extrativo):
// quem decide o veredito são os dados; o modelo só redige e cita.
type Model interface {
	Name() string // anthropic | openai | nenhum
	Model() string
	Complete(ctx context.Context, system, user string) (string, error)
	JSON(ctx context.Context, system, user string) (string, error)
}

// New escolhe o provedor pela chave disponível.
func New(o Options) Model {
	if o.AnthropicKey != "" {
		return &claude{client: anthropic.NewClient(option.WithAPIKey(o.AnthropicKey)), model: o.AnthropicModel}
	}
	if o.OpenAIKey != "" {
		return &openAICompat{key: o.OpenAIKey, model: o.OpenAIModel, base: strings.TrimRight(o.OpenAIBase, "/")}
	}
	return none{}
}

type none struct{}

func (none) Name() string  { return "nenhum" }
func (none) Model() string { return "" }
func (none) Complete(context.Context, string, string) (string, error) {
	return "", errors.New("no model configured")
}
func (none) JSON(context.Context, string, string) (string, error) {
	return "", errors.New("no model configured")
}

// ---------------------------------------------------------------- Claude (SDK oficial)

type claude struct {
	client anthropic.Client
	model  string
}

func (c *claude) Name() string  { return "anthropic" }
func (c *claude) Model() string { return c.model }

// Teto de tokens por chamada. O teto conta o raciocínio do modelo (pensamento adaptativo) mais o texto, e é o que
// limita o custo de uma chamada na rota pública. A redação pede no máximo 170 palavras e a extração de cenário
// devolve um objeto JSON pequeno, então os dois ficam bem abaixo dos 16 mil de antes. Uma resposta que bate no
// teto é tratada como falha (ver complete): o Atlas cai no modo extrativo ou na extração por regras, sem texto cortado.
const (
	maxTokensText = 4096
	maxTokensJSON = 2048
)

func (c *claude) Complete(ctx context.Context, system, user string) (string, error) {
	return c.complete(ctx, system, user, maxTokensText)
}

func (c *claude) complete(ctx context.Context, system, user string, maxTokens int64) (string, error) {
	// Pensamento adaptativo é o padrão do modelo; a resposta é curta, então não há streaming.
	// Fallback no servidor: se o modelo recusar por política, a API reencaminha a mesma chamada.
	resp, err := c.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: maxTokens,
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user))},
	})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) {
			switch apierr.StatusCode {
			case 401, 403:
				return "", fmt.Errorf("Anthropic key rejected (HTTP %d)", apierr.StatusCode)
			case 429:
				return "", errors.New("Anthropic rate limit reached; try again in a moment")
			default:
				return "", fmt.Errorf("Anthropic responded with HTTP %d", apierr.StatusCode)
			}
		}
		return "", err
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return "", errors.New("the model refused the question")
	}
	if resp.StopReason == anthropic.BetaStopReasonMaxTokens {
		return "", fmt.Errorf("the model hit the %d-token limit before finishing", maxTokens)
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	if b.Len() == 0 {
		return "", errors.New("empty response from the model")
	}
	return b.String(), nil
}

// JSON no Claude: a instrução de formato vai no prompt de sistema e a resposta é lida como texto.
func (c *claude) JSON(ctx context.Context, system, user string) (string, error) {
	return c.complete(ctx, system+"\nRespond only with the JSON object, with no text before or after it.", user, maxTokensJSON)
}

// ---------------------------------------------------------------- endpoint compatível com OpenAI

type openAICompat struct{ key, model, base string }

func (o *openAICompat) Name() string  { return "openai" }
func (o *openAICompat) Model() string { return o.model }

func (o *openAICompat) Complete(ctx context.Context, system, user string) (string, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	err := zhttp.PostJSON(ctx, o.base+"/chat/completions", map[string]string{"Authorization": "Bearer " + o.key}, map[string]any{
		"model":    o.model,
		"messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
	}, &out)
	if err != nil {
		return "", err
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("empty response from the model")
	}
	return out.Choices[0].Message.Content, nil
}

func (o *openAICompat) JSON(ctx context.Context, system, user string) (string, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	body := map[string]any{
		"model":           o.model,
		"messages":        []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
		"response_format": map[string]string{"type": "json_object"},
	}
	// modelos com raciocínio: esforço baixo, como o RAG da equipe faz, para a extração responder rápido
	if strings.HasPrefix(o.model, "gpt-5") || o.model == "gpt-6-luna" {
		body["reasoning_effort"] = "low"
	}
	if err := zhttp.PostJSON(ctx, o.base+"/chat/completions", map[string]string{"Authorization": "Bearer " + o.key}, body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("empty response from the model")
	}
	return out.Choices[0].Message.Content, nil
}
