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
3. `deployment-qa.yaml` + `service-qa.yaml` (qa) / `deployment.yaml` + `service.yaml`
   (production).
4. Rota no `infra-gtw-kong` (`manifests/{qa,prod}/scrapy.yaml`).

O scrapy sobe **antes** de qualquer serviço cliente (`ms-inventory`,
`ms-administrative-core`, ...) que dependa dele no boot — ver fase 5 do plano.

## Quem deve apontar para qual deployment do scrapy

O scrapy tem **dois** deployments (`qa` e `production`), cada um com Postgres próprio e
isolado — bancos completamente diferentes, não é a mesma fonte de dado filtrada por
ambiente. Isso existe só pra validar mudança no **código do scrapy** antes de promover pra
produção (o mesmo padrão de qualquer outro serviço do workspace).

Dentro do scrapy, o ambiente (`qa`/`prod`) já é modelado por entry
(`entries.env_id` — ver `internal/store/store.go`) e por api key escopada
(`api_keys.env_id`). Uma única instância — a de **produção** — já serve os dois ambientes:
uma chave criada com `Env=qa` só enxerga entries `qa`, uma com `Env=prod` só enxerga
`prod`. O dado é o mesmo Postgres, só filtrado.

Por isso:

| Quem | Aponta para | Env usado na api key |
|---|---|---|
| `ms-x` rodando em `qa` | `scrapy` do namespace **production** | `qa` |
| `ms-x` rodando em `production` | `scrapy` do namespace **production** | `prod` |
| Pipeline de CI/QA do **próprio scrapy** | `scrapy-qa` (namespace `qa`) | — (é o alvo do teste, não um consumidor) |

**Nunca** aponte um `ms-x` de `qa` para o `scrapy` do namespace `qa`. Por padrão, resolução
de DNS interna do k8s prefere o Service do mesmo namespace — se o Helm chart de um serviço
cliente não fixar explicitamente o host/namespace do scrapy de produção, ele cai nessa
armadilha silenciosamente: passa a ler de um banco isolado, sem sincronia com produção,
sem alertar ninguém. O sintoma é "kill switch/flag mudou no admin mas o serviço não
percebeu" — porque o serviço nunca estava lendo do banco onde a mudança foi feita.

Configure o host do scrapy nos manifests de `ms-x` como o FQDN completo do Service em
`production` (ex: `scrapy.production.svc.cluster.local`), nunca `scrapy` puro.

## Por que não StatefulSet para o Postgres

Réplica única, `PersistentVolumeClaim` `ReadWriteOnce`, `strategy: Recreate`: é o mesmo
padrão já usado em `ms-administrative-core/k8s/postgres-qa.yaml`. Projeto pequeno, um único
Postgres — StatefulSet, operator (Zalando/CloudNativePG) ou HA de banco viram
over-engineering aqui; sobem quando o volume de escrita ou o RPO exigirem.
