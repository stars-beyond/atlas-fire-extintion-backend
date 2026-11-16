// Package zpostgres abre o pool de conexões com o Postgres (pgvector) da ingestão.
package zpostgres

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SearchPath: a ingestão publica o acervo no schema atlas (montado ao lado e trocado numa transação);
// public fica no caminho para o tipo vector e para bancos de versões antigas, com as tabelas em public.
const SearchPath = "atlas,public"

// Limites do pool. Valem quando a URL de conexão não diz outra coisa (pool_max_conns, pool_max_conn_lifetime,
// pool_max_conn_idle_time, connect_timeout, statement_timeout).
const (
	// MaxConns: no blue/green duas cores ficam no ar ao mesmo tempo, ao lado da ingestão e do RAG, e o Postgres
	// aceita 100 conexões. O padrão do pgx (4 ou o número de CPUs) muda de máquina para máquina.
	MaxConns = 10
	// MaxConnLifetime recicla as conexões: depois de a ingestão trocar o schema, nenhuma fica velha por muito tempo.
	MaxConnLifetime = 30 * time.Minute
	// MaxConnIdleTime devolve ao banco as conexões paradas depois de um pico.
	MaxConnIdleTime = 5 * time.Minute
	// ConnectTimeout: banco fora do ar vira erro em segundos, em vez de prender o pedido no tempo do TCP.
	ConnectTimeout = 5 * time.Second
	// StatementTimeout é o teto de qualquer consulta, cortada pelo próprio Postgres. Folgado o bastante para a
	// carga do acervo na subida e para a busca vetorial; os pedidos têm prazos menores no contexto.
	StatementTimeout = 15 * time.Second
)

// Connect abre o pool. A conexão de fato só acontece na primeira consulta.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := Config(url)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// Config lê a URL de conexão e completa o que ela não traz com os limites acima.
func Config(url string) (*pgxpool.Config, error) {
	// O pgxpool tira os parâmetros pool_* ao ler a URL e põe os padrões dele no lugar: para saber o que a URL
	// trouxe de fato, ela é lida antes sem o pool.
	raw, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	given := func(k string) bool { _, ok := raw.RuntimeParams[k]; return ok }
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if !given("pool_max_conns") {
		cfg.MaxConns = MaxConns
	}
	if !given("pool_max_conn_lifetime") {
		cfg.MaxConnLifetime = MaxConnLifetime
	}
	if !given("pool_max_conn_idle_time") {
		cfg.MaxConnIdleTime = MaxConnIdleTime
	}
	if cfg.ConnConfig.ConnectTimeout == 0 { // connect_timeout na URL ou PGCONNECT_TIMEOUT no ambiente
		cfg.ConnConfig.ConnectTimeout = ConnectTimeout
	}
	if _, ok := cfg.ConnConfig.RuntimeParams["search_path"]; !ok {
		cfg.ConnConfig.RuntimeParams["search_path"] = SearchPath
	}
	if _, ok := cfg.ConnConfig.RuntimeParams["statement_timeout"]; !ok {
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(StatementTimeout.Milliseconds(), 10) // em ms
	}
	return cfg, nil
}

// Retry repete fn a cada intervalo até dar certo ou passar de tries tentativas. Serve para esperar o banco
// e a ingestão na subida do docker compose.
func Retry[T any](tries int, every time.Duration, what string, fn func() (T, error)) (T, error) {
	var zero T
	for i := 0; ; i++ {
		v, err := fn()
		if err == nil {
			return v, nil
		}
		if i >= tries {
			return zero, err
		}
		log.Printf("aguardando %s (%v)", what, err)
		time.Sleep(every)
	}
}
