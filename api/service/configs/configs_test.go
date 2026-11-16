package configs

import (
	"path/filepath"
	"testing"
)

var vars = []string{"PORT", "DATABASE_URL", "EMBEDDER_URL", "RAG_URL", "CHAT_MODEL", "ANTHROPIC_API_KEY", "ATLAS_MODEL",
	"OPENAI_API_KEY", "OPENAI_BASE_URL", "SIM_CONFIG", "SIM_VALIDATION", "SPREAD_TABLE", "SIM_SPREAD_CHECK", "EXTERNAL_TESTS"}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range vars {
		t.Setenv(k, "")
	}
}

// Sem nenhuma variável a API sobe com os padrões de desenvolvimento: RAG desligado, nenhum modelo com chave.
func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	c := Load()
	checks := []struct{ name, got, want string }{
		{"PORT", c.Server.Port, "8787"},
		{"DATABASE_URL", c.Database.URL, "postgresql://atlas:atlas@localhost:54329/atlas"},
		{"EMBEDDER_URL", c.Services.EmbedderURL, "http://localhost:8000"},
		{"RAG_URL", c.Services.RagURL, ""},
		{"CHAT_MODEL (RAG)", c.Services.RagModel, ""},
		{"ANTHROPIC_API_KEY", c.LLM.AnthropicKey, ""},
		{"OPENAI_API_KEY", c.LLM.OpenAIKey, ""},
		{"CHAT_MODEL (OpenAI)", c.LLM.OpenAIModel, "gpt-4o"},
		{"OPENAI_BASE_URL", c.LLM.OpenAIBaseURL, "https://api.openai.com/v1"},
		{"SIM_CONFIG", c.Sim.ConfigPath, "../data/seed/sim/lab3d.json"},
		{"SIM_VALIDATION", c.Sim.ValidationPath, filepath.Join("../data/seed/sim", "validation.json")},
		{"SPREAD_TABLE", c.Sim.SpreadPath, filepath.Join("../data/seed/sim", "spread.json")},
		{"SIM_SPREAD_CHECK", c.Sim.SpreadCheck, filepath.Join("../data/seed/sim", "spread-validation.json")},
		{"EXTERNAL_TESTS", c.External.Path, "../data/seed/external/zarm.json"},
	}
	for _, k := range checks {
		if k.got != k.want {
			t.Errorf("%s = %q, want %q", k.name, k.got, k.want)
		}
	}
	if c.LLM.AnthropicModel == "" {
		t.Error("ATLAS_MODEL sem padrão")
	}
}

func TestLoadReadsEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", " 9000 ") // espaços nas pontas não contam
	t.Setenv("DATABASE_URL", "postgresql://u:p@db:5432/x")
	t.Setenv("EMBEDDER_URL", "http://brain:8000")
	t.Setenv("RAG_URL", "http://rag:8080")
	t.Setenv("CHAT_MODEL", "gpt-test")
	t.Setenv("ANTHROPIC_API_KEY", "test-key-a")
	t.Setenv("ATLAS_MODEL", "claude-test")
	t.Setenv("OPENAI_API_KEY", "test-key-o")
	t.Setenv("OPENAI_BASE_URL", "http://llm:1234/v1")
	t.Setenv("SIM_CONFIG", "/data/sim/lab3d.json")
	t.Setenv("EXTERNAL_TESTS", "/data/zarm.json")
	c := Load()
	checks := []struct{ name, got, want string }{
		{"PORT", c.Server.Port, "9000"},
		{"DATABASE_URL", c.Database.URL, "postgresql://u:p@db:5432/x"},
		{"EMBEDDER_URL", c.Services.EmbedderURL, "http://brain:8000"},
		{"RAG_URL", c.Services.RagURL, "http://rag:8080"},
		{"CHAT_MODEL (RAG)", c.Services.RagModel, "gpt-test"},
		{"CHAT_MODEL (OpenAI)", c.LLM.OpenAIModel, "gpt-test"},
		{"ANTHROPIC_API_KEY", c.LLM.AnthropicKey, "test-key-a"},
		{"ATLAS_MODEL", c.LLM.AnthropicModel, "claude-test"},
		{"OPENAI_API_KEY", c.LLM.OpenAIKey, "test-key-o"},
		{"OPENAI_BASE_URL", c.LLM.OpenAIBaseURL, "http://llm:1234/v1"},
		{"SIM_CONFIG", c.Sim.ConfigPath, "/data/sim/lab3d.json"},
		// os outros arquivos do simulador ficam ao lado do SIM_CONFIG quando não têm variável própria
		{"SIM_VALIDATION", c.Sim.ValidationPath, "/data/sim/validation.json"},
		{"SPREAD_TABLE", c.Sim.SpreadPath, "/data/sim/spread.json"},
		{"SIM_SPREAD_CHECK", c.Sim.SpreadCheck, "/data/sim/spread-validation.json"},
		{"EXTERNAL_TESTS", c.External.Path, "/data/zarm.json"},
	}
	for _, k := range checks {
		if k.got != k.want {
			t.Errorf("%s = %q, want %q", k.name, k.got, k.want)
		}
	}
}

// Cada arquivo do simulador pode ter o seu caminho, independente do SIM_CONFIG.
func TestLoadSimFilesOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("SIM_CONFIG", "/a/lab3d.json")
	t.Setenv("SIM_VALIDATION", "/b/v.json")
	t.Setenv("SPREAD_TABLE", "/c/s.json")
	t.Setenv("SIM_SPREAD_CHECK", "/d/sv.json")
	c := Load()
	if c.Sim.ValidationPath != "/b/v.json" || c.Sim.SpreadPath != "/c/s.json" || c.Sim.SpreadCheck != "/d/sv.json" {
		t.Fatalf("sim = %+v", c.Sim)
	}
}
