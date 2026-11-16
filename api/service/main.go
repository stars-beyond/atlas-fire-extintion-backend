// Atlas de Extinção · API
//
// Lê o banco carregado pela ingestão (Postgres + pgvector) e responde: cobertura de evidência de um cenário,
// ranking de investigações, busca híbrida, previsão dentro da faixa validada e respostas com citação.
//
// Este arquivo só monta as peças: configuração → gateways → casos de uso (domain/) → handlers (service/api/v1).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	domainask "atlas-api/domain/ask"
	domaincatalog "atlas-api/domain/catalog"
	domaindatasets "atlas-api/domain/datasets"
	"atlas-api/domain/entities"
	domainevidence "atlas-api/domain/evidence"
	domainflam "atlas-api/domain/flammability"
	domainmission "atlas-api/domain/mission"
	domainpredict "atlas-api/domain/predict"
	"atlas-api/domain/search"
	domainsim "atlas-api/domain/sim"
	"atlas-api/extensions/zpostgres"
	"atlas-api/gateways/embedder"
	"atlas-api/gateways/externalfile"
	"atlas-api/gateways/llm"
	"atlas-api/gateways/mission"
	"atlas-api/gateways/postgres"
	"atlas-api/gateways/rag"
	"atlas-api/gateways/simfile"
	"atlas-api/gateways/thermal"
	v1 "atlas-api/service/api/v1"
	hdlask "atlas-api/service/api/v1/ask"
	hdlcatalog "atlas-api/service/api/v1/catalog"
	hdldatasets "atlas-api/service/api/v1/datasets"
	hdlevidence "atlas-api/service/api/v1/evidence"
	hdlflam "atlas-api/service/api/v1/flammability"
	hdlmission "atlas-api/service/api/v1/mission"
	hdlpredict "atlas-api/service/api/v1/predict"
	hdlsim "atlas-api/service/api/v1/sim"
	"atlas-api/service/configs"
)

func main() {
	if err := run(configs.Load()); err != nil {
		log.Fatal(err)
	}
}

func run(cfg configs.Config) error {
	ctx := context.Background()

	// --- dependências ---
	pool, err := zpostgres.Connect(ctx, cfg.Database.URL)
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := &postgres.Repo{DB: pool}
	// o acervo só existe depois da ingestão: espera até ~2 minutos na subida do compose
	cat, err := zpostgres.Retry(60, 2*time.Second, "o banco", func() (*entities.Catalog, error) { return repo.LoadCatalog(ctx) })
	if err != nil {
		return errors.New("database unavailable: " + err.Error())
	}

	model := llm.New(llm.Options{AnthropicKey: cfg.LLM.AnthropicKey, AnthropicModel: cfg.LLM.AnthropicModel,
		OpenAIKey: cfg.LLM.OpenAIKey, OpenAIModel: cfg.LLM.OpenAIModel, OpenAIBase: cfg.LLM.OpenAIBaseURL})
	emb := &embedder.Client{URL: cfg.Services.EmbedderURL}
	ragClient := rag.New(cfg.Services.RagURL) // nil = RAG desligado (Status responde "não pronto")
	// ensaios de fora do PSI (ZARM): opcionais, e nunca entram no nível de evidência
	if ext, err := externalfile.Load(cfg.External.Path); err != nil {
		log.Printf("evidência de fora da NASA ignorada: %v", err)
	} else if ext != nil {
		cat.External = ext
		log.Printf("fora da NASA: %d ensaios de %s", len(ext.Tests), ext.Name)
	}
	files := simfile.Files{ConfigPath: cfg.Sim.ConfigPath, ValidationPath: cfg.Sim.ValidationPath, SpreadPath: cfg.Sim.SpreadPath, SpreadCheckPath: cfg.Sim.SpreadCheck}

	log.Printf("acervo: %d investigações, %d testes (%d com O₂ e pressão); IA: %s; vetores: pgvector",
		len(cat.Invs), len(cat.Tests), cat.Plot, model.Name())

	// --- casos de uso ---
	searchUC := &search.HybridSearchUseCase{Embedder: emb, Chunks: repo}
	inferUC := &domainask.InferScenarioUseCase{LLM: model}
	askUC := &domainask.AskUseCase{Catalog: cat, LLM: model, Embedder: emb, Search: searchUC, Rag: ragClient, Infer: inferUC,
		RagModel: cfg.Services.RagModel}

	thermalUC := &domainpredict.PredictThermalUseCase{Files: files, Model: &thermal.Client{URL: cfg.Services.EmbedderURL}}
	// os conjuntos do construtor de gráficos são montados uma vez (no primeiro pedido) e divididos pelos quatro casos de uso
	sets := domaindatasets.NewStore(cat)
	// o motor de missão roda no mesmo serviço Python dos embeddings e da câmera térmica
	missionEngine := &mission.Client{URL: cfg.Services.EmbedderURL}

	api := v1.Api{
		Health:         hdlcatalog.HealthHandler(),
		Meta:           hdlcatalog.MetaHandler(&domaincatalog.GetMetaUseCase{Catalog: cat, LLM: model, Rag: ragClient}),
		Investigations: hdlcatalog.InvestigationsHandler(&domaincatalog.ListInvestigationsUseCase{Catalog: cat}),
		Investigation:  hdlcatalog.InvestigationHandler(&domaincatalog.GetInvestigationUseCase{Catalog: cat, Repo: repo}),
		Tests:          hdlcatalog.TestsHandler(&domaincatalog.ListTestsUseCase{Catalog: cat}),
		Test:           hdlcatalog.TestHandler(&domaincatalog.GetTestUseCase{Catalog: cat, Repo: repo}),
		External:       hdlcatalog.ExternalHandler(&domaincatalog.ListExternalUseCase{Catalog: cat}),

		Verdict:   hdlevidence.VerdictHandler(&domainevidence.GetVerdictUseCase{Catalog: cat}),
		Rank:      hdlevidence.RankHandler(&domainevidence.RankUseCase{Catalog: cat, Embedder: emb}),
		NextTests: hdlevidence.NextTestsHandler(&domainevidence.NextTestsUseCase{Catalog: cat}, domainevidence.NextTestsMethod),
		Graph:     hdlevidence.GraphHandler(&domainevidence.GraphUseCase{Catalog: cat}),
		Insights:  hdlevidence.InsightsHandler(&domainevidence.InsightsUseCase{Catalog: cat, Metrics: repo}),

		Ask:       hdlask.AskHandler(askUC),
		AskStream: hdlask.AskStreamHandler(askUC),
		Search:    hdlask.SearchHandler(searchUC),
		Infer:     hdlask.InferHandler(inferUC),

		Flammability: hdlflam.FlammabilityHandler(&domainflam.GetFlammabilityUseCase{Catalog: cat}),
		Assess:       hdlflam.AssessHandler(&domainflam.AssessUseCase{Catalog: cat}),
		Queue:        hdlflam.QueueHandler(&domainflam.GetQueueUseCase{Catalog: cat, Spread: files}),

		BurnRate: hdlpredict.BurnRateHandler(&domainpredict.PredictBurnRateUseCase{Catalog: cat}),
		Spread:   hdlpredict.SpreadHandler(&domainpredict.PredictSpreadUseCase{Files: files}),
		Thermal:  hdlpredict.ThermalHandler(thermalUC),

		SimConfig:     hdlsim.ConfigHandler(&domainsim.GetConfigUseCase{Catalog: cat, Files: files}),
		SimValidation: hdlsim.ValidationHandler(&domainsim.GetValidationUseCase{Catalog: cat, Files: files}),

		Datasets: hdldatasets.DatasetsHandler(&domaindatasets.ListDatasetsUseCase{Store: sets}, domaindatasets.Note),
		Records:  hdldatasets.RecordsHandler(&domaindatasets.GetRecordsUseCase{Store: sets}),
		Chart:    hdldatasets.ChartHandler(&domaindatasets.BuildChartUseCase{Store: sets}),
		CSV:      hdldatasets.CSVHandler(&domaindatasets.ExportCSVUseCase{Store: sets}),

		MissionPresets:  hdlmission.PresetsHandler(&domainmission.GetPresetsUseCase{Engine: missionEngine}),
		MissionSimulate: hdlmission.SimulateHandler(&domainmission.SimulateUseCase{Engine: missionEngine}),
	}

	// --- servidor ---
	// Sem WriteTimeout: /api/ask/stream fica aberto enquanto o RAG escreve.
	srv := &http.Server{Addr: ":" + cfg.Server.Port, Handler: api.Routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	errc := make(chan error, 1)
	go func() {
		log.Printf("Atlas API em %s", srv.Addr)
		errc <- srv.ListenAndServe()
	}()
	// A câmera térmica roda as redes a cada ar novo (alguns segundos). Os habitats prontos e o ensaio de
	// comparação são pedidos já na subida; o serviço Python guarda o resultado, e a tela os recebe na hora.
	go func() {
		n := 0
		// o serviço Python pode ainda estar carregando: tenta o primeiro filme por até um minuto
		first := domainpredict.ThermalIn{Scenario: entities.Scenario{O2: 0.21, P: 1, Family: "solido", Material: "pmma"}}
		for i := 0; i < 12 && thermalUC.Predict(ctx, first)["available"] != true; i++ {
			time.Sleep(5 * time.Second)
		}
		for _, h := range entities.Habitats {
			in := domainpredict.ThermalIn{Scenario: entities.Scenario{O2: h.O2, P: h.P, G: h.G, Family: "solido", Material: "pmma"}}
			if out := thermalUC.Predict(ctx, in); out["available"] == true {
				n++
			}
		}
		if out := thermalUC.Predict(ctx, domainpredict.ThermalIn{Compare: true}); out["available"] == true {
			n++
		}
		log.Printf("câmera térmica: %d filmes prontos em cache", n)
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errc:
		return err
	case s := <-stop:
		log.Printf("desligando (%s)", s)
		// Termina o que já está em andamento antes de sair: no blue/green a cor antiga é desligada logo depois da
		// troca, e uma resposta em streaming pode durar até 3 min (teto de domain/ask, Budgets.Stream). O compose
		// espera 200 s (api.compose.yml).
		c, cancel := context.WithTimeout(context.Background(), 190*time.Second)
		defer cancel()
		return srv.Shutdown(c)
	}
}
