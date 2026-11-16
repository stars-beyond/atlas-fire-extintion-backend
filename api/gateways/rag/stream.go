package rag

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"atlas-api/domain/entities"
)

// AskStream é o Ask em streaming: chama onText com cada pedaço do texto e devolve a resposta validada no fim.
func (r *Client) AskStream(ctx context.Context, conversation, content string, onText func(string)) (entities.RagAnswer, error) {
	// teto da resposta inteira: os pings do SSE mantêm a conexão viva, então sem isto um RAG travado prenderia a
	// conexão (e a goroutine) para sempre. Quem chama costuma trazer um prazo menor no contexto (domain/ask, Budgets).
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
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
	b, _ := json.Marshal(map[string]string{"content": content})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL+"/conversations/"+url.PathEscape(conversation)+"/messages/stream", strings.NewReader(string(b)))
	if err != nil {
		return entities.RagAnswer{Conversation: conversation}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// sem o Timeout total do cliente normal: o stream dura o tempo da resposta, e o ctx (teto de 3 min) corta se preciso
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		return entities.RagAnswer{Conversation: conversation}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return entities.RagAnswer{Conversation: conversation}, fmt.Errorf("the RAG responded HTTP %d", res.StatusCode)
	}

	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20) // o evento final traz as fontes com trechos
	event, sent := "", false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := []byte(strings.TrimPrefix(line, "data: "))
			switch event {
			case "texto":
				var t struct {
					T string `json:"t"`
				}
				if json.Unmarshal(data, &t) == nil && t.T != "" {
					sent = true
					onText(t.T)
				}
			case "pronto":
				var p struct {
					AI struct {
						Content string               `json:"content"`
						Sources []entities.RagSource `json:"sources"`
					} `json:"ai_message"`
				}
				if err := json.Unmarshal(data, &p); err != nil {
					return entities.RagAnswer{Conversation: conversation}, err
				}
				return entities.RagAnswer{Content: p.AI.Content, Sources: p.AI.Sources, Conversation: conversation}, nil
			case "erro":
				var e struct {
					Status int    `json:"status"`
					Detail string `json:"detail"`
				}
				_ = json.Unmarshal(data, &e)
				if e.Status == http.StatusNotFound && !sent && given { // conversa de uma sessão anterior: começa outra, uma vez só
					res.Body.Close()
					return r.AskStream(ctx, "", content, onText)
				}
				if e.Detail == "" {
					e.Detail = fmt.Sprintf("the RAG responded %d", e.Status)
				}
				return entities.RagAnswer{Conversation: conversation}, errors.New(e.Detail)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return entities.RagAnswer{Conversation: conversation}, err
	}
	return entities.RagAnswer{Conversation: conversation}, errors.New("the RAG ended the answer before it finished")
}
