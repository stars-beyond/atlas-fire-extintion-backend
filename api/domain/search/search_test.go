package search

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"atlas-api/domain/entities"
)

// O que FTSQuery devolve vai inteiro para to_tsquery('english', $2). Tem de ser vazio, ou termos de letras
// minúsculas e números separados por " | ": qualquer outro caractere (& ! ( ) : * ' \) é sintaxe do tsquery, e
// uma consulta malformada derruba a busca com erro do Postgres.
var safeTSQuery = regexp.MustCompile(`^[a-z0-9]{3,}( \| [a-z0-9]{3,})*$`)

func TestFTSQuery(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"inglês simples", "flame spread in microgravity", "flame | spread | microgravity"},
		{"português pelo glossário", "propagação da chama em microgravidade", "spread | flame | microgravity"},
		{"glossário com maiúscula vira minúscula", "queima de acrílico", "burning | pmma"},
		{"sem distinção de caixa", "FLAME Flame flame", "flame"},
		{"sem repetição depois de traduzir", "fogo fire incêndio", "fire"},
		{"palavras vazias e curtas saem", "what is the O2 limit for the ISS", "limit | iss"},
		{"hífen é removido, não vira operador", "low-gravity flame-spread", "lowgravity | flamespread"},
		{"palavra com acento fora do glossário sai", "combustão smoke", "smoke"},
		{"números ficam", "saffire 2 psi 102", "saffire | psi | 102"},
		{"só pontuação", "?!... --- '' ()", ""},
		{"vazio", "", ""},
		{"só palavras vazias", "what is this", ""},
		{"operadores do tsquery", "fire & !smoke | (soot:*) <-> 'flame'", "fire | smoke | soot | flame"},
		{"tentativa de fechar a consulta", "fire'); DROP TABLE chunks;--", "fire | drop | table | chunks"},
		{"barra invertida e aspas", `smoke\ "soot" \\ flame`, "smoke | soot | flame"},
		{"quebras de linha e tabulação", "flame\nspread\tsoot\r\nlimit", "flame | spread | soot | limit"},
		{"outros alfabetos saem", "пламя 火焰 flame", "flame"},
		{"no máximo 12 termos", "alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november",
			"alpha | bravo | charlie | delta | echo | foxtrot | golf | hotel | india | juliet | kilo | lima"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FTSQuery(c.in)
			if got != c.want {
				t.Errorf("FTSQuery(%q) = %q, want %q", c.in, got, c.want)
			}
			if got != "" && !safeTSQuery.MatchString(got) {
				t.Errorf("FTSQuery(%q) = %q não é um tsquery seguro", c.in, got)
			}
		})
	}
}

// Nenhuma entrada, por mais estranha, produz algo fora do formato seguro.
func TestFTSQueryIsAlwaysSafe(t *testing.T) {
	nasty := []string{
		"a & b", "!!!", "((((", "fire:*", "fire:A", "'fire'", `"fire"`, "fire <2> smoke", "fire || smoke", "| | |", "&&&&",
		"fire\x00smoke", "fire​smoke", "ﬁre", "ｆｉｒｅ", "fire--smoke", "-fire", "fire-", "a-b-c-d", "💥 fire 🔥",
		strings.Repeat("flame ", 500), strings.Repeat("x", 5000), strings.Repeat("a|", 300), "été naïve façade", "İstanbul ıssız",
		"1e9 0x1F 3.14", "fire\\", "\\", "'", `'; SELECT pg_sleep(10); --`, "$1 $2", "%s %d", "{{.}}", "<script>alert(1)</script>",
	}
	for _, in := range nasty {
		got := FTSQuery(in)
		if got != "" && !safeTSQuery.MatchString(got) {
			t.Errorf("FTSQuery(%q) = %q não é um tsquery seguro", in, got)
		}
		if n := len(strings.Split(got, " | ")); n > 12 {
			t.Errorf("FTSQuery(%q) tem %d termos, o teto é 12", in, n)
		}
	}
}

func TestToEnglish(t *testing.T) {
	cases := []struct{ in, want string }{
		{"propagação da chama", "spread da flame"},
		{"Fogo na Lua", "fire na lunar"},             // o glossário não distingue maiúsculas
		{"flame spread", "flame spread"},             // inglês passa igual
		{"acrílico a 30% de O2", "PMMA a 30% de O2"}, // pontuação e números ficam onde estão
		{"", ""},
	}
	for _, c := range cases {
		if got := ToEnglish(c.in); got != c.want {
			t.Errorf("ToEnglish(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- o caso de uso, com embedder e banco falsos ---

type fakeEmbedder struct {
	vec  []float32
	err  error
	hang bool // só volta quando o contexto acaba
	got  string
}

func (f *fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	f.got = text
	if f.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.vec, f.err
}

type fakeChunks struct {
	hits   []entities.Hit
	err    error
	got    ChunkQuery
	ctxErr error // estado do contexto quando a consulta chegou
}

func (f *fakeChunks) SearchChunks(ctx context.Context, q ChunkQuery) ([]entities.Hit, error) {
	f.got, f.ctxErr = q, ctx.Err()
	if f.ctxErr != nil { // como o pgx: contexto vencido, consulta recusada
		return nil, f.ctxErr
	}
	return f.hits, f.err
}

func TestSearchPassesVectorAndWordQuery(t *testing.T) {
	emb := &fakeEmbedder{vec: []float32{1, 2}}
	chunks := &fakeChunks{hits: []entities.Hit{{ID: "a1"}, {ID: "a2"}}}
	uc := &HybridSearchUseCase{Embedder: emb, Chunks: chunks}
	hits, src, err := uc.Search(context.Background(), "propagação da chama", []string{"documento"}, "PSI-25", 7)
	if err != nil || len(hits) != 2 || src["atlas"] != 2 {
		t.Fatalf("got %v, %v, %v", hits, src, err)
	}
	if emb.got != "spread da flame" {
		t.Errorf("o embedding recebeu %q, want a pergunta com os termos em inglês", emb.got)
	}
	q := chunks.got
	if len(q.Vec) != 2 || q.FTS != "spread | flame" || q.Inv != "PSI-25" || q.K != 7 || len(q.Kinds) != 1 {
		t.Errorf("consulta = %+v", q)
	}
}

// Embedder fora do ar: a busca segue só por palavra.
func TestSearchWithoutEmbedderFallsBackToWords(t *testing.T) {
	chunks := &fakeChunks{hits: []entities.Hit{{ID: "a1"}}}
	uc := &HybridSearchUseCase{Embedder: &fakeEmbedder{err: errors.New("connection refused")}, Chunks: chunks}
	hits, _, err := uc.Search(context.Background(), "flame spread", nil, "", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("got %v, %v", hits, err)
	}
	if chunks.got.Vec != nil || chunks.got.FTS != "flame | spread" {
		t.Errorf("consulta = %+v, want sem vetor", chunks.got)
	}
}

// Embedder travado: ele é cortado no prazo dele, e a busca por palavra roda com o contexto do pedido ainda vivo.
func TestSearchHangingEmbedderDoesNotBurnTheRequest(t *testing.T) {
	chunks := &fakeChunks{hits: []entities.Hit{{ID: "a1"}}}
	uc := &HybridSearchUseCase{Embedder: &fakeEmbedder{hang: true}, Chunks: chunks, EmbedTimeout: 20 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t0 := time.Now()
	hits, _, err := uc.Search(ctx, "flame spread", nil, "", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("got %v, %v: a busca por palavra devia ter respondido", hits, err)
	}
	if chunks.ctxErr != nil {
		t.Fatalf("a consulta ao banco recebeu um contexto vencido: %v", chunks.ctxErr)
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("a busca levou %v: o embedder não foi cortado no prazo", d)
	}
}

func TestSearchDatabaseErrorIsReturned(t *testing.T) {
	uc := &HybridSearchUseCase{Embedder: &fakeEmbedder{vec: []float32{1}}, Chunks: &fakeChunks{err: errors.New("boom")}}
	if hits, src, err := uc.Search(context.Background(), "flame", nil, "", 5); err == nil || hits != nil || src != nil {
		t.Fatalf("got %v, %v, %v", hits, src, err)
	}
}

func TestEmbedBudgetDefault(t *testing.T) {
	if EmbedBudget <= 0 || EmbedBudget > 10*time.Second {
		t.Fatalf("EmbedBudget = %v", EmbedBudget)
	}
}
