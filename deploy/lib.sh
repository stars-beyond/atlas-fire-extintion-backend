# shellcheck shell=bash
# Funções comuns de deploy.sh, refresh.sh e switch.sh: blue/green da API de produção. Lido com `source`.
#
# A API roda em duas cores, blue e green, cada uma no seu projeto do Docker Compose (deploy/api.compose.yml),
# ligadas à rede do backend, onde atendem por api-blue e api-green. Um deploy:
#   1. sobe a cor parada com o build novo e espera ela responder com o acervo completo;
#   2. troca o nginx para ela (`nginx -s reload`, sem cortar conexões);
#   3. confere de novo por 30 s, pelo nginx e pelo caminho do visitante (DNS público → Cloudflare → nginx). Se algo
#      falhar, volta sozinho para a cor anterior, que é o último build que estava no ar e ainda está de pé;
#   4. se os 30 s passaram sem erro, desliga a cor anterior. Ela termina as requisições em andamento antes de sair
#      (até ~3 min) e fica parada, pronta para deploy/switch.sh religar se for preciso voltar.
# Se a cor nova nem passar no passo 1, o nginx não é tocado: quem está no ar continua no ar.
#
# A cor ativa fica em /etc/atlas/active/api.conf (fora do git, sobrevive aos resets). Sem esse arquivo o nginx usa
# http://api:8787, a API do esquema antigo, o que mantém a produção no ar durante a migração.

atlas_defaults() {
  APP_DIR="${APP_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
  STACK="${STACK:-atlas-fire-extintion-backend}"
  ACTIVE_DIR="${ACTIVE_DIR:-/etc/atlas/active}"
  LOCK="${LOCK:-/run/lock/atlas-deploy.lock}"   # a mesma trava do deploy.sh antigo: os dois nunca rodam juntos
  API_HOST="${API_HOST:-api.atlas-stars-beyond.tech}"
  # o que o deploy sobe no projeto do backend: tudo menos a API, que roda em blue/green
  # shellcheck disable=SC2034  # usado pelo deploy.sh
  STACK_SERVICES=(db embedder ingest rag-index rag nginx)
  export STACK ACTIVE_DIR
}

log() { printf '%s atlas-deploy: %s\n' "$(date '+%F %T')" "$*"; }

# banco, embedder, ingestão, RAG e nginx (o serviço "api" do docker-compose.yml fica de fora em produção)
stack() { docker compose -p "$STACK" --project-directory "$APP_DIR" -f "$APP_DIR/docker-compose.yml" -f "$APP_DIR/deploy/compose.prod.yml" "$@"; }
# uma cor da API: api_color blue up -d ...
api_color() { local c="$1"; shift; COLOR="$c" docker compose -p "$STACK-api-$c" --project-directory "$APP_DIR" -f "$APP_DIR/deploy/api.compose.yml" "$@"; }

active_color() { grep -oE 'api-(blue|green)' "$ACTIVE_DIR/api.conf" 2>/dev/null | head -1 | cut -d- -f2; }
other_color() { [[ "$1" == blue ]] && echo green || echo blue; }

# Um /api/meta de verdade: lista de ambientes e testes > 0. É o que o front precisa para abrir.
meta_ok() { grep -q '"habitats":\[{' <<<"$1" && ! grep -qE '"(tests|investigations)":0[,}]' <<<"$1"; }

# A cor responde direto (sem passar pelo nginx): /api/health ok e /api/meta completo.
color_ready() {
  local cid meta
  cid="$(api_color "$1" ps -q atlas-api 2>/dev/null)"
  [[ -n "$cid" ]] || return 1
  docker exec "$cid" wget -qO- -T 5 http://localhost:8787/api/health 2>/dev/null | grep -q '"ok":true' || return 1
  meta="$(docker exec "$cid" wget -qO- -T 10 http://localhost:8787/api/meta 2>/dev/null)" || return 1
  meta_ok "$meta"
}

# O mesmo, mas pelo caminho do público: nginx (HTTPS, Host da API) → cor ativa.
public_ready() {
  local meta
  meta="$(stack exec -T nginx wget --no-check-certificate -qO- -T 10 --header "Host: $API_HOST" https://127.0.0.1/api/meta 2>/dev/null)" || return 1
  meta_ok "$meta"
}

# O caminho inteiro do visitante: DNS público → Cloudflare → nginx → cor ativa. Pega o que a conferência de dentro
# do contêiner não vê: a lista de IPs aceitos, a porta publicada, o certificado.
edge_ready() {
  local meta
  command -v curl >/dev/null || return 1
  meta="$(curl -fsS -m 10 "https://$API_HOST/api/meta" 2>/dev/null)" || return 1
  meta_ok "$meta"
}

# O site saiu do ar para o visitante: 6 tentativas em ~12 s, todas com erro. Uma falha solta do Cloudflare não conta.
edge_down() {
  local _
  for _ in 1 2 3 4 5 6; do
    if edge_ready; then return 1; fi
    sleep 2
  done
  return 0
}

reload_nginx() { stack exec -T nginx nginx -t -q && stack exec -T nginx nginx -s reload; }

# Aponta o nginx para uma cor ("" = API do esquema antigo). Se o nginx recusar, volta o arquivo anterior.
point_to() {
  local color="$1" file="$ACTIVE_DIR/api.conf" prev
  mkdir -p "$ACTIVE_DIR"
  prev="$(cat "$file" 2>/dev/null || true)"
  printf '# cor ativa da API: %s (%s)\nset $api_upstream http://api-%s:8787;\n' "$color" "$(date '+%F %T')" "$color" > "$file.tmp"
  mv "$file.tmp" "$file"
  if ! reload_nginx; then
    log "FALHOU: o nginx recusou a configuração; voltando a anterior"
    if [[ -n "$prev" ]]; then printf '%s\n' "$prev" > "$file"; else rm -f "$file"; fi
    reload_nginx || true
    return 1
  fi
}

# Sobe a cor parada com o código atual, verifica, troca o tráfego, verifica de novo e, se der erro, volta.
blue_green() {
  local live next cid edge="${EDGE_BASELINE:-}"
  # O site respondia para o visitante antes do deploy? (o deploy.sh já mediu, antes de trocar o checkout; o
  # refresh.sh mede aqui). Só nesse caso a conferência pelo Cloudflare entra como critério.
  if [[ -z "$edge" ]]; then
    edge=0
    if edge_ready; then edge=1; fi
  fi
  live="$(active_color)"
  next="$(other_color "${live:-green}")"
  log "API: subindo a cor $next (no ar: ${live:-esquema antigo})"

  # nginx no ar e com a configuração deste commit (o nginx.conf pode ter mudado no git)
  stack up -d nginx >/dev/null
  reload_nginx || { log "FALHOU: configuração do nginx inválida neste commit; nada mudou"; return 1; }

  if ! api_color "$next" up -d --build --force-recreate --remove-orphans; then
    log "FALHOU: build/subida da cor $next; o último build continua no ar"
    return 1
  fi
  local ok=0
  for _ in $(seq 1 60); do
    if color_ready "$next"; then ok=1; break; fi
    sleep 3
  done
  if [[ $ok -ne 1 ]]; then
    log "FALHOU: a cor $next não respondeu com o acervo em 3 min; o último build continua no ar"
    api_color "$next" logs --tail 40 atlas-api >&2 || true
    api_color "$next" stop >/dev/null 2>&1 || true
    return 1
  fi

  point_to "$next" || return 1

  # confere por 30 s: a cor nova precisa aguentar o tráfego de verdade, pelo nginx e pelo caminho do visitante
  local fails=0 edge_fails=0
  for _ in $(seq 1 15); do
    public_ready || fails=$((fails + 1))
    if [[ "$edge" -eq 1 ]]; then edge_ready || edge_fails=$((edge_fails + 1)); fi
    sleep 2
  done
  # pelo Cloudflare, uma falha solta é ruído da rede; a maioria falhando é o site fora do ar para o visitante
  if [[ $edge_fails -gt 7 ]]; then
    log "a cor $next respondeu pelo nginx, mas o site falhou $edge_fails de 15 vezes pelo Cloudflare"
    fails=$((fails + edge_fails))
  fi
  if [[ $fails -gt 0 ]]; then
    if [[ -n "$live" ]] && color_ready "$live"; then
      log "ROLLBACK: a cor $next falhou $fails de 15 verificações pelo nginx; voltando para $live (último build)"
      point_to "$live" || true
    else
      log "ROLLBACK: a cor $next falhou $fails de 15 verificações; voltando para a API do esquema antigo"
      rm -f "$ACTIVE_DIR/api.conf"; reload_nginx || true
    fi
    api_color "$next" stop >/dev/null 2>&1 || true
    return 1
  fi
  log "ok: tráfego na cor $next, 30 s sem erro"
  # a cor anterior sai do ar: termina o que estava atendendo e para (o container fica, parado, para uma volta manual)
  if [[ -n "$live" ]]; then
    log "desligando a cor $live (build anterior); deploy/switch.sh religa se precisar voltar"
    api_color "$live" stop >/dev/null 2>&1 || log "aviso: a cor $live não parou; segue de pé"
  fi

  # esquema antigo: a API que vivia no projeto do backend sai depois que a nova já está no ar e conferida
  cid="$(docker ps -aq --filter "label=com.docker.compose.project=$STACK" --filter label=com.docker.compose.service=api)"
  if [[ -n "$cid" ]]; then
    log "migração: removendo a API do esquema antigo"
    docker rm -f $cid >/dev/null
  fi
}
