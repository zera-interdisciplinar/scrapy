# k8s — scrapy

Manifests aplicados por ambiente. Antes do primeiro deploy, cada ambiente precisa de dois
Secrets criados manualmente (uma vez), no mesmo padrão dos outros serviços do workspace.

## 1. `scrapy-postgres-secrets`

Credenciais do Postgres próprio do scrapy (`postgres-qa.yaml` (qa) / `postgres.yaml` (production)).

```sh
# QA
kubectl create secret generic scrapy-postgres-secrets -n qa \
  --from-literal=POSTGRES_DB=scrapy \
  --from-literal=POSTGRES_USER=scrapy \
  --from-literal=POSTGRES_PASSWORD='<senha-forte-qa>'

# Produção
kubectl create secret generic scrapy-postgres-secrets -n production \
  --from-literal=POSTGRES_DB=scrapy \
  --from-literal=POSTGRES_USER=scrapy \
  --from-literal=POSTGRES_PASSWORD='<senha-forte-prod>'
```

## 2. `scrapy-secrets`

Consumido pelo `Deployment scrapy` (`deployment-qa.yaml` (qa) / `deployment.yaml` (production)).

| chave                       | obrigatória | o que é |
|------------------------------|------|---------|
| `DB_DSN`                     | sim  | string de conexão completa com o Postgres acima |
| `SCRAPY_MASTER_KEY`          | sim  | 32 bytes em base64, chave AES-256 para os `entries` marcados `secret=true` |
| `SCRAPY_SESSION_SECRET`      | sim  | segredo HMAC do JWT de sessão da UI admin |
| `SCRAPY_BOOTSTRAP_PASSWORD`  | não  | senha do admin no primeiro boot; sem ela o scrapy gera uma e imprime **uma vez** no log |
| `BACKUP_DB_DSN`              | não  | DSN de um segundo Postgres, em ambiente isolado, para onde toda escrita é espelhada (proteção contra perda de credenciais). Sem ela o mirror fica desligado. |

```sh
# QA
kubectl create secret generic scrapy-secrets -n qa \
  --from-literal=DB_DSN='postgres://scrapy:<senha-forte-qa>@scrapy-postgres:5432/scrapy?sslmode=disable' \
  --from-literal=SCRAPY_MASTER_KEY="$(openssl rand -base64 32)" \
  --from-literal=SCRAPY_SESSION_SECRET="$(openssl rand -base64 32)"

# Produção
kubectl create secret generic scrapy-secrets -n production \
  --from-literal=DB_DSN='postgres://scrapy:<senha-forte-prod>@scrapy-postgres:5432/scrapy?sslmode=disable' \
  --from-literal=SCRAPY_MASTER_KEY="$(openssl rand -base64 32)" \
  --from-literal=SCRAPY_SESSION_SECRET="$(openssl rand -base64 32)"
```

`SCRAPY_MASTER_KEY` **não pode ser rotacionada sem re-cifrar** todo `entries.value` marcado
`secret=true` — se precisar trocar, faça isso antes de qualquer entry secreta existir, ou
escreva uma migração de re-encriptação.

`BACKUP_DB_DSN` aponta para um Postgres **separado** do `scrapy-postgres` acima — outro
namespace/cluster/projeto, para que a perda do banco primário não leve o backup junto. O
scrapy roda as mesmas migrations nele e espelha toda escrita (via outbox, `internal/store/mirror.go`);
não precisa de setup manual de schema, só o banco existir e aceitar conexão.

```sh
kubectl create secret generic scrapy-secrets -n production \
  --from-literal=DB_DSN='...' \
  --from-literal=SCRAPY_MASTER_KEY='...' \
  --from-literal=SCRAPY_SESSION_SECRET='...' \
  --from-literal=BACKUP_DB_DSN='postgres://scrapy:<senha>@<host-isolado>:5432/scrapy_backup?sslmode=require'
```

## Ordem de deploy

1. `postgres-qa.yaml` (qa) / `postgres.yaml` (production) — PVC + Postgres + Service,
   precisa estar de pé e `Ready` antes do scrapy, que falha o boot se não conseguir
   conectar (comportamento pretendido: ver plano, "subir um pod com configuração errada
   é pior do que não subir").
2. Secrets acima.
3. `serviceaccount-qa.yaml` (CI aplica) e, em production, `rbac.yaml`
   (ServiceAccount `scrapy`). Os Role/RoleBinding de scale (`rbac-qa.yaml` e
   `rbac-prod.yaml`) **não** entram no GitHub Actions — a SA de deploy não tem
   `container.roles.create`. Aplique uma vez com um usuário admin:
   `kubectl apply -f k8s/rbac-qa.yaml` e `kubectl apply -f k8s/rbac-prod.yaml`.
   Sem o de QA, o toggle do namespace `qa` responde 403. Sem o de production,
   o toggle de produção responde 403. O resto do scrapy sobe normalmente.
4. `deployment-qa.yaml` + `service-qa.yaml` (qa) / `deployment.yaml` + `service.yaml`
   (production). Os pods usam `serviceAccountName: scrapy`.
5. Rota no `infra-gtw-kong` (`manifests/{qa,prod}/scrapy.yaml`).

O consumidor é o app mobile, via Kong (`/v1/boot` e `/v1/flags`, header `apikey`).

## Dois deployments, bancos isolados

O scrapy tem **dois** deployments (`qa` e `production`), cada um com Postgres próprio.
Isso existe só pra validar mudança no **código do scrapy** antes de promover. Não é o
mesmo dado filtrado por ambiente.

Dentro de uma instância, `qa`/`prod` é o env da entry e da api key: uma chave com
`Env=qa` só lê entries `qa`. O app de QA e o app de produção podem apontar para a
mesma instância (a de produção, quando existir) e diferir só no env da key. A
instância no namespace `qa` é o painel que liga e desliga os namespaces, e o alvo
do pipeline de CI (`deploy-qa.yml` no push em `qa`). Produção sobe com
`deploy-prod.yaml` no push de tag `v*.*.*` (mesmo padrão dos outros serviços):
substitui `IMAGE_TAG` pela tag, aplica ServiceAccount (`rbac.yaml`), Deployment
e Service, e espera o rollout. `rbac-prod.yaml` continua apply manual.

## Ambientes (ligar / desligar pela UI)

A tela **Sistema → Ambientes** escala os Deployments de um namespace para `0`
(desligar) ou `1` (ligar). Há um card para `qa` e outro para `production`.
Ligar sempre volta para **1** réplica — um Deployment que rodava com mais de uma
não recupera a contagem anterior. No card de **produção**, cada serviço não
protegido tem um campo de réplicas (0–20): `PUT /v1/admin/prod-env/scale`
(`{ "name", "replicas" }`), além do ligar/desligar do namespace.
Ficam de fora, de propósito, nos dois namespaces:

- `scrapy` e qualquer nome `scrapy-*` (inclui `scrapy-postgres` — sem o banco a UI morre)
- `postgres` (em `qa` é o banco do Kong: `postgres.qa.svc` — scale-to-0 derruba o IP público)
- qualquer Deployment cujo nome contenha `kong` (o gateway precisa continuar no ar)

Desligar o QA esvazia o namespace; o Autopilot então tenta empacotar o que sobrou.
`scrapy` e `scrapy-postgres` levam `safe-to-evict: "false"` e PDB `minAvailable: 1`
para não serem evictados — senão o PVC RWO do postgres descola, o pod fica
Pending. `/v1/healthz` responde 503 se o Postgres não responder.

O painel que opera os dois namespaces é o scrapy do namespace `qa`. O toggle só
funciona com esse pod **dentro do cluster** (ServiceAccount + Role). O CI cria o
ServiceAccount; `rbac-qa.yaml` e `rbac-prod.yaml` precisam de um apply manual
uma vez, com um principal que pode criar RBAC. O Role de produção autoriza
`system:serviceaccount:qa:scrapy` a listar e escalar Deployments em `production`.
Em docker compose local a API responde 503. Só o papel `admin` pode mudar o
estado; viewer/editor só lêem.

## Por que não StatefulSet para o Postgres

Réplica única, `PersistentVolumeClaim` `ReadWriteOnce`, `strategy: Recreate`: é o mesmo
padrão já usado em `ms-administrative-core/k8s/postgres-qa.yaml`. Projeto pequeno, um único
Postgres — StatefulSet, operator (Zalando/CloudNativePG) ou HA de banco viram
over-engineering aqui; sobem quando o volume de escrita ou o RPO exigirem.
