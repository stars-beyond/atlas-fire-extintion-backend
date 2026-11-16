package ask

// O Diário de bordo: pergunta em linguagem natural -> cenário -> evidência -> resposta com citação.
// O fluxo é fixo e controlado pelo código. O modelo de linguagem entra só no último passo, para redigir.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"atlas-api/domain/entities"
	"atlas-api/domain/evidence"
	"atlas-api/domain/ptbr"
)

// Embedder gera o embedding de um texto.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Searcher é a busca híbrida no acervo (domain/search).
type Searcher interface {
	Search(ctx context.Context, q string, kinds []string, inv string, k int) ([]entities.Hit, map[string]int, error)
}

// RagClient fala com o RAG da equipe (rag-Carlin).
type RagClient interface {
	Status(ctx context.Context) entities.RagStatus
	Ask(ctx context.Context, conversation, content string) (entities.RagAnswer, error)
	AskStream(ctx context.Context, conversation, content string, onText func(string)) (entities.RagAnswer, error)
}

// AskUseCase responde perguntas: com o RAG da equipe quando ele está no ar, senão com a busca local.
type AskUseCase struct {
	Catalog  *entities.Catalog
	LLM      LanguageModel
	Embedder Embedder
	Search   Searcher
	Rag      RagClient
	Infer    *InferScenarioUseCase
	RagModel string  // modelo que o RAG usa (CHAT_MODEL), só para mostrar na resposta
	Budgets  Budgets // prazos de cada etapa; campo zerado = o de DefaultBudgets
}

// Budgets são os prazos de cada etapa de uma pergunta. Cada etapa roda num contexto derivado do contexto do pedido,
// com o seu prazo: quando uma estoura, só ela é cortada, e a etapa seguinte ainda tem tempo. Antes o RAG podia
// gastar o prazo inteiro do pedido, e o caminho local rodava com o contexto já vencido (e respondia 502).
type Budgets struct {
	Infer     time.Duration // inferência do cenário pelo modelo; ao estourar, valem as regras (expressões regulares)
	Rag       time.Duration // resposta inteira do RAG em POST /api/ask
	RagStream time.Duration // resposta inteira do RAG em POST /api/ask/stream
	Stream    time.Duration // teto de POST /api/ask/stream do começo ao fim, com RAG ou pelo caminho local
	Search    time.Duration // busca local: embedding da pergunta e consulta no pgvector
	Embed     time.Duration // embedding da pergunta para o ranking
}

// DefaultBudgets são os prazos em produção.
//
// POST /api/ask tem 90 s ao todo (o handler; o Cloudflare corta em 100 s): 8 s de inferência, até 3 s para conferir
// o estado do RAG e 55 s de RAG deixam pelo menos 24 s para o caminho local (busca, ranking e redação).
// POST /api/ask/stream não passa pelo corte do Cloudflare (os pings mantêm a conexão), mas tem teto: 3 min ao
// todo, o que o desligamento espera (service/main.go), dos quais o RAG usa até 140 s e o caminho local, o resto.
var DefaultBudgets = Budgets{
	Infer:     8 * time.Second,
	Rag:       55 * time.Second,
	RagStream: 140 * time.Second,
	Stream:    3 * time.Minute,
	Search:    12 * time.Second,
	Embed:     5 * time.Second,
}

// budgets devolve os prazos em uso: os de uc.Budgets, completados com os de DefaultBudgets.
func (uc *AskUseCase) budgets() Budgets {
	b, d := uc.Budgets, DefaultBudgets
	if b.Infer <= 0 {
		b.Infer = d.Infer
	}
	if b.Rag <= 0 {
		b.Rag = d.Rag
	}
	if b.RagStream <= 0 {
		b.RagStream = d.RagStream
	}
	if b.Stream <= 0 {
		b.Stream = d.Stream
	}
	if b.Search <= 0 {
		b.Search = d.Search
	}
	if b.Embed <= 0 {
		b.Embed = d.Embed
	}
	return b
}

// Avisos que acompanham a resposta quando um serviço falha. O texto é fixo; a causa vai para o log.
const (
	ragDownWarning    = "The team's RAG did not respond. Answer built with the local search."
	llmDownWarning    = "The language model did not respond. Answer assembled from the evidence only."
	searchDownWarning = "The archive search did not respond. Answer limited to the verdict, which is computed from the test data."
)

// Ask responde de uma vez (POST /api/ask). Sempre há resposta: se o RAG falha, vale o caminho local; se a busca
// local também falha, vale o veredito sozinho (verdictOnly). O erro fica na assinatura para o handler, mas hoje
// é sempre nil.
func (uc *AskUseCase) Ask(ctx context.Context, in entities.AskIn) (entities.AskOut, error) {
	out, got, t0 := uc.begin(ctx, in)
	// Com o RAG da equipe no ar, é ele quem responde. O veredito e os testes vizinhos seguem ao lado.
	if st := uc.Rag.Status(ctx); st.Ready {
		rctx, cancel := context.WithTimeout(ctx, uc.budgets().Rag)
		ans, rerr := uc.Rag.Ask(rctx, in.Conversation, ragContent(in, out.Scenario, got))
		cancel()
		if rerr == nil {
			return uc.finishRag(ctx, in, out, st, ans, t0), nil
		}
		// a causa fica no log: o erro traz endereços internos, que não vão para a resposta
		log.Printf("ask: o RAG não respondeu: %v", rerr)
		out.Warning = ragDownWarning
	}
	return uc.local(ctx, in, out, t0), nil
}

// begin entende o cenário da pergunta e decide o veredito (pelos dados), antes de qualquer texto.
func (uc *AskUseCase) begin(ctx context.Context, in entities.AskIn) (entities.AskOut, []string, time.Time) {
	t0 := time.Now()
	cur := entities.Scenario{O2: 0.21, P: 1, G: 0, Family: "solido"}
	if in.Scenario != nil {
		cur = *in.Scenario
	}
	ictx, cancel := context.WithTimeout(ctx, uc.budgets().Infer)
	sc, got, _ := uc.Infer.Infer(ictx, in.Question, cur.Clean())
	cancel()
	out := entities.AskOut{Scenario: sc, Understood: got, Provider: uc.LLM.Name(), Model: uc.LLM.Model(), Citations: []entities.Citation{}}
	out.ScenarioChanged = sc != cur.Clean()
	out.Verdict = evidence.Verdict(uc.Catalog, sc, nil, 6)
	return out, got, t0
}

// ragContent é a pergunta enviada ao RAG; quando ela aponta para o cenário da tela, o cenário vai junto.
func ragContent(in entities.AskIn, sc entities.Scenario, got []string) string {
	content := in.Question
	if len(got) == 0 && reDeictic.MatchString(strings.ToLower(in.Question)) {
		content += fmt.Sprintf(" (Scenario under analysis: %s, %s oxygen, pressure of %s, %s.)", scenarioSubject(sc), ptbr.Pct(sc.O2), ptbr.Psi(sc.P), ptbr.GLabel(sc.G))
	}
	return content
}

// finishRag completa a resposta do RAG com ranking, citações e metadados.
func (uc *AskUseCase) finishRag(ctx context.Context, in entities.AskIn, out entities.AskOut, st entities.RagStatus, ans entities.RagAnswer, t0 time.Time) entities.AskOut {
	ectx, cancel := context.WithTimeout(ctx, uc.budgets().Embed)
	qvec, e := uc.Embedder.Embed(ectx, in.Question)
	cancel()
	if e == nil {
		if rk := evidence.Rank(uc.Catalog, out.Scenario, qvec); len(rk) > 5 {
			out.Ranking = rk[:5]
		} else {
			out.Ranking = rk
		}
	}
	out.Answer, out.Citations, out.Conversation = ans.Content, ragCitations(ans.Sources), ans.Conversation
	out.Mode, out.Provider, out.Model = "rag", "rag-carlin", uc.RagModel
	out.Sources = map[string]int{"rag": len(ans.Sources), "rag_trechos": st.Chunks}
	out.TookMs = time.Since(t0).Milliseconds()
	return out
}

// local responde com a busca do próprio Atlas: testes vizinhos, trechos e, se houver, o modelo de linguagem.
// A busca e o embedding têm prazo próprio; o modelo de linguagem fica com o que sobra do contexto do pedido.
func (uc *AskUseCase) local(ctx context.Context, in entities.AskIn, out entities.AskOut, t0 time.Time) entities.AskOut {
	b := uc.budgets()
	sc := out.Scenario
	sctx, cancel := context.WithTimeout(ctx, b.Search)
	hits, src, err := uc.Search.Search(sctx, in.Question, []string{"catalogo", "documento", "recurso"}, "", 7)
	cancel()
	if err != nil {
		// a causa fica no log: o erro do banco não vai para a resposta
		log.Printf("ask: a busca local falhou: %v", err)
		return uc.verdictOnly(out, t0)
	}
	out.Sources = src
	ectx, cancel := context.WithTimeout(ctx, b.Embed)
	qvec, _ := uc.Embedder.Embed(ectx, in.Question)
	cancel()
	rk := evidence.Rank(uc.Catalog, sc, qvec)
	if len(rk) > 5 {
		rk = rk[:5]
	}
	out.Ranking = rk

	// evidências numeradas: testes vizinhos (T) e trechos (D)
	var ev strings.Builder
	fmt.Fprintf(&ev, "SCENARIO: %s O₂, %s (%s atm), %s, family %s", ptbr.Pct(sc.O2), ptbr.Psi(sc.P), ptbr.Num(sc.P, 2), ptbr.GLabel(sc.G), sc.Family)
	if sc.Material != "" {
		fmt.Fprintf(&ev, ", material %s", sc.Material)
	}
	fmt.Fprintf(&ev, "\n\nVERDICT: %s. %s %s\nTests in this atmosphere: %d at the same gravity, %d at 0 g, %d at 1 g.\n",
		out.Verdict.Label, out.Verdict.Headline, out.Verdict.Explanation, out.Verdict.SameG, out.Verdict.ZeroG, out.Verdict.OneG)
	if e := out.Verdict.Empirical; e != nil {
		fmt.Fprintf(&ev, "EMPIRICAL RATE (a count of real tests, not a probability): %s %s\n", e.Headline, e.Caveat)
	}
	ev.WriteString("\nTESTS NEAREST TO THE SCENARIO (PSI test database):\n")
	for i, n := range out.Verdict.Nearest {
		c := testCitation(i, n)
		fmt.Fprintf(&ev, "[%s] %s. Difference from the scenario: %s of O₂, %s of pressure.\n", c.Ref, c.Text, ptbr.SignedPP(n.DO2pp), ptbr.SignedPct(n.DPpct))
		out.Citations = append(out.Citations, c)
	}
	ev.WriteString("\nDOCUMENT EXCERPTS (archive search):\n")
	for i, h := range hits {
		ref := fmt.Sprintf("D%d", i+1)
		where := h.File
		if h.Page != nil {
			where += fmt.Sprintf(", p. %d", *h.Page)
		}
		fmt.Fprintf(&ev, "[%s] %s (%s): %s\n", ref, h.Title, where, ptbr.Shorten(strings.Join(strings.Fields(h.Text), " "), 900))
		c := entities.Citation{Ref: ref, Kind: h.Kind, Title: h.Title, Where: where, URL: h.URL, Text: ptbr.Shorten(strings.Join(strings.Fields(h.Text), " "), 520)}
		if h.Inv != nil {
			c.Inv = *h.Inv
		}
		out.Citations = append(out.Citations, c)
	}
	ev.WriteString("\nMOST RELEVANT INVESTIGATIONS FOR THE SCENARIO:\n")
	for i, r := range rk {
		fmt.Fprintf(&ev, "%d. %s (%s): %s\n", i+1, r.Name, r.ID, r.Reason)
	}

	// Fora do acervo: a busca não achou nada parecido. Melhor dizer isso do que redigir.
	top := 0.0
	for _, h := range hits {
		if h.Origin == "atlas" && h.Sim > top {
			top = h.Sim
		}
	}
	out.TopSimilarity = ptbr.Round3(top)
	out.Abstained = len(hits) == 0 || top < AbstainBelow

	answer := ""
	if uc.LLM.Name() != "nenhum" && !out.Abstained {
		txt, lerr := uc.LLM.Complete(ctx, SystemPrompt, "QUESTION: "+in.Question+"\n\nEVIDENCE\n"+ev.String())
		if lerr == nil {
			answer, out.Mode = strings.TrimSpace(txt), "si"
		} else {
			log.Printf("ask: o modelo de linguagem não respondeu: %v", lerr)
			if out.Warning == "" {
				out.Warning = llmDownWarning
			}
		}
	}
	if answer == "" {
		answer, out.Mode = extractive(out, hits), "extrativo"
	}
	out.Answer, out.Citations = renumber(answer, out.Citations)
	out.TookMs = time.Since(t0).Milliseconds()
	return out
}

// verdictOnly é a resposta quando nem a busca local respondeu (banco fora do ar, ou o prazo do pedido acabou):
// o veredito e os testes vizinhos, que saem do acervo em memória, sem trechos de documentos e com o aviso.
// O formato é o mesmo de uma resposta em modo extrativo.
func (uc *AskUseCase) verdictOnly(out entities.AskOut, t0 time.Time) entities.AskOut {
	rk := evidence.Rank(uc.Catalog, out.Scenario, nil) // sem a parte semântica: não depende do embedder
	if len(rk) > 5 {
		rk = rk[:5]
	}
	out.Ranking = rk
	for i, n := range out.Verdict.Nearest {
		out.Citations = append(out.Citations, testCitation(i, n))
	}
	out.Sources = map[string]int{"atlas": 0}
	out.Mode, out.Warning = "extrativo", searchDownWarning
	out.Answer, out.Citations = renumber(verdictText(out.Verdict), out.Citations)
	out.TookMs = time.Since(t0).Milliseconds()
	return out
}
