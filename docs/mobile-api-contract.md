# Contrato API Mobile — Scrapy

Dois endpoints:

- `POST /v1/boot` — chamado no boot do app, antes do login. Retorna só envs (entries marcadas `boot_only` no admin), sem segmentação.
- `POST /v1/flags` — chamado depois do login. Retorna flags/conteúdo (entries com `boot_only=false`), geral ou individual por chave via `attrs` (mesma mecânica de regras de antes).

Ambos usam a mesma autenticação e o mesmo formato de request/response do antigo `/v1/evaluate` abaixo — só mudou o path e o filtro de quais entries voltam.

## Base URL

```
https://<host>/v1
```

## Autenticação

Header obrigatório em toda chamada:

```
Authorization: Bearer <api_key>
```

- API key é do tipo cliente: só leitura, presa a um `scope` + `env` fixos (definidos no momento em que a key foi criada no admin).
- Key pode ser extraída do app (é pública por natureza) — por isso é somente leitura e tem rate limit por IP (120 req/min).
- Key inválida ou revogada → `401 { "error": "invalid api key" }`.

## Requisição

```
POST /v1/flags
Content-Type: application/json
Authorization: Bearer <api_key>
If-None-Match: "<etag_anterior>"   // opcional, ver caching abaixo
```

Body:

```json
{
  "attrs": {
    "user_id": "abc123",
    "plan": "pro",
    "country": "BR",
    "app_version": "3.2.0"
  }
}
```

- `attrs`: mapa livre chave→valor (string, número, bool). Usado pelo servidor pra bater com as regras de segmentação de cada flag/config. Nunca mande dado sensível aqui — não é criptografado além do TLS.
- Não existe schema fixo de `attrs`; manda só o que os rules do scope realmente usam.

## Resposta

`200 OK`, body é mapa `key -> value` já resolvido pro attrs enviado:

```json
{
  "new_checkout_flow": true,
  "max_retries": 3,
  "banner_config": { "title": "Promo", "color": "#FF0000" }
}
```

- Cada valor pode ser bool, número, string ou objeto — depende de como foi cadastrado no admin.
- Entries marcadas como `secret` nunca aparecem aqui (servidor já filtra).
- Resolução de regra é 100% server-side: mobile nunca vê as regras, só o resultado final pro attrs mandado.

Header de resposta:

```
ETag: "<hash>"
```

## Caching / polling

Guarda o `ETag` recebido. Na próxima chamada manda:

```
If-None-Match: "<etag_guardado>"
```

Se nada mudou, servidor responde `304 Not Modified` (corpo vazio) — evita reprocessar e economiza banda. Se mudou, vem `200` com novo corpo + novo ETag.

## Erros

| Status | Quando |
|---|---|
| 400 | body malformado (`attrs` não é JSON válido) |
| 401 | key ausente, inválida ou revogada |
| 429 | rate limit estourado (120 req/min por IP) |
| 500 | erro interno |

## Exemplo (Swift / URLSession)

```swift
var req = URLRequest(url: URL(string: "https://host/v1/flags")!)
req.httpMethod = "POST"
req.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
req.setValue("application/json", forHTTPHeaderField: "Content-Type")
if let etag = cachedETag {
    req.setValue(etag, forHTTPHeaderField: "If-None-Match")
}
req.httpBody = try! JSONSerialization.data(withJSONObject: ["attrs": attrs])
```

## Exemplo (Kotlin / OkHttp)

```kotlin
val body = JSONObject(mapOf("attrs" to attrs)).toString()
    .toRequestBody("application/json".toMediaType())
val req = Request.Builder()
    .url("https://host/v1/flags")
    .post(body)
    .addHeader("Authorization", "Bearer $apiKey")
    .apply { cachedETag?.let { addHeader("If-None-Match", it) } }
    .build()
```
