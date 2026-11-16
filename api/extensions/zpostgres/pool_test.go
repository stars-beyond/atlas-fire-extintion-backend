package zpostgres

import (
	"errors"
	"testing"
	"time"
)

// Sem nada na URL valem os limites do pacote; o que a URL traz é respeitado (como já era com o search_path).
func TestConfig(t *testing.T) {
	t.Setenv("PGCONNECT_TIMEOUT", "") // o pgx também lê o prazo de conexão do ambiente
	cases := []struct {
		name      string
		url       string
		maxConns  int32
		lifetime  time.Duration
		idle      time.Duration
		connect   time.Duration
		path      string
		statement string
	}{
		{"padrões", "postgresql://atlas:atlas@localhost:54329/atlas",
			MaxConns, MaxConnLifetime, MaxConnIdleTime, ConnectTimeout, SearchPath, "15000"},
		{"a URL manda", "postgresql://atlas:atlas@db:5432/atlas?pool_max_conns=3&pool_max_conn_lifetime=10m&pool_max_conn_idle_time=45s" +
			"&connect_timeout=2&search_path=public&statement_timeout=4000",
			3, 10 * time.Minute, 45 * time.Second, 2 * time.Second, "public", "4000"},
		{"só parte na URL", "postgresql://atlas:atlas@db:5432/atlas?pool_max_conns=25&statement_timeout=0",
			25, MaxConnLifetime, MaxConnIdleTime, ConnectTimeout, SearchPath, "0"},
		{"formato chave=valor", "host=db user=atlas password=atlas dbname=atlas pool_max_conn_idle_time=2m",
			MaxConns, MaxConnLifetime, 2 * time.Minute, ConnectTimeout, SearchPath, "15000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := Config(c.url)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MaxConns != c.maxConns || cfg.MaxConnLifetime != c.lifetime || cfg.MaxConnIdleTime != c.idle {
				t.Errorf("pool: %d conexões, vida %v, ociosa %v", cfg.MaxConns, cfg.MaxConnLifetime, cfg.MaxConnIdleTime)
			}
			if cfg.ConnConfig.ConnectTimeout != c.connect {
				t.Errorf("connect timeout = %v, want %v", cfg.ConnConfig.ConnectTimeout, c.connect)
			}
			rp := cfg.ConnConfig.RuntimeParams
			if rp["search_path"] != c.path || rp["statement_timeout"] != c.statement {
				t.Errorf("runtime params = %v", rp)
			}
			for k := range rp { // os parâmetros do pool não podem ir para o Postgres como parâmetros de sessão
				if len(k) > 5 && k[:5] == "pool_" {
					t.Errorf("%s ficou nos parâmetros de sessão", k)
				}
			}
		})
	}
}

func TestStatementTimeoutIsFifteenSeconds(t *testing.T) {
	if StatementTimeout != 15*time.Second {
		t.Fatalf("statement_timeout = %v: a carga do acervo e a busca vetorial contam com 15 s", StatementTimeout)
	}
}

func TestConfigRejectsBadURL(t *testing.T) {
	for _, url := range []string{"postgresql://atlas@db:notaport/atlas", "postgresql://db/atlas?pool_max_conns=0", "postgresql://db/atlas?pool_max_conns=abc"} {
		if _, err := Config(url); err == nil {
			t.Errorf("%q devia ser recusada", url)
		}
	}
}

func TestRetry(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	v, err := Retry(5, time.Millisecond, "o teste", func() (int, error) {
		calls++
		if calls < 3 {
			return 0, boom
		}
		return 42, nil
	})
	if err != nil || v != 42 || calls != 3 {
		t.Fatalf("got %d, %v depois de %d chamadas", v, err, calls)
	}
	calls = 0
	_, err = Retry(2, time.Millisecond, "o teste", func() (int, error) { calls++; return 0, boom })
	if !errors.Is(err, boom) || calls != 3 { // a primeira tentativa mais duas repetições
		t.Fatalf("got %v depois de %d chamadas", err, calls)
	}
}
