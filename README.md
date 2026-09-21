# scrapy

Plataforma de configuração ao vivo (feature flags, kill-switches, conteúdo segmentado) para
os serviços do workspace e para o app mobile. Ver plano completo em
`/home/gustavo/.claude/plans/voc-j-tem-contexto-ticklish-pony.md`.

## Stack

- Backend + UI: Go (Gin) com UI React embutida (`ui/dist` via `go:embed`)
- Persistência: Postgres (fonte de verdade, sem cache próprio)
- Tempo real: WebSocket bidirecional (`hello`/`state`/`change`/`ack`) + `LISTEN/NOTIFY` para
  fanout entre réplicas do scrapy
- SDKs: Java (JDK HttpClient + Jackson `provided`) e Python (`httpx` + `websockets`)

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
primeiro login. Nenhuma credencial fica no repositório.

## UI

```bash
cd ui && npm install && npm run dev   # dev server com proxy para :8080
npm run build                          # gera ui/dist, embutido no binário Go
```

## Testes

```bash
go test ./...
cd sdk/python && pip install -e . && pytest   # requer httpx + websockets instalados
```

## Estrutura

Ver árvore completa e modelo de dados no plano. Resumo:

- `internal/store` — Postgres, migrations embutidas, criptografia AES-GCM de secrets
- `internal/hub` — WebSocket hub + LISTEN/NOTIFY
- `internal/eval` — avaliação de regras de segmentação (mobile)
- `internal/auth` — argon2id, JWT de sessão, API keys
- `sdk/java`, `sdk/python` — clientes para os serviços do workspace
- `k8s/` — manifests padrão do workspace (deployment-qa, service-qa, PDB)
