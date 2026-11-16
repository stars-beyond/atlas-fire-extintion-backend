package ask

// Perguntas em streaming (Server-Sent Events): POST /api/ask/stream.
//
// Eventos, nesta ordem:
//   contexto  logo de cara: cenário entendido, se mudou e o veredito (decidido pelos dados, não pelo modelo)
//   texto     pedaços da resposta enquanto o RAG da equipe escreve ({"t": "..."})
//   resposta  a resposta completa e validada, no mesmo formato de POST /api/ask (substitui o texto parcial)
//   erro      {"error": "..."} quando nada pôde ser respondido (o front o trata; hoje nenhum caminho o emite)
// Sem RAG, ou se ele falhar, a resposta vem pelo caminho local (busca + modelo ou modo extrativo) num só evento.
// Se a busca local também falhar, a resposta é o veredito sozinho, com aviso (verdictOnly): o veredito já saiu
// no evento contexto, e uma resposta curta serve mais do que um erro.

import (
	"context"
	"log"

	"atlas-api/domain/entities"
)

// Emitter manda um evento ao cliente (o handler usa SSE).
type Emitter interface {
	Send(event string, data any)
}

// Stream responde emitindo os eventos acima. A resposta inteira tem teto (Budgets.Stream), com RAG ou sem ele:
// os pings do SSE mantêm a conexão viva, então sem teto um serviço travado prenderia a conexão (e a goroutine).
func (uc *AskUseCase) Stream(ctx context.Context, in entities.AskIn, emit Emitter) {
	b := uc.budgets()
	ctx, cancel := context.WithTimeout(ctx, b.Stream)
	defer cancel()
	out, got, t0 := uc.begin(ctx, in)
	emit.Send("contexto", entities.AskContext{Scenario: out.Scenario, ScenarioChanged: out.ScenarioChanged,
		Understood: out.Understood, Verdict: out.Verdict})

	if st := uc.Rag.Status(ctx); st.Ready {
		// o RAG tem um prazo menor que o teto: se ele travar, sobra tempo para o caminho local
		rctx, rcancel := context.WithTimeout(ctx, b.RagStream)
		ans, err := uc.Rag.AskStream(rctx, in.Conversation, ragContent(in, out.Scenario, got),
			func(t string) { emit.Send("texto", map[string]string{"t": t}) })
		rcancel()
		if err == nil {
			emit.Send("resposta", uc.finishRag(ctx, in, out, st, ans, t0))
			return
		}
		if ctx.Err() != nil { // o cliente desistiu (ou o teto acabou): não há para quem responder
			return
		}
		log.Printf("ask/stream: o RAG não respondeu: %v", err)
		out.Warning = ragDownWarning
	}
	emit.Send("resposta", uc.local(ctx, in, out, t0))
}
