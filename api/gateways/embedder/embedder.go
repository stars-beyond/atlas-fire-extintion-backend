// Package embedder chama o serviço de embeddings da ingestão (brain, fastembed multilíngue).
package embedder

import (
	"context"
	"fmt"

	"atlas-api/extensions/zhttp"
)

// Client gera embeddings pelo serviço em URL (POST /embed).
type Client struct {
	URL string
}

func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	var out struct {
		Vectors [][]float32 `json:"vectors"`
	}
	if err := zhttp.PostJSON(ctx, c.URL+"/embed", nil, map[string]any{"texts": []string{text}}, &out); err != nil {
		return nil, err
	}
	if len(out.Vectors) == 0 {
		return nil, fmt.Errorf("embedder returned no vectors")
	}
	return out.Vectors[0], nil
}
