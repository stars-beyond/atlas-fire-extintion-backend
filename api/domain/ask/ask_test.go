package ask

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"atlas-api/domain/entities"
)

// --- dependências falsas: nada aqui usa rede nem banco ---

type fakeLLM struct {
	name         string // anthropic | openai | nenhum
	jsonOut      string
	jsonErr      error
	jsonHang     bool // só volta quando o contexto acaba
	answer       string
	completeErr  error
	completeHang bool
	jsonCalls    atomic.Int32
	calls        atomic.Int32
	lastUser     string
}

func (f *fakeLLM) Name() string  { return f.name }
func (f *fakeLLM) Model() string { return "fake-model" }
func (f *fakeLLM) JSON(ctx context.Context, _, _ string) (string, error) {
	f.jsonCalls.Add(1)
	if f.jsonHang {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return f.jsonOut, f.jsonErr
}
func (f *fakeLLM) Complete(ctx context.Context, _, user string) (string, error) {
	f.calls.Add(1)
	f.lastUser = user
	if f.completeHang {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return f.answer, f.completeErr
}

type fakeRag struct {
	ready  bool
	hang   bool // RAG travado: só volta quando o contexto acaba, com o erro do contexto
	err    error
	answer entities.RagAnswer
	texts  []string // pedaços emitidos no streaming
	calls  atomic.Int32
	// cancela o contexto do pedido no meio da resposta (o cliente desistiu)
	clientGone context.CancelFunc
}

func (f *fakeRag) Status(context.Context) entities.RagStatus {
	return entities.RagStatus{Enabled: true, Ready: f.ready, Chunks: 4321}
}
func (f *fakeRag) Ask(ctx context.Context, _, _ string) (entities.RagAnswer, error) {
	f.calls.Add(1)
	if f.hang {
		<-ctx.Done()
		return entities.RagAnswer{}, ctx.Err()
	}
	return f.answer, f.err
}
func (f *fakeRag) AskStream(ctx context.Context, _, _ string, onText func(string)) (entities.RagAnswer, error) {
	f.calls.Add(1)
	for _, t := range f.texts {
		onText(t)
	}
	if f.clientGone != nil {
		f.clientGone()
	}
	if f.hang || f.clientGone != nil {
		<-ctx.Done()
		return entities.RagAnswer{}, ctx.Err()
	}
	return f.answer, f.err
}

// fakeSearch faz o papel da busca no banco: com o contexto vencido ela falha, como o pgx.
type fakeSearch struct {
	hits   []entities.Hit
	err    error
	calls  atomic.Int32
	ctxErr error // estado do contexto quando a busca foi chamada
}

func (f *fakeSearch) Search(ctx context.Context, _ string, _ []string, _ string, _ int) ([]entities.Hit, map[string]int, error) {
	f.calls.Add(1)
	f.ctxErr = ctx.Err()
	if f.ctxErr != nil {
		return nil, nil, f.ctxErr
	}
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.hits, map[string]int{"atlas": len(f.hits)}, nil
}

type fakeEmbed struct {
	hang bool
	err  error
}

func (f *fakeEmbed) Embed(ctx context.Context, _ string) ([]float32, error) {
	if f.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []float32{1, 0}, f.err
}

type recorder struct {
	mu     sync.Mutex
	events []string
	data   []any
}

func (r *recorder) Send(event string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	r.data = append(r.data, data)
}

func (r *recorder) answer(t *testing.T) entities.AskOut {
	t.Helper()
	for i, e := range r.events {
		if e == "resposta" {
			return r.data[i].(entities.AskOut)
		}
	}
	t.Fatalf("nenhum evento resposta: %v", r.events)
	return entities.AskOut{}
}

func askCatalog() *entities.Catalog {
	inv := &entities.Investigation{ID: "PSI-25", Name: "BASS-II", Family: "solido", Priority: 0.9, PriorityWhy: "Cabin solids", Vec: []float32{1, 0}}
	cat := &entities.Catalog{InvByID: map[string]*entities.Investigation{"PSI-25": inv}, Invs: []*entities.Investigation{inv}}
	for i, out := range []string{"queimou", "queimou", "extinguiu"} {
		o2, p := 0.21, 1.0
		cat.Tests = append(cat.Tests, &entities.Test{ID: string(rune('a' + i)), Inv: "PSI-25", Label: "run " + string(rune('1'+i)),
			Family: "solido", O2: &o2, P: &p, Outcome: out, File: "bass.csv", Row: i + 2})
	}
	return cat
}

var goodHits = []entities.Hit{
	{ID: "a1", Origin: "atlas", Kind: "documento", Title: "BASS-II report", File: "bass.pdf", Text: "Flames spread faster with flow.", Sim: 0.8},
	{ID: "a2", Origin: "atlas", Kind: "catalogo", Title: "BASS-II summary", File: "catalog", Text: "Burning and suppression of solids.", Sim: 0.6},
}

// Prazos curtos, para os testes de estouro não esperarem os de produção.
var fast = Budgets{Infer: 30 * time.Millisecond, Rag: 40 * time.Millisecond, RagStream: 40 * time.Millisecond,
	Stream: 2 * time.Second, Search: time.Second, Embed: 30 * time.Millisecond}

func newUC(llm *fakeLLM, rag *fakeRag, search *fakeSearch, emb *fakeEmbed) *AskUseCase {
	return &AskUseCase{Catalog: askCatalog(), LLM: llm, Embedder: emb, Search: search, Rag: rag,
		Infer: &InferScenarioUseCase{LLM: llm}, RagModel: "rag-model", Budgets: fast}
}

// uma pergunta que não fala de cenário: as regras não mudam nada, e vale a estação com sólidos
var question = entities.AskIn{Question: "Is a fire likely to keep going?"}

// --- os prazos ---

func TestBudgetsDefaults(t *testing.T) {
	d := (&AskUseCase{}).budgets()
	if d != DefaultBudgets {
		t.Fatalf("sem nada configurado valem os padrões: %+v", d)
	}
	b := (&AskUseCase{Budgets: Budgets{Rag: time.Second}}).budgets()
	if b.Rag != time.Second || b.Infer != DefaultBudgets.Infer || b.Search != DefaultBudgets.Search {
		t.Fatalf("um campo configurado não muda os outros: %+v", b)
	}
}

// Os prazos de produção têm de caber no que está em volta: os 90 s do handler de /api/ask (o Cloudflare corta em
// 100 s) e os 190 s que o desligamento espera por uma resposta em streaming.
func TestDefaultBudgetsFitTheirCeilings(t *testing.T) {
	const handler, ragStatus, shutdown = 90 * time.Second, 3 * time.Second, 190 * time.Second
	d := DefaultBudgets
	if d.Infer > 8*time.Second || d.Rag > 55*time.Second {
		t.Errorf("inferência %v (teto 8 s), RAG %v (teto 55 s)", d.Infer, d.Rag)
	}
	if left := handler - d.Infer - ragStatus - d.Rag; left < 20*time.Second {
		t.Errorf("sobram %v para o caminho local em /api/ask, o mínimo é 20 s", left)
	}
	if d.Stream > shutdown {
		t.Errorf("o teto do streaming (%v) passa do que o desligamento espera (%v)", d.Stream, shutdown)
	}
	if left := d.Stream - d.Infer - ragStatus - d.RagStream; left < 20*time.Second {
		t.Errorf("sobram %v para o caminho local no streaming, o mínimo é 20 s", left)
	}
	if d.Search+d.Embed >= 20*time.Second {
		t.Errorf("a busca (%v) e o embedding (%v) não deixam tempo para a redação", d.Search, d.Embed)
	}
}

// --- POST /api/ask ---

func TestAskWithRag(t *testing.T) {
	yes := true
	rag := &fakeRag{ready: true, answer: entities.RagAnswer{Content: "The RAG says [1].", Conversation: "c9",
		Sources: []entities.RagSource{{Citation: 1, Title: "Report", Cited: &yes}}}}
	search := &fakeSearch{hits: goodHits}
	out, err := newUC(&fakeLLM{name: "anthropic"}, rag, search, &fakeEmbed{}).Ask(context.Background(), question)
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "rag" || out.Provider != "rag-carlin" || out.Model != "rag-model" || out.Answer != "The RAG says [1]." || out.Conversation != "c9" {
		t.Errorf("resposta do RAG: %+v", out)
	}
	if len(out.Citations) != 1 || out.Citations[0].Ref != "R1" || out.Sources["rag"] != 1 || out.Sources["rag_trechos"] != 4321 {
		t.Errorf("citações %+v, fontes %v", out.Citations, out.Sources)
	}
	if out.Verdict.Tier != "medido" || len(out.Ranking) != 1 || out.Warning != "" {
		t.Errorf("veredito %s, ranking %d, aviso %q", out.Verdict.Tier, len(out.Ranking), out.Warning)
	}
	if search.calls.Load() != 0 {
		t.Error("com o RAG respondendo a busca local não roda")
	}
}

// O caso da revisão: o RAG estoura o prazo. Antes o caminho local herdava o contexto vencido, a busca falhava e
// o usuário recebia 502. Agora só o RAG é cortado, e a busca local roda com o contexto do pedido ainda vivo.
func TestAskRagTimeoutFallsBackToLocalSearch(t *testing.T) {
	rag := &fakeRag{ready: true, hang: true}
	search := &fakeSearch{hits: goodHits}
	llm := &fakeLLM{name: "anthropic", answer: "Acrylic burns at 21% [T1]. The report agrees [D1]."}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // o prazo do pedido, como o do handler
	defer cancel()
	t0 := time.Now()
	out, err := newUC(llm, rag, search, &fakeEmbed{}).Ask(ctx, question)
	if err != nil {
		t.Fatalf("RAG lento não pode virar erro: %v", err)
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("a resposta levou %v: o RAG não foi cortado no prazo dele", d)
	}
	if search.calls.Load() != 1 || search.ctxErr != nil {
		t.Fatalf("a busca local recebeu um contexto vencido: %v", search.ctxErr)
	}
	if out.Mode != "si" || out.Warning != ragDownWarning {
		t.Errorf("modo %q, aviso %q", out.Mode, out.Warning)
	}
	if out.Answer != "Acrylic burns at 21% [1]. The report agrees [2]." || len(out.Citations) != 2 ||
		out.Citations[0].Kind != "teste" || out.Citations[1].Kind != "documento" {
		t.Errorf("resposta %q, citações %+v", out.Answer, out.Citations)
	}
	if out.Sources["atlas"] != 2 || out.Abstained || out.TopSimilarity != 0.8 {
		t.Errorf("fontes %v, abstained %v, semelhança %v", out.Sources, out.Abstained, out.TopSimilarity)
	}
}

// RAG fora do prazo e busca local também falhando (banco fora do ar): a resposta é o veredito, que sai do acervo
// em memória, em modo extrativo e com o aviso fixo. Antes era 502.
func TestAskRagTimeoutAndSearchDownAnswersWithVerdictOnly(t *testing.T) {
	rag := &fakeRag{ready: true, hang: true}
	search := &fakeSearch{err: errors.New("dial tcp 10.0.0.5:5432: connection refused")}
	llm := &fakeLLM{name: "anthropic", answer: "should not be used"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // o prazo do pedido, como o do handler
	defer cancel()
	out, err := newUC(llm, rag, search, &fakeEmbed{err: errors.New("embedder down")}).Ask(ctx, question)
	if err != nil {
		t.Fatalf("busca fora do ar não pode virar erro: %v", err)
	}
	if out.Mode != "extrativo" || out.Warning != searchDownWarning {
		t.Errorf("modo %q, aviso %q", out.Mode, out.Warning)
	}
	if !strings.HasPrefix(out.Answer, out.Verdict.Headline) || !strings.Contains(out.Answer, "Tests nearest to the scenario:") {
		t.Errorf("a resposta devia ser o veredito e os testes vizinhos: %q", out.Answer)
	}
	if strings.Contains(out.Answer, "10.0.0.5") || strings.Contains(out.Warning, "10.0.0.5") || strings.Contains(out.Answer, "refused") {
		t.Errorf("a causa interna vazou para a resposta: %q / %q", out.Answer, out.Warning)
	}
	if strings.Contains(out.Answer, "found no excerpts") || strings.Contains(out.Answer, "What the archive says") {
		t.Errorf("sem busca a resposta não pode falar do que a busca achou: %q", out.Answer)
	}
	// o formato de uma resposta de sucesso continua inteiro: listas e mapa presentes, nunca null
	if len(out.Citations) != 3 || out.Citations[0].N != 1 || out.Citations[0].Kind != "teste" {
		t.Errorf("citações dos testes vizinhos: %+v", out.Citations)
	}
	if out.Sources == nil || out.Sources["atlas"] != 0 || len(out.Ranking) != 1 || out.Verdict.Tier != "medido" {
		t.Errorf("fontes %v, ranking %v, veredito %s", out.Sources, out.Ranking, out.Verdict.Tier)
	}
	if llm.calls.Load() != 0 {
		t.Error("sem trechos o modelo de linguagem não é chamado")
	}
}

// Mesmo com o prazo do pedido já esgotado ao chegar, há resposta (o veredito), e não erro.
func TestAskWithExpiredRequestStillAnswers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	search := &fakeSearch{hits: goodHits}
	out, err := newUC(&fakeLLM{name: "nenhum"}, &fakeRag{}, search, &fakeEmbed{}).Ask(ctx, question)
	if err != nil || out.Mode != "extrativo" || out.Warning != searchDownWarning || out.Answer == "" {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestAskLocalPaths(t *testing.T) {
	weak := []entities.Hit{{ID: "a1", Origin: "atlas", Kind: "documento", Title: "Rocket engines", Text: "Propellant.", Sim: 0.2}}
	cases := []struct {
		name      string
		llm       *fakeLLM
		hits      []entities.Hit
		mode      string
		warning   string
		abstained bool
		llmCalls  int32
		contains  string
	}{
		{"modelo redige", &fakeLLM{name: "anthropic", answer: "  Written by the model [D1].  "}, goodHits, "si", "", false, 1, "Written by the model [1]."},
		{"sem modelo: extrativo", &fakeLLM{name: "nenhum"}, goodHits, "extrativo", "", false, 0, "What the archive says about the question:"},
		{"modelo falha: extrativo com aviso", &fakeLLM{name: "anthropic", completeErr: errors.New("HTTP 529 overloaded")}, goodHits,
			"extrativo", llmDownWarning, false, 1, "What the archive says about the question:"},
		{"modelo devolve vazio: extrativo", &fakeLLM{name: "anthropic", answer: "   "}, goodHits, "extrativo", "", false, 1, "What the archive says"},
		{"nada parecido no acervo: abstém e não chama o modelo", &fakeLLM{name: "anthropic", answer: "x"}, weak, "extrativo", "", true, 0,
			"found no excerpts close to this question"},
		{"busca sem resultado: abstém", &fakeLLM{name: "anthropic", answer: "x"}, nil, "extrativo", "", true, 0, "found no excerpts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := newUC(c.llm, &fakeRag{}, &fakeSearch{hits: c.hits}, &fakeEmbed{}).Ask(context.Background(), question)
			if err != nil {
				t.Fatal(err)
			}
			if out.Mode != c.mode || out.Warning != c.warning || out.Abstained != c.abstained {
				t.Errorf("modo %q, aviso %q, abstained %v", out.Mode, out.Warning, out.Abstained)
			}
			if !strings.Contains(out.Answer, c.contains) {
				t.Errorf("resposta %q, faltou %q", out.Answer, c.contains)
			}
			if c.llm.calls.Load() != c.llmCalls {
				t.Errorf("chamadas ao modelo = %d, want %d", c.llm.calls.Load(), c.llmCalls)
			}
			if strings.Contains(out.Answer, "529") || strings.Contains(out.Warning, "529") {
				t.Errorf("a causa interna vazou: %q / %q", out.Answer, out.Warning)
			}
			if out.Citations == nil || out.Sources == nil || out.Provider == "" {
				t.Errorf("formato incompleto: %+v", out)
			}
		})
	}
}

// O que vai para o modelo: a pergunta e as evidências numeradas, com o veredito decidido pelos dados.
func TestAskLocalPromptCarriesEvidence(t *testing.T) {
	llm := &fakeLLM{name: "anthropic", answer: "ok"}
	if _, err := newUC(llm, &fakeRag{}, &fakeSearch{hits: goodHits}, &fakeEmbed{}).Ask(context.Background(), question); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"QUESTION: Is a fire likely to keep going?", "VERDICT: Measured.", "[T1] BASS-II run 1", "[D1] BASS-II report (bass.pdf)",
		"EMPIRICAL RATE", "MOST RELEVANT INVESTIGATIONS"} {
		if !strings.Contains(llm.lastUser, want) {
			t.Errorf("faltou %q no texto enviado ao modelo:\n%s", want, llm.lastUser)
		}
	}
}

// Cada etapa lenta é cortada no prazo dela, e a pergunta ainda é respondida.
func TestAskSlowStagesAreCutAtTheirBudget(t *testing.T) {
	cases := []struct {
		name string
		llm  *fakeLLM
		emb  *fakeEmbed
		mode string
		warn string
	}{
		{"inferência de cenário travada: valem as regras", &fakeLLM{name: "anthropic", jsonHang: true, answer: "Fine [T1]."}, &fakeEmbed{}, "si", ""},
		{"embedder travado: ranking sem a parte semântica", &fakeLLM{name: "anthropic", jsonOut: "{}", answer: "Fine [T1]."}, &fakeEmbed{hang: true}, "si", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			search := &fakeSearch{hits: goodHits}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			t0 := time.Now()
			out, err := newUC(c.llm, &fakeRag{}, search, c.emb).Ask(ctx, entities.AskIn{Question: "Does acrylic burn at 30% oxygen?"})
			if err != nil {
				t.Fatal(err)
			}
			if d := time.Since(t0); d > 2*time.Second {
				t.Fatalf("levou %v: a etapa travada não foi cortada", d)
			}
			if search.ctxErr != nil {
				t.Fatalf("a busca recebeu um contexto vencido: %v", search.ctxErr)
			}
			if out.Mode != c.mode || out.Warning != c.warn || len(out.Ranking) != 1 {
				t.Errorf("modo %q, aviso %q, ranking %d", out.Mode, out.Warning, len(out.Ranking))
			}
			if out.Scenario.O2 != 0.30 && c.llm.jsonHang { // as regras leram os 30% da pergunta
				t.Errorf("cenário = %+v", out.Scenario)
			}
		})
	}
}

// Depois de o RAG responder, um embedder travado não segura a resposta: ela sai sem o ranking.
func TestAskRagAnswerDoesNotWaitForHangingEmbedder(t *testing.T) {
	rag := &fakeRag{ready: true, answer: entities.RagAnswer{Content: "ok"}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t0 := time.Now()
	out, err := newUC(&fakeLLM{name: "nenhum"}, rag, &fakeSearch{}, &fakeEmbed{hang: true}).Ask(ctx, question)
	if err != nil || out.Mode != "rag" || out.Answer != "ok" {
		t.Fatalf("got %+v, %v", out, err)
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("levou %v", d)
	}
}

func TestAskScenarioFromRequestAndQuestion(t *testing.T) {
	uc := newUC(&fakeLLM{name: "nenhum"}, &fakeRag{}, &fakeSearch{hits: goodHits}, &fakeEmbed{})
	// sem cenário no pedido vale a estação; a pergunta não fala de cenário
	out, _ := uc.Ask(context.Background(), question)
	if out.ScenarioChanged || out.Scenario.O2 != 0.21 || out.Scenario.Family != "solido" {
		t.Errorf("cenário padrão: %+v (mudou: %v)", out.Scenario, out.ScenarioChanged)
	}
	// a pergunta muda o cenário da tela
	cur := entities.Scenario{O2: 0.21, P: 1, Family: "solido"}
	out, _ = uc.Ask(context.Background(), entities.AskIn{Question: "and on a lunar base?", Scenario: &cur})
	if !out.ScenarioChanged || out.Scenario.Habitat != "lua" || len(out.Understood) != 1 || out.Verdict.Tier != "sem_evidencia" {
		t.Errorf("base lunar: %+v, entendido %v, veredito %s", out.Scenario, out.Understood, out.Verdict.Tier)
	}
}

// --- POST /api/ask/stream ---

func TestStreamWithRag(t *testing.T) {
	rag := &fakeRag{ready: true, texts: []string{"The ", "answer."}, answer: entities.RagAnswer{Content: "The answer.", Conversation: "c1"}}
	rec := &recorder{}
	newUC(&fakeLLM{name: "nenhum"}, rag, &fakeSearch{}, &fakeEmbed{}).Stream(context.Background(), question, rec)
	if got := strings.Join(rec.events, ","); got != "contexto,texto,texto,resposta" {
		t.Fatalf("eventos = %s", got)
	}
	ctxEvent := rec.data[0].(entities.AskContext)
	if ctxEvent.Verdict.Tier != "medido" {
		t.Errorf("o veredito sai no primeiro evento: %+v", ctxEvent)
	}
	if rec.data[1].(map[string]string)["t"] != "The " {
		t.Errorf("texto parcial: %v", rec.data[1])
	}
	if out := rec.answer(t); out.Mode != "rag" || out.Answer != "The answer." || out.Conversation != "c1" {
		t.Errorf("resposta: %+v", out)
	}
}

// RAG travado no streaming: é cortado no prazo dele, e a resposta vem pelo caminho local, sem evento de erro.
func TestStreamRagTimeoutFallsBackToLocal(t *testing.T) {
	rag := &fakeRag{ready: true, hang: true, texts: []string{"partial "}}
	search := &fakeSearch{hits: goodHits}
	rec := &recorder{}
	t0 := time.Now()
	newUC(&fakeLLM{name: "nenhum"}, rag, search, &fakeEmbed{}).Stream(context.Background(), question, rec)
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("levou %v: o RAG não foi cortado no prazo dele", d)
	}
	if got := strings.Join(rec.events, ","); got != "contexto,texto,resposta" {
		t.Fatalf("eventos = %s", got)
	}
	if search.ctxErr != nil {
		t.Fatalf("a busca local recebeu um contexto vencido: %v", search.ctxErr)
	}
	if out := rec.answer(t); out.Mode != "extrativo" || out.Warning != ragDownWarning || !strings.Contains(out.Answer, "What the archive says") {
		t.Errorf("resposta: %+v", out)
	}
}

func TestStreamSearchDownAnswersWithVerdictOnly(t *testing.T) {
	for _, rag := range []*fakeRag{{}, {ready: true, err: errors.New("the RAG responded HTTP 500")}} {
		rec := &recorder{}
		search := &fakeSearch{err: errors.New("connection refused")}
		newUC(&fakeLLM{name: "anthropic", answer: "x"}, rag, search, &fakeEmbed{}).Stream(context.Background(), question, rec)
		if got := strings.Join(rec.events, ","); got != "contexto,resposta" {
			t.Fatalf("eventos = %s (não pode haver erro: o veredito responde)", got)
		}
		if out := rec.answer(t); out.Mode != "extrativo" || out.Warning != searchDownWarning || !strings.HasPrefix(out.Answer, out.Verdict.Headline) {
			t.Errorf("resposta: %+v", out)
		}
	}
}

// O cliente fechou a conexão no meio da resposta do RAG: não há para quem responder, e o caminho local nem roda.
func TestStreamClientGoneStopsQuietly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rag := &fakeRag{ready: true, texts: []string{"partial"}, clientGone: cancel}
	search := &fakeSearch{hits: goodHits}
	rec := &recorder{}
	newUC(&fakeLLM{name: "nenhum"}, rag, search, &fakeEmbed{}).Stream(ctx, question, rec)
	if got := strings.Join(rec.events, ","); got != "contexto,texto" {
		t.Fatalf("eventos = %s", got)
	}
	if search.calls.Load() != 0 {
		t.Error("a busca local rodou para um cliente que já foi embora")
	}
}

// O caminho local do streaming também tem teto: um modelo travado é cortado, e a resposta sai em modo extrativo.
// Antes só o RAG tinha teto (3 min), e este caminho podia ficar aberto enquanto os pings mantinham a conexão.
func TestStreamLocalPathHasGlobalCeiling(t *testing.T) {
	uc := newUC(&fakeLLM{name: "anthropic", jsonOut: "{}", completeHang: true}, &fakeRag{}, &fakeSearch{hits: goodHits}, &fakeEmbed{})
	uc.Budgets.Stream = 80 * time.Millisecond
	rec := &recorder{}
	done := make(chan struct{})
	t0 := time.Now()
	go func() { uc.Stream(context.Background(), question, rec); close(done) }() // o pedido não tem prazo: só o teto corta
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("o streaming local ficou preso: não há teto")
	}
	if d := time.Since(t0); d < 60*time.Millisecond {
		t.Fatalf("terminou em %v, antes do teto", d)
	}
	if got := strings.Join(rec.events, ","); got != "contexto,resposta" {
		t.Fatalf("eventos = %s", got)
	}
	if out := rec.answer(t); out.Mode != "extrativo" || out.Warning != llmDownWarning {
		t.Errorf("resposta: modo %q, aviso %q", out.Mode, out.Warning)
	}
}
