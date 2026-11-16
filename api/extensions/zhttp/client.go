// Package zhttp é o cliente HTTP dos serviços externos (embeddings, modelos de linguagem).
package zhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 60 * time.Second}

// PostJSON manda in como JSON e lê a resposta em out. Erro HTTP vira erro com o começo do corpo.
func PostJSON(ctx context.Context, url string, headers map[string]string, in, out any) error {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		return &StatusError{URL: url, Code: res.StatusCode, Body: raw}
	}
	return json.Unmarshal(raw, out)
}

// GetJSON lê a resposta de um GET em out, com os mesmos limites e erros de PostJSON.
func GetJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		return &StatusError{URL: url, Code: res.StatusCode, Body: raw}
	}
	return json.Unmarshal(raw, out)
}

// StatusError é a resposta HTTP de erro de um serviço: o código e o corpo, para quem precisa repassar o motivo.
type StatusError struct {
	URL  string
	Code int
	Body []byte
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.URL, e.Code, shorten(string(e.Body), 300))
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
