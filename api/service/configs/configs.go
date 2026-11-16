// Package configs lê a configuração da API das variáveis de ambiente (as mesmas do docker compose).
package configs

import (
	"os"
	"path/filepath"
	"strings"
)

type Database struct {
	URL string // DATABASE_URL
}

type Services struct {
	EmbedderURL string // EMBEDDER_URL: serviço de embeddings da ingestão
	RagURL      string // RAG_URL: RAG da equipe; vazio = desligado
	RagModel    string // CHAT_MODEL: modelo do RAG, só para exibir
}

type LLM struct {
	AnthropicKey   string // ANTHROPIC_API_KEY
	AnthropicModel string // ATLAS_MODEL
	OpenAIKey      string // OPENAI_API_KEY
	OpenAIModel    string // CHAT_MODEL
	OpenAIBaseURL  string // OPENAI_BASE_URL
}

type Sim struct {
	ConfigPath     string // SIM_CONFIG
	ValidationPath string // SIM_VALIDATION (padrão: validation.json ao lado do SIM_CONFIG)
	SpreadPath     string // SPREAD_TABLE (padrão: spread.json ao lado do SIM_CONFIG)
	SpreadCheck    string // SIM_SPREAD_CHECK (padrão: spread-validation.json ao lado do SIM_CONFIG)
}

type External struct {
	Path string // EXTERNAL_TESTS: ensaios de fora do PSI (atlas.external/1); sem o arquivo, a API sobe sem eles
}

type Server struct {
	Port string // PORT
}

// Config é toda a configuração da API.
type Config struct {
	Server   Server
	Database Database
	Services Services
	LLM      LLM
	Sim      Sim
	External External
}

// Load lê as variáveis de ambiente, com os mesmos padrões de sempre.
func Load() Config {
	simCfg := env("SIM_CONFIG", "../data/seed/sim/lab3d.json")
	return Config{
		Server:   Server{Port: env("PORT", "8787")},
		Database: Database{URL: env("DATABASE_URL", "postgresql://atlas:atlas@localhost:54329/atlas")},
		Services: Services{EmbedderURL: env("EMBEDDER_URL", "http://localhost:8000"), RagURL: env("RAG_URL", ""), RagModel: env("CHAT_MODEL", "")},
		LLM: LLM{AnthropicKey: env("ANTHROPIC_API_KEY", ""), AnthropicModel: env("ATLAS_MODEL", "claude-opus-5-5"),
			OpenAIKey: env("OPENAI_API_KEY", ""), OpenAIModel: env("CHAT_MODEL", "gpt-4o"), OpenAIBaseURL: env("OPENAI_BASE_URL", "https://api.openai.com/v1")},
		Sim: Sim{ConfigPath: simCfg, ValidationPath: env("SIM_VALIDATION", filepath.Join(filepath.Dir(simCfg), "validation.json")),
			SpreadPath:  env("SPREAD_TABLE", filepath.Join(filepath.Dir(simCfg), "spread.json")),
			SpreadCheck: env("SIM_SPREAD_CHECK", filepath.Join(filepath.Dir(simCfg), "spread-validation.json"))},
		External: External{Path: env("EXTERNAL_TESTS", "../data/seed/external/zarm.json")},
	}
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
