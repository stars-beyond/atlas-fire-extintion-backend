#!/usr/bin/env bash
# Deploy contínuo da API de produção na VPS. O timer do systemd (atlas-deploy.timer) chama este script a cada 30 s;
# ele só faz algo quando há um commit novo liberado para deploy no GitHub.
#
#   deploy/deploy.sh            # verifica e, se houver commit novo liberado, atualiza e sobe
#   deploy/deploy.sh --force    # sobe a ponta da branch (main) agora, sem esperar o CI, mesmo sem mudança
#
# O que é "liberado": a branch `release`, que só o CI avança, depois de compilar, testar e validar o deploy
# (.github/workflows/ci.yml, job release). Um push na main que o CI reprova nunca chega à VPS. Enquanto a branch
# `release` não existir no GitHub (CI ainda não rodou com este esquema), o script segue a main, como antes.
# DEPLOY_GATE= (vazio) desliga a trava; DEPLOY_GATE=outra-branch troca o nome.
#
# Fluxo: git fetch → compara com o último commit tentado → git reset --hard no commit novo →
# banco/embedder/ingestão/RAG/nginx (docker compose up) → espera a ingestão terminar →
# API em blue/green (deploy/lib.sh): a cor nova sobe ao lado da que está no ar, é verificada, recebe o tráfego e é
# verificada de novo, pelo nginx e pelo caminho do visitante (Cloudflare). Qualquer erro deixa (ou devolve) o tráfego
# no último build que funcionava e devolve o checkout ao último deploy bom: o nginx e os arquivos de data/seed que a
# API no ar lê voltam junto. Banco, embedder e RAG já reconstruídos ficam na versão nova: mudanças neles precisam
# continuar funcionando com a API anterior.
# Um commit que falhou não é tentado de novo a cada volta: o próximo deploy acontece no próximo commit, ou com --force.
#
# .env, data/ e deploy/certs/ ficam fora do git e não são tocados pelo reset.

main() {
  set -Eeuo pipefail
  # shellcheck source=deploy/lib.sh
  source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
  atlas_defaults
  local branch force state local_sha remote_sha last ingest rc
  branch="${BRANCH:-main}"
  force="${1:-}"
  state="$APP_DIR/.deploy"

  mkdir -p "$state"
  exec 9>"$LOCK"
  flock -n 9 || exit 0          # já tem um deploy ou atualização rodando
  cd "$APP_DIR"

  git fetch --quiet origin "$branch"
  # A trava do CI: se a branch `release` existe no GitHub, o deploy segue ela, e não a ponta da main.
  local target="origin/$branch" gate="${DEPLOY_GATE-release}" seen=0 tip
  tip="$(git rev-parse "origin/$branch")"
  if [[ "$force" != "--force" && -n "$gate" ]]; then
    git ls-remote --exit-code --heads origin "$gate" >/dev/null 2>&1 || seen=$?
    case "$seen" in
      0)
        git fetch --quiet origin "+refs/heads/$gate:refs/remotes/origin/$gate"
        if ! git merge-base --is-ancestor "origin/$gate" "origin/$branch"; then
          # só acontece se alguém reescreveu a main ou escreveu na release à mão: não sobe nada sozinho
          if [[ "$(cat "$state/waiting" 2>/dev/null || true)" != "fora:$tip" ]]; then
            log "aviso: a branch $gate não faz parte da $branch; nada a fazer (deploy.sh --force sobe a $branch)"
            echo "fora:$tip" > "$state/waiting"
          fi
          exit 0
        fi
        target="origin/$gate" ;;
      2) ;;        # a branch do CI ainda não existe: segue a main, como antes
      *) exit 0 ;; # GitHub não respondeu: tenta de novo na próxima volta
    esac
  fi
  remote_sha="$(git rev-parse "$target")"
  local_sha="$(git rev-parse HEAD)"
  last="$(cat "$state/last-attempt" 2>/dev/null || true)"

  if [[ "$tip" != "$remote_sha" && "$(cat "$state/waiting" 2>/dev/null || true)" != "$tip" ]]; then
    log "a $branch está em ${tip:0:7}, esperando o CI liberar ($gate em ${remote_sha:0:7})"
    echo "$tip" > "$state/waiting"
  fi
  if [[ "$force" != "--force" && "$remote_sha" == "$last" ]]; then
    exit 0                      # nada novo desde o último deploy (com sucesso ou não)
  fi
  # Nunca anda para trás sozinho: depois de um --force, a release fica atrás do que está no ar até o CI alcançar.
  local good
  good="$(cat "$state/last-ok" 2>/dev/null || true)"
  if [[ "$force" != "--force" && -n "$good" ]] && git merge-base --is-ancestor "$remote_sha" "$good" 2>/dev/null; then
    exit 0
  fi

  # O site respondia pelo caminho do visitante antes de mexer em qualquer coisa? Só então essa conferência vale
  # como critério depois (uma VPS sem saída para a internet, ou o Cloudflare fora do ar, não reprova o deploy).
  EDGE_BASELINE=0
  if edge_ready; then EDGE_BASELINE=1; fi
  export EDGE_BASELINE

  log "deploy ${local_sha:0:7} → ${remote_sha:0:7} ($(git log -1 --format=%s "$remote_sha"))"
  echo "$remote_sha" > "$state/last-attempt"
  git reset --hard --quiet "$remote_sha"

  if [[ ! -f .env ]]; then
    log "aviso: sem .env; subindo sem chaves de IA (copie o .env para $APP_DIR)"
    touch .env
  fi
  chmod 600 .env                # chaves de IA: só o root lê

  # Devolve o checkout ao último deploy bom. Com ele voltam a configuração do nginx (e as portas e volumes do
  # contêiner, se o compose mudou) e os arquivos de data/seed que a API no ar lê a cada pedido.
  restore() {
    local good
    good="$(cat "$state/last-ok" 2>/dev/null || true)"
    [[ -n "$good" && "$good" != "$(git rev-parse HEAD)" ]] || return 0
    log "voltando o checkout para ${good:0:7}, o último deploy bom"
    git reset --hard --quiet "$good" || { log "aviso: não consegui voltar o checkout"; return 0; }
    stack up -d nginx >/dev/null 2>&1 || log "aviso: o nginx não voltou à configuração anterior"
    reload_nginx || true
  }

  # 1) serviços com estado e nginx. A API no ar não faz parte deste projeto, então isto não a derruba.
  if ! stack up -d --build "${STACK_SERVICES[@]}"; then
    log "FALHOU: docker compose up dos serviços (commit ${remote_sha:0:7}); a API no ar continua"
    restore
    exit 1
  fi
  # o nginx pode ter sido recriado com portas, volumes ou regras novas: se o site respondia e parou, volta já
  if [[ "$EDGE_BASELINE" -eq 1 ]] && edge_down; then
    log "FALHOU: depois de subir os serviços o site parou de responder pelo Cloudflare (commit ${remote_sha:0:7})"
    restore
    exit 1
  fi

  # 2) a ingestão monta o acervo ao lado do publicado e troca numa transação; espera ela acabar
  ingest="$(stack ps -aq ingest)"
  rc=""
  for _ in $(seq 1 540); do       # até 45 min (primeira vez: 10 a 15 min; depois, segundos)
    if [[ "$(docker inspect -f '{{.State.Status}}' "$ingest" 2>/dev/null)" == exited ]]; then
      rc="$(docker inspect -f '{{.State.ExitCode}}' "$ingest")"
      break
    fi
    sleep 5
  done
  if [[ "$rc" == 4 ]]; then
    log "aviso: a ingestão recusou um acervo que encolheu demais; o publicado continua e o deploy do código segue"
  elif [[ "$rc" != 0 && "$rc" != 3 ]]; then
    log "FALHOU: ingestão ${rc:+saiu com $rc}${rc:-não terminou em 45 min}; o acervo publicado e a API no ar continuam"
    stack logs --tail 30 ingest >&2 || true
    restore
    exit 1
  fi

  # 3) API em blue/green, com volta automática para o último build se der erro
  if ! blue_green; then
    restore
    exit 1
  fi
  echo "$remote_sha" > "$state/last-ok"
  log "ok: ${remote_sha:0:7} no ar"
  docker image prune -f >/dev/null 2>&1 || true
}

# main é lido inteiro antes de rodar: o git reset pode trocar este arquivo no meio do deploy sem quebrar nada
main "$@"
exit
