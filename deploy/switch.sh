#!/usr/bin/env bash
# Troca a cor da API que recebe o tráfego (volta para o build anterior).
#
#   deploy/switch.sh            # volta para a outra cor (o build anterior; religa o container se estiver parado)
#   deploy/switch.sh blue       # aponta para uma cor específica
#   deploy/switch.sh --status   # mostra qual cor está no ar e quais estão prontas
#
# Só troca se a cor de destino estiver de pé e servindo o acervo. Depois de um deploy bem-sucedido a cor anterior fica
# parada: aqui ela é religada e verificada antes de receber o tráfego (alguns segundos).

main() {
  set -Eeuo pipefail
  # shellcheck source=deploy/lib.sh
  source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
  atlas_defaults
  local live target c
  live="$(active_color)"
  if [[ "${1:-}" == --status ]]; then
    echo "no ar: ${live:-esquema antigo (api)}"
    for c in blue green; do
      if color_ready "$c"; then echo "  $c: pronta"; else echo "  $c: parada ou sem acervo"; fi
    done
    exit 0
  fi
  target="${1:-$(other_color "${live:-green}")}"
  [[ "$target" == blue || "$target" == green ]] || { echo "cor deve ser blue ou green" >&2; exit 2; }
  if command -v flock >/dev/null; then   # no macOS não há flock (só nos testes locais)
    exec 9>"$LOCK"
    flock -w 60 9 || { log "FALHOU: um deploy está rodando"; exit 1; }
  fi
  if ! color_ready "$target"; then
    log "a cor $target está parada; religando"
    api_color "$target" start >/dev/null 2>&1 || true
    local ok=0
    for _ in $(seq 1 30); do
      if color_ready "$target"; then ok=1; break; fi
      sleep 2
    done
    if [[ $ok -ne 1 ]]; then
      log "FALHOU: a cor $target não ficou pronta; nada mudou (no ar: ${live:-esquema antigo})"
      exit 1
    fi
  fi
  point_to "$target"
  log "ok: tráfego na cor $target (antes: ${live:-esquema antigo})"
}

main "$@"
