// Conversão entry <-> formulário, extraído de App.jsx pra ser testável sem montar React.
// entry.value e entry.rules chegam do backend como json.RawMessage: embutidos como JSON
// nativo na resposta (array/objeto/string já "parseados"), nunca como string escapada
// duas vezes — é o detalhe que causou o bug de "Unexpected end of JSON input" ao salvar
// (JSON.parse(array) coage pra string via toString() antes de tentar parsear).

export function isFreeText(type) {
  return type === "string" || type === "markdown";
}

export function parseValueForType(type, raw) {
  if (isFreeText(type)) return raw;
  try {
    return JSON.parse(raw);
  } catch {
    throw new Error(
      'valor não é JSON válido para o tipo declarado (ex: number = 3, bool = true, json = {"a":1})'
    );
  }
}

export function displayValueForType(type, jsonRaw) {
  try {
    const parsed = JSON.parse(jsonRaw ?? "null");
    return isFreeText(type) ? (parsed ?? "") : JSON.stringify(parsed);
  } catch {
    // dado legado gravado sem passar pela API (ex: inserido direto no banco), não é JSON válido
    return jsonRaw ?? "";
  }
}

// Monta o payload de PUT /v1/admin/entries a partir de uma entry já carregada da API.
// rules, se passado, substitui entry.rules (edição via RulesEditor); senão preserva o que
// já existia. Nenhum dos dois passa por JSON.parse — já chegam nativos do backend.
export function buildSetEntryPayload(entry, scope, env, rawValue, rules) {
  return {
    Scope: scope,
    Env: env,
    Key: entry.key,
    Type: entry.type,
    Value: parseValueForType(entry.type, rawValue),
    Rules: rules ?? entry.rules ?? [],
    Secret: entry.secret,
  };
}

// --- edição de regras de segmentação (rules: [{when:[{attr,op,value}], then}]) ---
// Ver internal/eval/eval.go: primeira regra cujas condições batem todas vence, senão cai
// no valor base da entry. Suportado: eq, neq, gte, lte, in.

export const RULE_OPS = ["eq", "neq", "gte", "lte", "in"];

export function emptyCond() {
  return { attr: "", op: "eq", value: "" };
}

export function emptyRule(entryType) {
  return { name: "", when: [emptyCond()], then: entryType === "bool" ? false : "" };
}

// cond.value já é nativo (string/number/array, conforme salvo antes). "in" usa array;
// os outros ops comparam via toStr/toFloat no backend, então texto simples serve.
export function condValueToText(cond) {
  if (cond.op === "in") {
    return Array.isArray(cond.value) ? cond.value.join(", ") : String(cond.value ?? "");
  }
  return String(cond.value ?? "");
}

export function textToCondValue(op, text) {
  if (op === "in") {
    return text.split(",").map((s) => s.trim()).filter(Boolean);
  }
  return text;
}

// "then" é nativo (mesmo shape do value principal da entry) — não JSON.parse duas vezes.
export function displayNative(type, native) {
  if (isFreeText(type)) return native ?? "";
  return JSON.stringify(native ?? null);
}

// --- dimensões conhecidas de segmentação ---
// Levantado nos serviços irmãos do workspace: ms-administrative-core (JWT carrega sub/
// email/role, sem claim de org/unidade) e ms-inventory (todo request tem header
// X-Unit-Id). organizationId/"empresa" não chega em nenhum request hoje — só existe via
// lookup unit->organization dentro do banco do ms-administrative-core; fica exposto aqui
// como preparação, marcado unavailable, não como algo que já funciona.
export const ATTR_PRESETS = [
  { key: "userId", label: "Usuário", hint: "UUID do usuário (sub do JWT)" },
  { key: "unitId", label: "Sede / Unidade", hint: "UUID da unidade (header X-Unit-Id)" },
  { key: "role", label: "Cargo", hint: "MANAGER ou EMPLOYEE", options: ["MANAGER", "EMPLOYEE"] },
  {
    key: "organizationId",
    label: "Empresa",
    hint: "nenhum serviço envia esse dado hoje — precisa de claim/attr novo antes de funcionar de verdade",
    unavailable: true,
  },
  { key: "country", label: "País", hint: "ex: BR" },
  { key: "appVersion", label: "Versão do app", hint: "ex: 4.2.0" },
];

export const CUSTOM_ATTR = "__custom__";

export function presetForAttr(attrKey) {
  return ATTR_PRESETS.find((p) => p.key === attrKey);
}
