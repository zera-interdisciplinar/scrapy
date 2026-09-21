# k8s — scrapy

Manifests aplicados por ambiente. Antes do primeiro deploy, cada ambiente precisa de dois
Secrets criados manualmente (uma vez), no mesmo padrão dos outros serviços do workspace.

## 1. `scrapy-postgres-secrets`

Credenciais do Postgres próprio do scrapy (`postgres-qa.yaml` / `postgres-prod.yaml`).

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

Consumido pelo `Deployment scrapy` (`deployment-qa.yaml` / `deployment-prod.yaml`).

| chave                       | obrigatória | o que é |
|------------------------------|------|---------|
| `DB_DSN`                     | sim  | string de conexão completa com o Postgres acima |
| `SCRAPY_MASTER_KEY`          | sim  | 32 bytes em base64, chave AES-256 para os `entries` marcados `secret=true` |
| `SCRAPY_SESSION_SECRET`      | sim  | segredo HMAC do JWT de sessão da UI admin |
| `SCRAPY_BOOTSTRAP_PASSWORD`  | não  | senha do admin no primeiro boot; sem ela o scrapy gera uma e imprime **uma vez** no log |

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

## Ordem de deploy

1. `postgres-{qa,prod}.yaml` (PVC + Postgres + Service) — precisa estar de pé e `Ready`
   antes do scrapy, que falha o boot se não conseguir conectar (comportamento pretendido:
   ver plano, "subir um pod com configuração errada é pior do que não subir").
2. Secrets acima.
3. `deployment-{qa,prod}.yaml` + `service-{qa,prod}.yaml`.
4. Rota no `infra-gtw-kong` (`manifests/{qa,prod}/scrapy.yaml`).

O scrapy sobe **antes** de qualquer serviço cliente (`ms-inventory`,
`ms-administrative-core`, ...) que dependa dele no boot — ver fase 5 do plano.

## Por que não StatefulSet para o Postgres

Réplica única, `PersistentVolumeClaim` `ReadWriteOnce`, `strategy: Recreate`: é o mesmo
padrão já usado em `ms-administrative-core/k8s/postgres-qa.yaml`. Projeto pequeno, um único
Postgres — StatefulSet, operator (Zalando/CloudNativePG) ou HA de banco viram
over-engineering aqui; sobem quando o volume de escrita ou o RPO exigirem.
