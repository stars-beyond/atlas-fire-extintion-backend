// Package v1 registra as rotas HTTP da API do Atlas. Cada handler vem do pacote da sua feature.
package v1

import (
	"net/http"

	"atlas-api/extensions/zweb"
)

// Api reúne os handlers. main.go monta cada um com o seu caso de uso.
type Api struct {
	// Acervo
	Health         http.HandlerFunc
	Meta           http.HandlerFunc
	Investigations http.HandlerFunc
	Investigation  http.HandlerFunc
	Tests          http.HandlerFunc
	Test           http.HandlerFunc
	External       http.HandlerFunc

	// Evidência
	Verdict   http.HandlerFunc
	Rank      http.HandlerFunc
	NextTests http.HandlerFunc
	Graph     http.HandlerFunc
	Insights  http.HandlerFunc

	// Linguagem natural
	Ask       http.HandlerFunc
	AskStream http.HandlerFunc
	Search    http.HandlerFunc
	Infer     http.HandlerFunc

	// Inflamabilidade
	Flammability http.HandlerFunc
	Assess       http.HandlerFunc
	Queue        http.HandlerFunc

	// Previsão
	BurnRate http.HandlerFunc
	Spread   http.HandlerFunc
	Thermal  http.HandlerFunc

	// Simulador
	SimConfig     http.HandlerFunc
	SimValidation http.HandlerFunc

	// Construtor de gráficos
	Datasets http.HandlerFunc
	Records  http.HandlerFunc
	Chart    http.HandlerFunc
	CSV      http.HandlerFunc

	// Motor de missão (modo história)
	MissionPresets  http.HandlerFunc
	MissionSimulate http.HandlerFunc
}

// Routes devolve o roteador com todas as rotas, já com CORS.
func (a *Api) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.Health)
	mux.HandleFunc("GET /api/meta", a.Meta)
	mux.HandleFunc("GET /api/investigations", a.Investigations)
	mux.HandleFunc("GET /api/investigations/{id}", a.Investigation)
	mux.HandleFunc("GET /api/tests", a.Tests)
	mux.HandleFunc("GET /api/tests/{id}", a.Test)
	mux.HandleFunc("GET /api/external", a.External)

	mux.HandleFunc("POST /api/verdict", a.Verdict)
	mux.HandleFunc("POST /api/rank", a.Rank)
	mux.HandleFunc("POST /api/next-tests", a.NextTests)
	mux.HandleFunc("POST /api/graph", a.Graph)
	mux.HandleFunc("GET /api/insights", a.Insights)

	mux.HandleFunc("POST /api/ask", a.Ask)
	mux.HandleFunc("POST /api/ask/stream", a.AskStream)
	mux.HandleFunc("POST /api/search", a.Search)
	mux.HandleFunc("POST /api/scenario/infer", a.Infer)

	mux.HandleFunc("GET /api/flammability", a.Flammability)
	mux.HandleFunc("POST /api/flammability/assess", a.Assess)
	mux.HandleFunc("GET /api/flammability/queue", a.Queue)

	mux.HandleFunc("POST /api/predict/burn-rate", a.BurnRate)
	mux.HandleFunc("POST /api/predict/spread", a.Spread)
	mux.HandleFunc("POST /api/predict/thermal", a.Thermal)

	mux.HandleFunc("GET /api/sim/config", a.SimConfig)
	mux.HandleFunc("GET /api/sim/validation", a.SimValidation)

	mux.HandleFunc("GET /api/datasets", a.Datasets)
	mux.HandleFunc("GET /api/datasets/{id}/records", a.Records)
	mux.HandleFunc("POST /api/datasets/{id}/chart", a.Chart)
	mux.HandleFunc("GET /api/datasets/{id}/csv", a.CSV)

	mux.HandleFunc("GET /api/missions/presets", a.MissionPresets)
	mux.HandleFunc("POST /api/missions/simulations", a.MissionSimulate)
	return zweb.Cors(zweb.Recover(mux))
}
