// Package thermal chama o serviço Python (brain) que roda as redes da câmera térmica (POST /thermal).
package thermal

import (
	"context"

	"atlas-api/extensions/zhttp"
)

// Client pede o filme previsto ao serviço em URL (o mesmo dos embeddings).
type Client struct {
	URL string
}

// Rollout devolve o filme como veio do serviço: quadros comprimidos, frente, velocidade e origem.
// sample pede o ensaio de exemplo com os quadros medidos, em vez das condições dadas.
func (c *Client) Rollout(ctx context.Context, o2Pct, kPa, flow float64, sample bool) (map[string]any, error) {
	in := map[string]any{"sample": sample, "steps": 63}
	if !sample {
		in["o2_pct"], in["pressure_kpa"], in["flow_cm_s"] = o2Pct, kPa, flow
	}
	var out map[string]any
	if err := zhttp.PostJSON(ctx, c.URL+"/thermal", nil, in, &out); err != nil {
		return nil, err
	}
	return out, nil
}
