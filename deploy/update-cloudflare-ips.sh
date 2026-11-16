#!/usr/bin/env bash
# Regera deploy/nginx/snippets/cloudflare.conf com as faixas atuais do Cloudflare e recarrega o nginx.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
v4="$(curl -fsS https://www.cloudflare.com/ips-v4)"; v6="$(curl -fsS https://www.cloudflare.com/ips-v6)"
ips="$(printf '%s\n%s\n' "$v4" "$v6" | grep -E '^[0-9a-f:.]+/[0-9]+$')"
[[ $(wc -l <<<"$ips") -gt 10 ]] || { echo "lista do Cloudflare veio vazia; nada mudou" >&2; exit 1; }
{
  echo "# Faixas de IP do Cloudflare (https://www.cloudflare.com/ips). Atualize com deploy/update-cloudflare-ips.sh."
  echo "# 1) O IP real do visitante vem no cabeçalho CF-Connecting-IP."
  while read -r ip; do echo "set_real_ip_from $ip;"; done <<<"$ips"
  echo "real_ip_header CF-Connecting-IP;"
  echo
  echo "# 2) \$from_cf = 1 só quando a conexão veio do Cloudflare, ou de dentro do próprio contêiner do nginx (é assim que o"
  echo "#    deploy confere a API: deploy/lib.sh, public_ready). A rede interna do Docker (172.16.0.0/12) NÃO entra: uma"
  echo "#    conexão que chega de fora por IPv6 passa pelo docker-proxy e aparece com o endereço do gateway dessa rede."
  echo 'geo $realip_remote_addr $from_cf {'
  echo "  default 0;"
  echo "  127.0.0.0/8 1;"
  while read -r ip; do echo "  $ip 1;"; done <<<"$ips"
  echo "}"
  echo
  echo "# 3) \$from_local = 1 para a conferência do deploy, feita de dentro do contêiner."
  echo 'geo $realip_remote_addr $from_local {'
  echo "  default 0;"
  echo "  127.0.0.0/8 1;"
  echo "}"
} > deploy/nginx/snippets/cloudflare.conf
docker compose -f docker-compose.yml -f deploy/compose.prod.yml exec -T nginx nginx -s reload 2>/dev/null || true
echo "cloudflare.conf atualizado ($(wc -l <<<"$ips") faixas)"
