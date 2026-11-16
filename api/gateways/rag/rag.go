// Package rag fala com o RAG da equipe (rag-Carlin): um serviço FastAPI que responde sobre a coleção v6
// (no pgvector) com fontes numeradas, fórmulas em LaTeX e histórico de conversa. O Atlas continua decidindo
// o nível de evidência; o RAG responde o que os documentos dizem.
package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"atlas-api/domain/entities"
)

// Client fala com o RAG da equipe. Um *Client nil responde como RAG desligado.
type Client struct {
	URL    string
	client *http.Client

	mu      sync.Mutex
	checked time.Time
	probing bool
	state   entities.RagStatus
}

// New devolve o cliente, ou nil quando RAG_URL não está definido.
func New(url string) *Client {
	u := strings.TrimRight(url, "/")
	if u == "" {
		return nil
	}
	// 80 s é só o teto de segurança deste cliente. O prazo de cada pergunta vem do contexto (domain/ask, Budgets:
	// 55 s para o RAG), para sobrar tempo ao caminho local dentro dos 100 s em que o Cloudflare corta.
	return &Client{URL: u, client: &http.Client{Timeout: 80 * time.Second}}
}

func (r *Client) do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, r.URL+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Detail any `json:"detail"`
		}
		_ = json.Unmarshal(raw, &e)
		if s, ok := e.Detail.(string); ok && s != "" {
			return res.StatusCode, fmt.Errorf("%s", s)
		}
		return res.StatusCode, fmt.Errorf("the RAG responded with HTTP %d", res.StatusCode)
	}
	return res.StatusCode, json.Unmarshal(raw, out)
}

// Status consulta /health do RAG, com cache curto para não pesar em cada pergunta.
// A consulta não segura a trava nem usa o contexto do pedido: com o RAG travado, o /api/meta responde com o último
// estado conhecido em vez de esperar, e um cliente que desiste não marca o RAG como fora do ar.
func (r *Client) Status(_ context.Context) entities.RagStatus {
	if r == nil {
		return entities.RagStatus{}
	}
	r.mu.Lock()
	ttl := 20 * time.Second
	if !r.state.Ready {
		ttl = 4 * time.Second // enquanto carrega o acervo, confere mais vezes
	}
	if r.probing || time.Since(r.checked) < ttl {
		st := r.state
		r.mu.Unlock()
		return st
	}
	r.probing = true
	r.mu.Unlock()

	c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var h struct {
		Rag struct {
			Ready  bool   `json:"ready"`
			Detail string `json:"detail"`
			Chunks int    `json:"chunks"`
		} `json:"rag"`
	}
	st := entities.RagStatus{Enabled: true}
	if _, err := r.do(c, http.MethodGet, "/health", nil, &h); err != nil {
		st.Detail = "RAG service is down"
	} else {
		st.Ready, st.Chunks, st.Detail = h.Rag.Ready, h.Rag.Chunks, h.Rag.Detail
	}
	r.mu.Lock()
	r.state, r.checked, r.probing = st, time.Now(), false
	r.mu.Unlock()
	return st
}

// Ask manda a pergunta para uma conversa do RAG (criando uma se preciso) e devolve resposta e fontes.
func (r *Client) Ask(ctx context.Context, conversation, content string) (entities.RagAnswer, error) {
	given := conversation != ""
	if conversation == "" {
		var c struct {
			ID string `json:"id"`
		}
		if _, err := r.do(ctx, http.MethodPost, "/conversations/", map[string]any{}, &c); err != nil {
			return entities.RagAnswer{}, err
		}
		conversation = c.ID
	}
	var out struct {
		AI struct {
			Content string               `json:"content"`
			Sources []entities.RagSource `json:"sources"`
		} `json:"ai_message"`
	}
	code, err := r.do(ctx, http.MethodPost, "/conversations/"+url.PathEscape(conversation)+"/messages", map[string]any{"content": content}, &out)
	if code == http.StatusNotFound && given { // conversa de uma sessão anterior: começa outra, uma vez só
		return r.Ask(ctx, "", content)
	}
	if err != nil {
		return entities.RagAnswer{Conversation: conversation}, err
	}
	return entities.RagAnswer{Content: out.AI.Content, Sources: out.AI.Sources, Conversation: conversation}, nil
}
