# scrapy

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8.svg)](https://go.dev/)

Plataforma de configuração ao vivo do sistema Zera: envs de boot, feature flags, kill-switches e
conteúdo segmentado para o app mobile, sem rebuild do app nem restart de pod. Um painel admin
(React, embutido no binário) edita os valores e também liga e desliga os namespaces Kubernetes
`qa` e `production`. Parte de um sistema maior; o consumidor é o
[`zera-android-app`](https://github.com/zera-interdisciplinar/zera-android-app), que chega ao
serviço pelo gateway ([`infra-gtw-kong`](https://github.com/zera-interdisciplinar/infra-gtw-kong)).

## Sumário

- [Domínio](#domínio)
- [Arquitetura](#arquitetura)
- [Stack](#stack)
- [Executando localmente](#executando-localmente)
- [Configuração](#configuração)
- [API](#api)
- [Segurança](#segurança)
- [Ambientes Kubernetes](#ambientes-kubernetes)
- [Backup de credenciais](#backup-de-credenciais)
- [Testes](#testes)
- [Deploy](#deploy)

## Domínio

Uma **entry** é um par chave/valor tipado (`bool`, `string`, `number`, `json` ou `markdown`)
que vive num **scope** (hoje, o app `mobile`) e num **env** (`qa` ou `prod`). Três marcações mudam
como ela é entregue:

- `boot_only=true` — é uma **env de boot**: sai em `POST /v1/boot`, antes do login, sempre com o
  valor base e sem segmentação (ainda não existe usuário logado para aplicar regra).
- `boot_only=false` — é uma **flag** ou conteúdo: sai em `POST /v1/flags`, depois do login, com
  regras `when`/`then` (first-match) avaliadas sobre os `attrs` que o app envia.
- `secret=true` — o valor é cifrado em repouso (AES-GCM) e **nunca** aparece na resposta mobile.

O editor de segmentação conhece `userId`, `unitId` ("sede") e `role` ("cargo"). `organizationId`
("empresa") aparece na UI, mas nenhum client envia esse attr hoje.

O **kill-switch** (`POST /v1/admin/kill/:scope`) vira para `false`, numa transação, toda entry
`bool` do scope/env — o botão vermelho para desligar um recurso em produção sem esperar deploy.
Cada mudança de entry grava um registro na trilha de auditoria (`audit_log`).

Uma **API key** é criada no painel (`POST /v1/admin/keys`) presa a um scope e a um env. Uma key
com `env=qa` só lê entries `qa`.

## Arquitetura

```
cmd/scrapy/             Entrypoint: lê a configuração, migra o banco, semeia o admin e sobe o Gin
internal/
├── api/                Rotas (Gin), middlewares de sessão e API key, rate limit
├── auth/               argon2id, JWT de sessão, API keys
├── eval/               Avaliação das regras de segmentação (when/then, first-match)
├── store/              Postgres, migrations embutidas, criptografia AES-GCM, mirror de backup
└── cluster/            Escala os Deployments dos namespaces qa e production
ui/                     Painel admin (React + Vite); ui/dist é embutido no binário via go:embed
k8s/                    Manifests (*-qa.yaml para qa, sem sufixo para production)
docs/                   Contrato da API mobile
```

Persistência: o Postgres é a fonte de verdade, sem cache próprio. As migrations
(`internal/store/migrations/`) são aplicadas no boot; se o banco não responder, o serviço falha o
boot de propósito — subir um pod com configuração errada é pior do que não subir.

## Stack

| | |
|---|---|
| Linguagem | **Go 1.25** |
| Framework | Gin |
| Banco | PostgreSQL (pgx) |
| Auth | argon2id, JWT de sessão HS256, API keys |
| UI | React 18 + Vite, embutida no binário (`go:embed`) |
| Cliente | App mobile (`POST /v1/boot` e `POST /v1/flags`, header `apikey`) |

## Executando localmente

Pré-requisitos: Go 1.25, Docker (para o Postgres) e Node 22 (só para mexer na UI).

```bash
docker compose up -d                 # Postgres em :5433
export DB_DSN="postgres://scrapy:scrapy_dev_only@localhost:5433/scrapy?sslmode=disable"
export SCRAPY_MASTER_KEY=$(openssl rand -base64 32)
export SCRAPY_SESSION_SECRET=$(openssl rand -base64 32)
go run ./cmd/scrapy
```

A aplicação sobe em `http://localhost:8080`. A senha do usuário `admin@scrapy.local` é gerada e
impressa **uma única vez** no log de boot (a menos que `SCRAPY_BOOTSTRAP_PASSWORD` esteja setada).
O banco marca a troca de senha como obrigatória no primeiro login (`must_change_password`), mas a
UI ainda não força essa troca. Nenhuma credencial fica no repositório.

Fora do cluster, `GET /v1/admin/qa-env` e `GET /v1/admin/prod-env` respondem 503.

### UI

```bash
cd ui && npm install && npm run dev   # dev server com proxy para :8080
npm run build                          # gera ui/dist, embutido no binário Go
```

## Configuração

Toda configuração sensível vem de variável de ambiente; não há segredo com valor padrão no
código. Provisionamento dos Secrets em QA/produção: [`k8s/README.md`](k8s/README.md).

### Obrigatórias

| Variável | Efeito |
|---|---|
| `DB_DSN` | String de conexão completa com o Postgres |
| `SCRAPY_MASTER_KEY` | 32 bytes em base64 — chave AES-256 das entries `secret=true`. **Não pode ser rotacionada sem re-cifrar** todas elas |
| `SCRAPY_SESSION_SECRET` | Segredo HMAC do JWT de sessão da UI admin |

### Opcionais

| Variável | Efeito |
|---|---|
| `SCRAPY_BOOTSTRAP_PASSWORD` | Senha do admin no primeiro boot; sem ela o serviço gera uma e imprime no log |
| `BACKUP_DB_DSN` | Liga o espelhamento de toda escrita para um segundo Postgres (ver [Backup de credenciais](#backup-de-credenciais)) |
| `PORT` (default `8080`) | Porta HTTP |
| `QA_NAMESPACE` (default `qa`), `PROD_NAMESPACE` (default `production`) | Namespaces controlados pelo painel de ambientes |

## API

Contrato completo da API mobile: [`docs/mobile-api-contract.md`](docs/mobile-api-contract.md).

### Mobile

A key é client-side: qualquer um decompila o app e extrai ela. Por isso ela só lê o scope + env em
que foi criada, e entries `secret=true` nunca aparecem na resposta.

```bash
curl -X POST https://scrapy.internal/v1/boot \
  -H "apikey: sk_client_..."

curl -X POST https://scrapy.internal/v1/flags \
  -H "apikey: sk_client_..." \
  -H "Content-Type: application/json" \
  -d '{"attrs": {"userId": "abc123", "unitId": "d290f1ee-...", "role": "EMPLOYEE", "country": "BR", "appVersion": "4.2.0"}}'
```

| Endpoint | Quando | O quê |
|---|---|---|
| `POST /v1/boot` | Antes do login | Só entries `boot_only=true`, valor base, sem segmentação |
| `POST /v1/flags` | Depois do login | Flags e conteúdo, com regras `when`/`then` (first-match) sobre `attrs` |

- Cacheia a resposta e reenvia `If-None-Match`; `304` sem corpo se nada mudou.
- Limite de 120 requisições/minuto por IP (`internal/api/ratelimit.go`).

### Painel admin

Autenticado por sessão (JWT), com três papéis: `viewer` lê, `editor` escreve entries e aciona o
kill-switch, `admin` faz tudo, inclusive chaves e ambientes.

| Recurso | Endpoints | Papel mínimo |
|---|---|---|
| Sessão | `POST /v1/auth/login`, `POST /v1/auth/logout`, `GET /v1/auth/me` | Login: público. Demais: `viewer` |
| Entries | `GET/PUT/DELETE /v1/admin/entries` | Ler: `viewer`. Escrever: `editor` |
| Scopes e auditoria | `GET /v1/admin/scopes`, `GET /v1/admin/audit` | `viewer` |
| Kill-switch | `POST /v1/admin/kill/:scope?env=` | `editor` |
| API keys | `POST /v1/admin/keys`, `POST /v1/admin/keys/:prefix/revoke` | `admin` |
| Ambientes | `GET /v1/admin/{qa,prod}-env`, `PUT /v1/admin/{qa,prod}-env`, `PUT /v1/admin/prod-env/scale` | Ler: `viewer`. Escrever: `admin` |

Público, sem autenticação: `/v1/healthz` (responde 503 se o Postgres não responder).

## Segurança

- **Logout é revogação real**: `POST /v1/auth/logout` põe o `jti` da sessão numa deny-list
  (`revoked_sessions`) checada em toda request.
- **RBAC por escopo**: `allowed_scopes` (`text[]` em `users`) restringe em quais apps um
  `editor`/`admin` escreve. `NULL` = todos (padrão do admin de bootstrap). Não há UI para editar
  isso — seta direto no banco:
  ```sql
  UPDATE users SET allowed_scopes = ARRAY['mobile'] WHERE email = 'dev@empresa.com';
  ```
- **Login tem rate limit** (10 tentativas/minuto por IP).
- **JWT de sessão fixa o algoritmo** (`HS256`) na verificação.
- **Secrets em repouso**: entries `secret=true` são cifradas com AES-GCM.

## Ambientes Kubernetes

**Sistema → Ambientes** liga e desliga os namespaces `qa` e `production` (scale em massa para 0 ou
1). Ficam de fora `scrapy`, `scrapy-*`, o Deployment `postgres` e qualquer nome com `kong`. Ligar
volta para **1** réplica, não para a contagem anterior.

No card de **produção**, cada Deployment não protegido também tem um campo de réplicas (0–20):
`PUT /v1/admin/prod-env/scale` com `{ "name", "replicas" }`. Só `admin` escreve. Detalhe e o apply
manual de `k8s/rbac-qa.yaml` / `k8s/rbac-prod.yaml`: [`k8s/README.md`](k8s/README.md).

## Backup de credenciais

Se `BACKUP_DB_DSN` estiver setada, toda escrita no Postgres primário é espelhada para um segundo
Postgres (ver [`k8s/README.md`](k8s/README.md)). O espelho roda via outbox no primário
(`internal/store/mirror.go`): uma falha no banco de backup não afeta o serviço — o worker tenta de
novo a cada 5s. Sem `BACKUP_DB_DSN`, o mirror fica desligado.

## Testes

```bash
go test ./...
cd ui && npm test
```

## Deploy

CI (`ci.yml`) roda em toda PR para `main`/`qa`: testes Go com cobertura enviada ao Codecov. Deploy
para QA (`deploy-qa.yml`) acontece no push na branch `qa`; produção (`deploy-prod.yaml`), no push
de uma tag `v*.*.*`. Há **dois deployments** (`qa` e `production`), cada um com Postgres próprio — a
separação existe para validar mudança no código do scrapy antes de promover, não para filtrar o mesmo
dado por ambiente. Segredos, ordem de deploy e passos manuais (RBAC) estão em
[`k8s/README.md`](k8s/README.md) — sempre a fonte da verdade para o que precisa existir em cada
ambiente antes do deploy. A rota no gateway fica em `infra-gtw-kong` (`manifests/{qa,prod}/scrapy.yaml`).
