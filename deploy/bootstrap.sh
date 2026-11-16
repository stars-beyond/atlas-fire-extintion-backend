#!/usr/bin/env bash
# Prepara uma VPS Debian/Ubuntu para o backend do Atlas. Roda como root, pode ser repetido.
#
# Da sua máquina (na primeira vez o SSH ainda está na 22; depois, use -p 665):
#   ssh root@IP 'REPO_URL=git@github.com:ORG/REPO.git bash -s' < deploy/bootstrap.sh
#
# Variáveis: REPO_URL (obrigatória), BRANCH=main, APP_DIR=/opt/atlas, POLL_INTERVAL=30s, SSH_PORT=665,
#            REFRESH_AT="*-*-* 04:00:00" (atualização diária do acervo a partir do PSI, horário do servidor)
#
# 1ª vez: instala Docker, git e firewall, cria a deploy key e para, mostrando a chave pública.
#         Cadastre-a no GitHub (repositório → Settings → Deploy keys, só leitura) e rode de novo.
# 2ª vez: clona, gera um certificado provisório, instala os timers e faz o primeiro deploy.

set -Eeuo pipefail
REPO_URL="${REPO_URL:?defina REPO_URL, ex.: git@github.com:stars-beyond/atlas-fire-extintion-backend.git}"
BRANCH="${BRANCH:-main}"
APP_DIR="${APP_DIR:-/opt/atlas}"
POLL_INTERVAL="${POLL_INTERVAL:-30s}"
REFRESH_AT="${REFRESH_AT:-*-*-* 04:00:00}"
SSH_PORT="${SSH_PORT:-665}"
KEY=/root/.ssh/atlas_deploy
log() { printf '\n== %s\n' "$*"; }

[[ $EUID -eq 0 ]] || { echo "rode como root" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive

log "pacotes base"
apt-get update -qq
apt-get install -y -qq ca-certificates curl git openssl ufw >/dev/null

if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
  log "Docker (repositório oficial)"
  . /etc/os-release
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/${ID} ${VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null
  systemctl enable --now docker
fi
docker --version; docker compose version

log "SSH na porta $SSH_PORT (a 22 só fecha depois que a nova responde)"
printf '# porta do SSH (atlas)\nPort %s\n' "$SSH_PORT" > /etc/ssh/sshd_config.d/10-atlas-port.conf
[[ "$SSH_PORT" == 22 ]] || printf 'Port 22\n' >> /etc/ssh/sshd_config.d/10-atlas-port.conf
# Login só por chave. Só entra se o root já tem uma chave cadastrada: sem ela, desligar a senha trancaria a VPS.
if [[ -s /root/.ssh/authorized_keys ]]; then
  printf '# login só por chave (atlas)\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\n' \
    > /etc/ssh/sshd_config.d/11-atlas-keys-only.conf
else
  echo "aviso: o root não tem chave em /root/.ssh/authorized_keys; o login por senha continua ligado."
  echo "       Cadastre a sua chave (ssh-copy-id) e rode este script de novo."
fi
sshd -t && systemctl reload ssh

log "firewall: só SSH ($SSH_PORT) e HTTPS"
ufw allow "$SSH_PORT/tcp" comment ssh >/dev/null
ufw allow 443/tcp >/dev/null
ufw --force enable >/dev/null
ufw status | head -10

log "sem LLMNR/mDNS escutando na rede"
mkdir -p /etc/systemd/resolved.conf.d
printf '[Resolve]\nLLMNR=no\nMulticastDNS=no\n' > /etc/systemd/resolved.conf.d/atlas.conf
systemctl restart systemd-resolved

log "deploy key do GitHub"
mkdir -p /root/.ssh && chmod 700 /root/.ssh
[[ -f "$KEY" ]] || ssh-keygen -q -t ed25519 -N "" -C "atlas-deploy@$(hostname)" -f "$KEY"
ssh-keyscan -t ed25519 github.com 2>/dev/null >> /root/.ssh/known_hosts
sort -u -o /root/.ssh/known_hosts /root/.ssh/known_hosts
if ! grep -q "Host github.com" /root/.ssh/config 2>/dev/null; then
  printf 'Host github.com\n  IdentityFile %s\n  IdentitiesOnly yes\n' "$KEY" >> /root/.ssh/config
  chmod 600 /root/.ssh/config
fi

if [[ "$REPO_URL" == git@* ]] && ! GIT_SSH_COMMAND="ssh -o BatchMode=yes" git ls-remote "$REPO_URL" HEAD >/dev/null 2>&1; then
  echo
  echo "O GitHub ainda não aceita esta chave. Cadastre a linha abaixo em"
  echo "  repositório → Settings → Deploy keys → Add deploy key (sem marcar 'Allow write access')"
  echo
  cat "$KEY.pub"
  echo
  echo "Depois rode este script de novo."
  exit 2
fi

log "código em $APP_DIR ($BRANCH)"
if [[ ! -d "$APP_DIR/.git" ]]; then
  git clone --branch "$BRANCH" "$REPO_URL" "$APP_DIR"
else
  git -C "$APP_DIR" remote set-url origin "$REPO_URL"
fi
cd "$APP_DIR"
# o deploy.sh roda a partir do checkout: ele precisa ser o da branch, não o que já estava no disco
git fetch --quiet origin "$BRANCH" && git checkout --quiet -B "$BRANCH" "origin/$BRANCH"
if [[ ! -f .env ]]; then
  install -m 600 /dev/null .env
  echo "criei um .env vazio (sem chaves de IA); copie o seu .env para $APP_DIR/.env (variáveis: deploy/README.md)"
fi
chmod 600 .env               # chaves de IA: só o root lê
mkdir -p /etc/atlas/active   # cor ativa da API (blue/green), escrita pelo deploy

log "certificado de origem"
mkdir -p deploy/certs
if [[ ! -f deploy/certs/origin.pem ]]; then
  # provisório (Cloudflare em SSL "Full"). Troque pelo Origin Certificate do Cloudflare e use "Full (strict)".
  openssl req -x509 -nodes -newkey rsa:2048 -days 825 -subj "/CN=atlas-origin" \
    -keyout deploy/certs/origin.key -out deploy/certs/origin.pem 2>/dev/null
  echo "gerei um certificado autoassinado provisório em deploy/certs/"
fi
chmod 600 deploy/certs/origin.key

log "timers: deploy (a cada $POLL_INTERVAL) e atualização do acervo pelo PSI ($REFRESH_AT)"
for u in atlas-deploy.service atlas-deploy.timer atlas-refresh.service atlas-refresh.timer; do
  sed -e "s#@APP_DIR@#$APP_DIR#g" -e "s#@BRANCH@#$BRANCH#g" -e "s#@INTERVAL@#$POLL_INTERVAL#g" \
    -e "s#@REFRESH_AT@#$REFRESH_AT#g" "deploy/systemd/$u" > "/etc/systemd/system/$u"
done
systemctl daemon-reload

log "primeiro deploy (build + ingestão: pode levar 10 a 15 min)"
# </dev/null: com "bash -s" o resto deste script chega pela entrada padrão, e o docker compose a consumiria
APP_DIR="$APP_DIR" BRANCH="$BRANCH" deploy/deploy.sh --force </dev/null
systemctl enable --now atlas-deploy.timer atlas-refresh.timer
systemctl list-timers 'atlas-*' --no-pager | head -4

if [[ "$SSH_PORT" != 22 ]]; then
  echo
  echo "Teste agora: ssh -p $SSH_PORT root@$(hostname -I | awk '{print $1}')"
  echo "Funcionou? Feche a 22:  sed -i '/^Port 22$/d' /etc/ssh/sshd_config.d/10-atlas-port.conf && ufw delete allow OpenSSH; ufw delete allow 22/tcp; systemctl reload ssh"
fi
echo
echo "Pronto. Logs do deploy:  journalctl -u atlas-deploy -f"
echo "Estado dos serviços:     cd $APP_DIR && docker compose -f docker-compose.yml -f deploy/compose.prod.yml ps"
echo "Cor da API no ar:        $APP_DIR/deploy/switch.sh --status"
