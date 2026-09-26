import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ATTR_PRESETS,
  buildSetEntryPayload,
  condValueToText,
  displayNative,
  displayValueForType,
  emptyRule,
  parseValueForType,
  presetForAttr,
  textToCondValue,
} from "./entryValue.js";

test("buildSetEntryPayload: entry.rules já é array nativo, não string (regressão do bug de salvar)", () => {
  const entry = { key: "checkout_v2_enabled", type: "bool", secret: false, rules: [] };
  const payload = buildSetEntryPayload(entry, "ms-inventory", "qa", "true");
  assert.deepEqual(payload.Rules, []);
  assert.equal(payload.Value, true);
});

test("buildSetEntryPayload: preserva regras de segmentação existentes", () => {
  const entry = {
    key: "banner_promo",
    type: "markdown",
    secret: false,
    rules: [{ attr: "country", op: "eq", value: "BR" }],
  };
  const payload = buildSetEntryPayload(entry, "ms-inventory", "prod", "Promoção!");
  assert.deepEqual(payload.Rules, [{ attr: "country", op: "eq", value: "BR" }]);
  assert.equal(payload.Value, "Promoção!");
});

test("parseValueForType: string/markdown não exigem JSON, viram literal", () => {
  assert.equal(parseValueForType("string", "olá mundo"), "olá mundo");
  assert.equal(parseValueForType("markdown", "# título"), "# título");
});

test("parseValueForType: bool/number/json exigem JSON válido", () => {
  assert.equal(parseValueForType("bool", "true"), true);
  assert.equal(parseValueForType("number", "42"), 42);
  assert.deepEqual(parseValueForType("json", '{"a":1}'), { a: 1 });
  assert.throws(() => parseValueForType("number", "não é json"));
});

test("displayValueForType: dado legado não-JSON não quebra a tela (fallback pro texto cru)", () => {
  assert.equal(displayValueForType("string", "http://monitoring.internal/health"), "http://monitoring.internal/health");
});

test("displayValueForType: valor JSON válido é exibido normalmente", () => {
  assert.equal(displayValueForType("bool", "true"), "true");
  assert.equal(displayValueForType("string", '"olá"'), "olá");
});

test("condValueToText/textToCondValue: op 'in' vira array separado por vírgula, ida e volta", () => {
  const cond = { attr: "country", op: "in", value: ["BR", "PT"] };
  assert.equal(condValueToText(cond), "BR, PT");
  assert.deepEqual(textToCondValue("in", "BR, PT, "), ["BR", "PT"]);
});

test("condValueToText/textToCondValue: outros ops tratam valor como texto simples", () => {
  const cond = { attr: "appVersion", op: "eq", value: "1.2.0" };
  assert.equal(condValueToText(cond), "1.2.0");
  assert.equal(textToCondValue("eq", "1.2.0"), "1.2.0");
});

test("displayNative: 'then' de regra já vem nativo, não passa por JSON.parse", () => {
  assert.equal(displayNative("bool", true), "true");
  assert.equal(displayNative("string", "banner novo"), "banner novo");
  assert.equal(displayNative("json", { a: 1 }), '{"a":1}');
});

test("emptyRule: valor base do 'then' casa com o tipo da entry", () => {
  assert.equal(emptyRule("bool").then, false);
  assert.equal(emptyRule("string").then, "");
  assert.deepEqual(emptyRule("bool").when, [{ attr: "", op: "eq", value: "" }]);
});

test("buildSetEntryPayload: aceita override de rules (edição via RulesEditor)", () => {
  const entry = { key: "banner_promo", type: "string", secret: false, rules: [] };
  const newRules = [{ when: [{ attr: "country", op: "eq", value: "BR" }], then: "promo-br" }];
  const payload = buildSetEntryPayload(entry, "ms-inventory", "prod", "base", newRules);
  assert.deepEqual(payload.Rules, newRules);
});

test("presetForAttr: acha o preset certo por key, undefined pra attr custom", () => {
  assert.equal(presetForAttr("unitId").label, "Sede / Unidade");
  assert.equal(presetForAttr("userId").label, "Usuário");
  assert.equal(presetForAttr("algum_attr_customizado"), undefined);
});

test("presetForAttr: cargo (role) expõe as opções válidas", () => {
  assert.deepEqual(presetForAttr("role").options, ["MANAGER", "EMPLOYEE"]);
});

test("presetForAttr: empresa (organizationId) vem marcada como indisponível", () => {
  assert.equal(presetForAttr("organizationId").unavailable, true);
});

test("ATTR_PRESETS: toda entrada tem key e label", () => {
  for (const p of ATTR_PRESETS) {
    assert.ok(p.key, `preset sem key: ${JSON.stringify(p)}`);
    assert.ok(p.label, `preset sem label: ${JSON.stringify(p)}`);
  }
});
