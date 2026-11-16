.PHONY: up down logs reingest refresh ps

up:        ## sobe tudo (primeira vez: baixa dados do PSI e gera os embeddings)
	docker compose up --build -d

down:      ## para os serviços (os dados ficam no volume)
	docker compose down

logs:
	docker compose logs -f --tail 50

reingest:  ## refaz a ingestão do zero a partir do PSI/cache
	ATLAS_FORCE=1 docker compose run --rm ingest && docker compose restart api

refresh:   ## busca novidades no PSI; só refaz o acervo (e reinicia a API) se algo mudou
	LOCAL=1 deploy/refresh.sh

ps:
	docker compose ps
