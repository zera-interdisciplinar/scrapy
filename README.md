# scrapy

> ⚠️ **Projeto gerado integralmente por IA (Claude Code), depois cortado para o app mobile.**
> Trate com atenção as partes de segurança (criptografia de secrets, auth, RBAC) antes de
> confiar nele em produção.

Configuração ao vivo do app mobile: envs de boot, feature flags e conteúdo segmentado.
O painel admin também liga e desliga os namespaces Kubernetes `qa` e `production`.

## Stack

- Backend + UI: Go (Gin) com UI React embutida (`ui/dist` via `go:embed`)
- Persistência: Postgres (fonte de verdade, sem cache próprio)
- Cliente: o app mobile, com `POST /v1/boot` e `POST /v1/flags` (header `apikey`)

## Rodando local

```bash
docker compose up -d                 # Postgres em :5433
export DB_DSN="postgres://scrapy:scrapy_dev_only@localhost:5433/scrapy?sslmode=disable"
export SCRAPY_MASTER_KEY=$(openssl rand -base64 32)
export SCRAPY_SESSION_SECRET=$(openssl rand -base64 32)
go run ./cmd/scrapy
```

A senha do usuário `admin@scrapy.local` é gerada e impressa **uma única vez** no log de boot
(a menos que `SCRAPY_BOOTSTRAP_PASSWORD` esteja setada). Troca de senha é obrigatória no
primeiro login no banco (`must_change_password`); a UI ainda não força essa troca.
Nenhuma credencial fica no repositório.

Fora do cluster, `GET /v1/admin/qa-env` e `GET /v1/admin/prod-env` respondem 503.

## UI

```bash
cd ui && npm install && npm run dev   # dev server com proxy para :8080
npm run build                          # gera ui/dist, embutido no binário Go
```

## Testes

```bash
go test ./...
cd ui && npm test
```

## Estrutura

- `internal/store` — Postgres, migrations embutidas, criptografia AES-GCM de secrets
- `internal/eval` — avaliação de regras de segmentação (mobile)
- `internal/auth` — argon2id, JWT de sessão, API keys
- `internal/cluster` — escala Deployments dos namespaces `qa` e `production`
- `k8s/` — manifests (`*-qa.yaml` para qa, sem sufixo para production). `rbac-prod.yaml` é apply manual

## Uso — mobile

A key é client-side: qualquer um decompila o app e extrai ela. Ela só lê o `scope` + `env`
em que foi criada. Entries `secret=true` nunca aparecem na resposta.

```bash
curl -X POST https://scrapy.internal/v1/boot \
  -H "apikey: sk_client_..."

curl -X POST https://scrapy.internal/v1/flags \
  -H "apikey: sk_client_..." \
  -H "Content-Type: application/json" \
  -d '{"attrs": {"userId": "abc123", "unitId": "d290f1ee-...", "role": "EMPLOYEE", "country": "BR", "appVersion": "4.2.0"}}'
```

- `POST /v1/boot` — antes do login. Só entries `boot_only=true` (envs), valor base, sem segmentação.
- `POST /v1/flags` — depois do login. Flags e conteúdo, com regras `when`/`then` (first-match) sobre `attrs`.
- A key é criada em `POST /v1/admin/keys` com o `Env` do app (`qa` ou `prod`).
- Cacheia a resposta e reenvia `If-None-Match`. `304` sem corpo se nada mudou.
- Limite: 120 requisições/minuto por IP (`internal/api/ratelimit.go`).

Contrato completo: `docs/mobile-api-contract.md`.

O editor de segmentação conhece `userId`, `unitId` ("sede") e `role` ("cargo").
`organizationId` ("empresa") aparece na UI, mas nenhum client envia esse attr hoje.

## Segurança da UI admin

- **Logout é revogação real**: `POST /v1/auth/logout` põe o `jti` da sessão numa deny-list
  (`revoked_sessions`) checada em toda request.
- **RBAC por escopo**: `allowed_scopes` (`text[]` em `users`) restringe em quais apps um
  `editor`/`admin` escreve. `NULL` = todos (padrão do admin de bootstrap). Sem UI pra
  editar isso — seta direto no banco:
  ```sql
  UPDATE users SET allowed_scopes = ARRAY['mobile'] WHERE email = 'dev@empresa.com';
  ```
- **Login tem rate limit** (10 tentativas/minuto por IP).
- **JWT de sessão fixa o algoritmo** (`HS256`) na verificação.

## Ambientes Kubernetes

**Sistema → Ambientes** escala os Deployments de `qa` e de `production` para 0 ou 1.
Ficam de fora `scrapy`, `scrapy-*`, o Deployment `postgres` e qualquer nome com `kong`.
Ligar volta para **1** réplica, não para a contagem anterior. Só `admin` escreve.
Detalhe e o apply manual de `k8s/rbac-qa.yaml` / `k8s/rbac-prod.yaml`: `k8s/README.md`.

## Backup de credenciais

Se `BACKUP_DB_DSN` estiver setada, toda escrita no Postgres primário é espelhada para um
segundo Postgres (ver `k8s/README.md`). O espelho roda via outbox no primário
(`internal/store/mirror.go`): uma falha no banco de backup não afeta o serviço — o worker
tenta de novo a cada 5s. Sem `BACKUP_DB_DSN`, o mirror fica desligado.
