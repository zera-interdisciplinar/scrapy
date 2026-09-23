# scrapy

> ⚠️ **Projeto gerado integralmente por IA (Claude Code).** Todo o código deste
> repositório — backend, UI, SDKs e manifests k8s — foi escrito por um agente de IA a
> partir de um plano revisado por humano, sem edição manual linha a linha. Trate como POC:
> revise com atenção antes de usar em produção, especialmente as partes de segurança
> (criptografia de secrets, auth, RBAC).

Plataforma de configuração ao vivo (feature flags, kill-switches, conteúdo segmentado) para
os serviços do workspace e para o app mobile.

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
- `k8s/` — manifests padrão do workspace (`*-qa.yaml` para qa, sem sufixo para production, PDB)

## Uso — serviços do workspace (Java/Python)

Toda config de serviço passa por uma **api key** escopada a um `scope` (o serviço) + `env`
(`qa`/`prod`). A key nunca é secreta no sentido de "não pode vazar em log" — trate como
credencial normal de serviço: fica em variável de ambiente/Secret do pod, nunca commitada.

**1. Criar a key** (via UI admin, papel `admin`, ou direto na API):

```bash
curl -X POST https://scrapy.internal/v1/admin/keys \
  -H "Content-Type: application/json" \
  --cookie "scrapy_session=<cookie de sessão admin>" \
  -d '{"Scope": "ms-inventory", "Env": "prod"}'
# => {"key": "sk_..."}  -- aparece só essa vez, guarda no secret do serviço
```

**2. Bootstrap** — puxa o config inteiro antes do app subir, vira env var / property:

```bash
curl https://scrapy.internal/v1/bootstrap?scope=ms-inventory \
  -H "Authorization: Bearer sk_..."
# => {"search.timeout_ms": 3000, "checkout_v2_enabled": true, ...}
```

Python (`sdk/python`):

```python
from scrapy_client import init, get

init("https://scrapy.internal", "sk_...", "ms-inventory", instance="pod-abc123")
timeout = get("search.timeout_ms", default=1000)
```

Java (Spring Boot, `sdk/java`) — configurado só via env do pod, sem código:

```
SCRAPY_URL=https://scrapy.internal
SCRAPY_API_KEY=sk_...
SCRAPY_SCOPE=ms-inventory
```
```java
boolean on = ScrapyHolder.get().getBool("checkout_v2_enabled", false);
```

**3. Tempo real** — depois do bootstrap, o SDK abre WebSocket (`/v1/connect`) sozinho e
mantém os valores atualizados via `hello`/`state`/`change`/`ack`, sem restart do serviço.
Se a conexão cair, reconecta com backoff+jitter; se o scrapy inteiro cair depois do boot,
o serviço continua rodando com o último valor conhecido (nunca trava esperando scrapy).

**4. Qual instância do scrapy apontar** — ver `k8s/README.md#quem-deve-apontar-para-qual-deployment-do-scrapy`:
`ms-x` em `qa` ou `production` aponta pro scrapy de **produção**, variando só o `Env` da key.
O deployment `scrapy-qa` é exclusivo pra testar o próprio scrapy.

**5. Rotação de key sem downtime**: cria uma key nova, atualiza o Secret do serviço,
confirma que ele reconectou com a nova, só então revoga a antiga:

```bash
curl -X POST https://scrapy.internal/v1/admin/keys/sk_abc12345/revoke \
  --cookie "scrapy_session=<cookie de sessão admin>"
```

## Uso — mobile

O endpoint mobile é **diferente** do dos serviços: sem WebSocket, sem SDK, um `POST` simples
com cache via ETag, pensado pra rodar embutido num app público (a key é client-side, não
tem como ser mantida secreta — qualquer um decompila o app e extrai ela).

```bash
curl -X POST https://scrapy.internal/v1/evaluate \
  -H "Authorization: Bearer sk_client_..." \
  -H "Content-Type: application/json" \
  -d '{"attrs": {"userId": "abc123", "unitId": "d290f1ee-...", "role": "EMPLOYEE", "country": "BR", "appVersion": "4.2.0"}}'
# => {"banner_promo": "...", "new_checkout_pct": 25}
```

- A key usada aqui é criada do mesmo jeito que a de serviço (`POST /v1/admin/keys`), mas
  com `Env: "prod"` — mobile só lê `prod`, não existe conceito de "mobile em qa".
- `attrs` alimenta as regras de segmentação (`rules`) de cada entry — é o que decide, por
  exemplo, se `userId` cai no rollout de 25% de uma feature nova. As dimensões conhecidas
  no editor de segmentação da UI admin (`userId`, `unitId`/"sede", `role`/"cargo") batem
  com o que os serviços reais do workspace carregam: JWT do `ms-administrative-core`
  expõe `sub`/`role`, e todo request no `ms-inventory` leva um header `X-Unit-Id`. O
  editor também expõe `organizationId`/"empresa" como preparação, mas **nenhum client
  envia esse dado hoje** — só existe via lookup `unit → organization` dentro do banco do
  `ms-administrative-core`; segmentar por empresa não funciona até algum client passar a
  mandar esse attr.
- Entries `secret=true` nunca aparecem na resposta, nem que a regra "combine".
- Cacheia a resposta local e reenvia com `If-None-Match: <etag recebido>` — o servidor
  responde `304` sem corpo se nada mudou, então **não** deixe de mandar o ETag: é a única
  proteção contra spam de request num endpoint que é, por natureza, público.
- Limite: 120 requisições/minuto por IP (`internal/api/ratelimit.go`) — normal pra um app
  que faz polling periódico; se o seu client faz mais que isso, aumente o intervalo de
  polling em vez de pedir mais limite.

## Segurança da UI admin

- **Logout é revogação real**, não só limpar cookie: `POST /v1/auth/logout` põe o `jti` da
  sessão numa deny-list (`revoked_sessions`) checada em toda request — um cookie roubado
  para de funcionar na hora, não fica valendo até o TTL de 12h expirar.
- **RBAC por escopo**: um usuário `editor`/`admin` pode ter `allowed_scopes` (coluna em
  `users`, `text[]`) restringindo em quais serviços ele escreve. `NULL` = acesso a todos
  (padrão do admin de bootstrap). Sem UI própria ainda pra editar isso — seta direto no
  banco:
  ```sql
  UPDATE users SET allowed_scopes = ARRAY['ms-inventory'] WHERE email = 'dev-inventory@empresa.com';
  ```
- **Login tem rate limit** (10 tentativas/minuto por IP) contra brute-force de senha.
- **JWT de sessão fixa o algoritmo** (`HS256`) na verificação — não aceita mais o `alg` que
  o próprio token declarar.

## Backup de credenciais

Se `BACKUP_DB_DSN` estiver setada, toda escrita no Postgres primário é espelhada para um
segundo Postgres, num ambiente isolado (ver `k8s/README.md`). O espelho roda via uma fila
outbox no primário (trigger em cada tabela + worker em Go, `internal/store/mirror.go`):
nenhum código de escrita precisou mudar, e uma falha no banco de backup não afeta o serviço —
o worker acumula e tenta de novo a cada 5s até conseguir aplicar. Sem `BACKUP_DB_DSN`, o
mirror fica desligado (comportamento padrão em dev local).
