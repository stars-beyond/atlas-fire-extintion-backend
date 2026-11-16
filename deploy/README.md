# Deploy na VPS (blue/green, CI/CD)

```
atlas-stars-beyond.tech (Worker, só arquivos estáticos)
        │ o navegador chama a API direto (CORS)
        ▼
https://api.atlas-stars-beyond.tech (Cloudflare, nuvem laranja, SSL Full)
        │ só IPs do Cloudflare passam
        ▼
VPS :443 nginx ─► api-blue | api-green (cor ativa em /etc/atlas/active/api.conf) ─► db, embedder, rag
```

## CI/CD

- **CI** ([.github/workflows/ci.yml](../.github/workflows/ci.yml)): em todo PR e push em `main`/`dev`, compila e testa a
  API (Go), confere que o Python compila e não usa nomes que não existem, e valida o deploy: scripts (bash +
  shellcheck), arquivos do compose e `nginx -t` com a configuração real. Um job à parte (`scan`) roda formatação,
  `go test -race`, govulncheck, gitleaks e trivy: ele avisa com um X vermelho, mas não segura o deploy.
- **A trava**: quando os jobs `api`, `python` e `deploy` passam num push na `main`, o job `release` avança a branch
  **`release`** para aquele commit. É o único lugar que escreve nela.
- **CD**: um timer do systemd na VPS chama [deploy.sh](deploy.sh) a cada 30 s. Ele sobe o que está na `release`,
  em blue/green, sem tirar a API do ar. **Um push na `main` que o CI reprova nunca chega à produção**, e um push
  que passa chega alguns minutos depois (o tempo do CI). Enquanto a branch `release` não existir no GitHub, o script
  segue a `main`, como antes: a trava liga sozinha na primeira vez que o CI criar a branch.
- **Para valer contra uma conta comprometida** (e não só contra um commit quebrado), falta o que só se faz no GitHub,
  em Settings → Branches: proteger `main` (PR obrigatório, checks `API (Go)`, `Python` e `Deploy` obrigatórios, sem
  force-push) e proteger `release` (só o GitHub Actions escreve).

| Situação | O que fazer |
|---|---|
| Preciso subir agora, sem esperar o CI | Na VPS: `deploy/deploy.sh --force` (sobe a ponta da `main`) |
| O CI está fora do ar e nada sobe | O mesmo `--force`; ou `DEPLOY_GATE= deploy/deploy.sh` para uma volta sem a trava |
| Quero desligar a trava de vez | Apague a branch `release` no GitHub (o script volta a seguir a `main`) |
| O log diz "a branch release não faz parte da main" | A `main` foi reescrita. `git push -f origin main:release` depois de conferir, ou `--force` na VPS |

## Como um deploy acontece

1. `git fetch`; se a `release` (ou a `main`, sem a trava) tem commit novo, mede se o site responde para o visitante
   (pelo Cloudflare) e faz `git reset --hard` no commit.
2. `docker compose up -d --build` de banco, embedder, ingestão, RAG e nginx. A API não está nessa lista, então nada
   aqui a derruba. Se o site respondia antes e para de responder depois deste passo (porta, volume ou regra nova do
   nginx), o deploy para aqui e volta tudo.
3. Espera a ingestão terminar. Ela monta o acervo no schema `atlas_build` e troca com o publicado numa transação.
   Se falhar, o acervo publicado e a API no ar continuam.
4. **Blue/green** ([lib.sh](lib.sh)): sobe a cor parada com o build novo e espera ela responder `/api/health` **e**
   `/api/meta` com a lista de ambientes e testes > 0. Só então troca o nginx para ela (`nginx -s reload`, sem cortar
   conexões).
5. Confere de novo, 15 vezes em 30 s, por dois caminhos: **pelo nginx**, de dentro do contêiner, e **pelo caminho do
   visitante** (DNS público → Cloudflare → nginx), que pega o que o primeiro não vê: lista de IPs aceitos, porta
   publicada, certificado. **Se falhar, volta sozinho para a cor anterior** (o último build que funcionava, que
   ainda está de pé).
6. **Passados os 30 s sem erro, a cor anterior é desligada.** Ela termina as requisições em andamento antes de sair
   (até ~3 min, para não cortar uma resposta em streaming) e fica parada. `deploy/switch.sh` religa e volta para ela
   se for preciso.

**Quando um deploy falha em qualquer passo**, o checkout volta para o último commit que subiu bem. Com ele voltam a
configuração do nginx (e as portas e volumes do contêiner) e os arquivos de `data/seed` que a API no ar lê. O que
**não** volta sozinho: banco, embedder e RAG já reconstruídos ficam na versão nova, e o acervo, se a ingestão já
trocou, fica o novo. Por isso mudanças nesses serviços e no formato do acervo precisam continuar funcionando com a
API anterior.

Se a cor nova nem ficar pronta (build quebrado, API que não sobe), o nginx não é tocado: o último build segue no ar.
O nginx nunca devolve página HTML para a API: com a API fora do ar ele responde `503` em JSON, com CORS, e o front
mostra a mensagem em vez de quebrar. Qual build atende aparece no cabeçalho `X-Atlas-Api`, só com o nome da cor:

```bash
curl -sI https://api.atlas-stars-beyond.tech/api/health | grep -i x-atlas-api   # blue ou green
```

Atualização do acervo ([refresh.sh](refresh.sh), uma vez por dia às 04:00): se o PSI mudou, a ingestão troca o acervo
e uma cor nova da API sobe com ele (blue/green, sem reinício); depois o RAG reindexa os PDFs novos.

## Dia a dia (na VPS, em `/opt/atlas`)

| O quê | Comando |
|---|---|
| Qual cor está no ar | `deploy/switch.sh --status` |
| **Voltar para o build anterior** | `deploy/switch.sh` (religa a cor anterior, confere e troca: alguns segundos) |
| Logs do deploy | `journalctl -u atlas-deploy -f` |
| Forçar deploy (ponta da `main`, sem esperar o CI) | `deploy/deploy.sh --force` |
| Pausar deploys | `systemctl stop atlas-deploy.timer` |
| Serviços | `docker compose -f docker-compose.yml -f deploy/compose.prod.yml ps` |
| Logs da API no ar | `docker logs -f atlas-fire-extintion-backend-api-$(grep -oE 'blue\|green' /etc/atlas/active/api.conf)-atlas-api-1` |
| Buscar novidades no PSI agora | `deploy/refresh.sh` |
| Refazer o acervo do zero | `deploy/refresh.sh --force` |
| Logs da atualização | `journalctl -u atlas-refresh -f` |
| IPs do Cloudflare (regera `nginx/snippets/cloudflare.conf`; depois faça o commit) | `deploy/update-cloudflare-ips.sh` |

Um commit cujo deploy falhou não é tentado de novo a cada volta: o próximo deploy acontece no próximo commit ou com
`--force`. O que está fora do git (`.env`, `data/`, `deploy/certs/`, `/etc/atlas/`) não é tocado.

## Migração do esquema antigo (automática)

Na VPS que já roda o deploy antigo não é preciso entrar: o merge na `main` é puxado pelo `deploy.sh` antigo, que faz
um deploy como sempre (a API antiga continua declarada em [compose.prod.yml](compose.prod.yml) para isso) e deixa no
disco o `deploy.sh` novo. O commit seguinte já roda o novo: sobe a primeira cor, troca o nginx, confere e só então
remove a API antiga. Enquanto não existe `/etc/atlas/active/api.conf`, o nginx manda o tráfego para a API antiga.

## O que protege a origem

| Camada | Onde | O que faz |
|---|---|---|
| Só o Cloudflare fala com a API | [snippets/cloudflare.conf](nginx/snippets/cloudflare.conf) | Conexão de fora das faixas do Cloudflare é fechada (`444`). A rede interna do Docker não está na lista: em IPv6 uma conexão de fora chegaria com o endereço dela. |
| Só IPv4 | [compose.prod.yml](compose.prod.yml) | A 443 é publicada em `0.0.0.0`. O Cloudflare chega pelo registro `A`. |
| Limite por visitante nas rotas de IA | [conf.d/atlas.conf](nginx/conf.d/atlas.conf) | 30 por minuto, rajada de 20, em `/api/ask`, `/api/ask/stream` e `/api/scenario/infer`. |
| Teto das rotas de IA somando todo mundo | idem | 300 por minuto, rajada de 100. Limita o gasto de um ataque espalhado por muitos IPs. |
| Limite da câmera térmica | idem | 3 por segundo por visitante, rajada de 30. |
| Limite das outras rotas pesadas | idem | 20 por segundo por visitante, rajada de 100, em busca, ranking, próximos testes, grafo e simulação de missão. |
| Pedidos simultâneos por visitante | idem | 100. |
| Cabeçalhos | [snippets/headers.conf](nginx/snippets/headers.conf) | HSTS e `X-Content-Type-Options: nosniff` em toda resposta. |
| Prioridades dos contêineres | [compose.prod.yml](compose.prod.yml) | Na disputa por CPU, nginx e API pesam mais que os modelos e a ingestão; se a memória acabar, a ingestão e os modelos morrem antes do banco. São prioridades, não tetos. |

Quem estoura um limite recebe `429` em JSON, com CORS e `Retry-After`. Os números são um ponto de partida: se uma
sala inteira sair pelo mesmo IP e bater no limite, aumente a rajada no arquivo e faça o commit.

**O que só dá para fazer fora do repositório**

- **Limite de gasto nas chaves**: no console da Anthropic e da OpenAI. É o teto de verdade do custo.
- **SSL Full (strict)**: gere um Origin Certificate no Cloudflare (SSL/TLS → Origin Server), grave em
  `/opt/atlas/deploy/certs/origin.pem` e `origin.key` (`chmod 600`), recarregue o nginx
  (`docker compose -f docker-compose.yml -f deploy/compose.prod.yml exec nginx nginx -s reload`) e só então troque
  o modo para Full (strict).
- **Authenticated Origin Pulls** (o nginx só aceita o Cloudflare da *sua* zona, e não qualquer cliente dele). Nesta
  ordem, para não derrubar o site:
  1. No Cloudflare: SSL/TLS → Origin Server → Authenticated Origin Pulls → ligar. O Cloudflare passa a apresentar
     um certificado de cliente; o nginx ainda não o exige.
  2. Na VPS, baixe a autoridade do Cloudflare (o endereço está na documentação "Authenticated Origin Pulls" deles)
     para `/opt/atlas/deploy/certs/origin-pull-ca.pem`.
  3. Crie `/opt/atlas/deploy/certs/aop.conf` com estas três linhas e recarregue o nginx:
     ```nginx
     ssl_client_certificate /etc/nginx/certs/origin-pull-ca.pem;
     ssl_verify_client optional;
     set $atlas_aop 1;
     ```
  4. Confira: `curl -sI https://api.atlas-stars-beyond.tech/api/health` responde `200`. Para desfazer, apague o
     `aop.conf` e recarregue. O deploy continua conferindo a API por dentro do contêiner, sem certificado.
- **SSH só por chave numa VPS que já existe**: confirme que a sua chave entra, e então
  ```bash
  printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\n' \
    > /etc/ssh/sshd_config.d/11-atlas-keys-only.conf && sshd -t && systemctl reload ssh
  ```
  O `bootstrap.sh` já faz isso em VPS nova (se o root tiver chave cadastrada). O `.env` passa a `600` no próximo
  deploy.

## Primeira vez (VPS nova)

1. **Repositório.** O código desta pasta precisa estar no GitHub (ex.: `stars-beyond/atlas-fire-extintion-backend`).
2. **Bootstrap**, da sua máquina:
   ```bash
   ssh root@IP 'REPO_URL=git@github.com:stars-beyond/atlas-fire-extintion-backend.git bash -s' < deploy/bootstrap.sh
   # depois da primeira vez o SSH está na 665: ssh -p 665 ...
   ```
   Na primeira rodada ele instala Docker, git e firewall, cria a deploy key e mostra a chave pública. Cadastre-a em
   **GitHub → repositório → Settings → Deploy keys** (só leitura) e rode o mesmo comando de novo: ele clona em
   `/opt/atlas`, instala os timers e faz o primeiro deploy (build + ingestão, 10 a 15 min).
3. **Segredos**: copie o `.env` (chaves de IA) para a VPS e force um deploy:
   ```bash
   scp -P 665 .env root@IP:/opt/atlas/.env
   ssh -p 665 root@IP /opt/atlas/deploy/deploy.sh --force
   ```
   Variáveis: `ANTHROPIC_API_KEY`, `ATLAS_MODEL`, `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `EMBEDDING_MODEL`, `CHAT_MODEL`,
   `RAG_COLLECTION`, `ATLAS_NETWORK`, `ATLAS_DOCS`, `ATLAS_DOC_BUDGET_MB`.
4. **Cloudflare (domínio)**: DNS `A api → IP da VPS` (só `A`; o nginx não atende IPv6), **Proxied**; SSL/TLS modo
   **Full** (ou **Full (strict)** com um Origin Certificate em `/opt/atlas/deploy/certs/origin.pem` e `origin.key`);
   **Always Use HTTPS** ligado.
5. **Portas**: só **665** (SSH) e **443** (nginx). Banco e API ficam na rede interna do Docker.
