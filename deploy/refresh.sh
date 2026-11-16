#!/usr/bin/env bash
# Atualização do acervo a partir do PSI. O timer do systemd (atlas-refresh.timer) chama este script uma vez por dia.
#
#   deploy/refresh.sh            # busca novidades no PSI e, se algo mudou, refaz o acervo, atualiza o RAG e a API
#   deploy/refresh.sh --force    # refaz o acervo mesmo sem novidade
#   LOCAL=1 deploy/refresh.sh    # na sua máquina (make refresh): docker-compose.yml puro, reinicia a API local
#
# Fluxo: roda a ingestão (docker compose run ingest). Ela renova os catálogos vencidos, baixa arquivos novos ou
# substituídos e compara a impressão digital das entradas com a do acervo publicado:
#   - igual    → sai com 3 e nada muda (barato: algumas requisições ao PSI);
#   - diferente → monta o acervo novo no schema atlas_build e troca com o publicado numa transação (a API segue
#                respondendo com o antigo enquanto isso) → este script sobe a API em blue/green (a cor nova carrega o
#                acervo novo e só então recebe o tráfego; a API no ar não reinicia) → roda o rag-index (PDFs novos
#                entram no RAG da equipe) e reinicia o RAG.
# Usa a mesma trava do deploy.sh: os dois nunca rodam juntos.

main() {
  set -Eeuo pipefail
  # shellcheck source=deploy/lib.sh
  source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
  atlas_defaults
  local rc=0 local_mode="${LOCAL:-}"
  local -a extra=(-e ATLAS_UNCHANGED_EXIT=3)
  [[ "${1:-}" == "--force" ]] && extra+=(-e ATLAS_FORCE=1)
  if [[ -n "$local_mode" ]]; then
    stack() { docker compose --project-directory "$APP_DIR" -f "$APP_DIR/docker-compose.yml" "$@"; }
    LOCK=/tmp/atlas-refresh.lock
  fi

  cd "$APP_DIR"
  if command -v flock >/dev/null; then  # no macOS (make refresh local) não há flock: a ingestão já se trava no banco
    mkdir -p "$(dirname "$LOCK")"
    exec 9>"$LOCK"
    if ! flock -w 3600 9; then
      log "FALHOU: outro deploy/atualização segura a trava há mais de 1 h"
      exit 1
    fi
  fi

  log "procurando novidades no PSI"
  stack run --rm -T "${extra[@]}" ingest </dev/null || rc=$?
  case "$rc" in
    3) log "nada novo no PSI; acervo mantido"; exit 0 ;;
    4) log "FALHOU: o acervo novo encolheu demais e foi recusado; o publicado continua (ATLAS_FORCE=1 se a queda for real)"; exit 4 ;;
    0) log "acervo atualizado" ;;
    *) log "FALHOU: ingestão saiu com $rc; o acervo anterior continua publicado"; exit "$rc" ;;
  esac

  # Logo depois da troca do acervo: a API guarda o catálogo em memória ao subir, e até ela recarregar o catálogo
  # dela e o banco discordam (detalhes de testes e investigações vazios). Por isso ela vem antes do RAG.
  if [[ -n "$local_mode" ]]; then
    stack restart api
    log "API local reiniciada com o acervo novo"
  else
    # uma cor nova com o acervo novo, verificada antes de receber o tráfego; a API no ar não reinicia
    blue_green || log "aviso: a API segue no último build, com o catálogo anterior, até o próximo deploy"
  fi

  # PDFs novos do PSI entram também no RAG da equipe (ponte rag/atlas_pg/bridge.py): só os trechos novos geram
  # embedding. --no-deps: sem ele o compose roda a ingestão de novo (rag-index depende dela).
  log "atualizando o acervo do RAG da equipe"
  stack run --rm -T --no-deps rag-index </dev/null || log "aviso: rag-index falhou; o RAG segue com o acervo anterior"
  stack restart rag || true
  log "ok: acervo, API e RAG atualizados"
}

main "$@"
