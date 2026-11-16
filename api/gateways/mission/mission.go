// Package mission chama o motor de missão no serviço Python (brain): GET /mission/presets e POST /mission/simulate.
package mission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	domainmission "atlas-api/domain/mission"
	"atlas-api/extensions/zhttp"
)

// Client fala com o motor em URL (o mesmo serviço dos embeddings e da câmera térmica).
type Client struct {
	URL string
}

func (c *Client) Presets(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	return out, translate(zhttp.GetJSON(ctx, c.URL+"/mission/presets", &out))
}

func (c *Client) Simulate(ctx context.Context, cfg json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	return out, translate(zhttp.PostJSON(ctx, c.URL+"/mission/simulate", nil, cfg, &out))
}

// translate separa "a missão pedida é impossível" (4xx do motor, com o motivo) de "o motor não respondeu".
func translate(err error) error {
	if err == nil {
		return nil
	}
	var se *zhttp.StatusError
	if errors.As(err, &se) && se.Code >= 400 && se.Code < 500 {
		var body struct {
			Detail json.RawMessage `json:"detail"`
		}
		_ = json.Unmarshal(se.Body, &body)
		// detail é o motivo (texto) ou, se a configuração não passou no esquema, a lista de erros de validação
		var msg string
		if json.Unmarshal(body.Detail, &msg) == nil && msg != "" {
			return &domainmission.InvalidError{Message: msg}
		}
		return &domainmission.InvalidError{Message: "invalid mission configuration", Detail: body.Detail}
	}
	return fmt.Errorf("%w: %v", domainmission.ErrUnavailable, err)
}
