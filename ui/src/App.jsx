import React, { useEffect, useState } from "react";
import { api } from "./api.js";
import {
  ATTR_PRESETS,
  buildSetEntryPayload,
  condValueToText,
  CUSTOM_ATTR,
  displayNative,
  displayValueForType,
  emptyCond,
  emptyRule,
  isFreeText,
  parseValueForType,
  presetForAttr,
  RULE_OPS,
  textToCondValue,
} from "./entryValue.js";
import "./App.css";

function Logo() {
  return (
    <div className="logo">
      <span className="logo-mark">Z</span>
      <span className="logo-text">scrapy</span>
    </div>
  );
}

function Login({ onLoggedIn }) {
  const [email, setEmail] = useState("admin@scrapy.local");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  async function submit(e) {
    e.preventDefault();
    try {
      const res = await api.login(email, password);
      onLoggedIn(res);
    } catch (err) {
      setError(err.message);
    }
  }

  return (
    <div className="login-screen">
      <form className="login-card" onSubmit={submit}>
        <Logo />
        <input className="field" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="email" />
        <input className="field" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="senha" type="password" />
        <button className="btn btn-primary" type="submit">entrar</button>
        {error && <p className="error-text">{error}</p>}
      </form>
    </div>
  );
}

const OP_LABELS = {
  eq: "é igual a",
  neq: "é diferente de",
  gte: "é maior ou igual a",
  lte: "é menor ou igual a",
  in: "está na lista",
};

// Só flags e conteúdo têm segmentação — são os únicos tipos que o app mobile mostra pra
// usuário final via /v1/evaluate. Env (string/number/json) é config de serviço, não faz
// sentido "segmentar" um timeout por país.
function supportsSegmentation(entryType) {
  return entryType === "bool" || entryType === "markdown";
}

// Editor de segmentos (rules: [{when:[{attr,op,value}], then}]). Modelo mental: "todos os
// usuários" recebem o valor padrão (linha de cima, fora daqui); cada segmento abaixo
// SOBRESCREVE esse padrão só para quem bate com as condições. O primeiro segmento que
// bater vence — ver internal/eval/eval.go.
function RulesEditor({ rules, entryType, onChange }) {
  function updateRule(i, next) {
    onChange(rules.map((r, idx) => (idx === i ? next : r)));
  }
  function removeRule(i) {
    onChange(rules.filter((_, idx) => idx !== i));
  }
  function addRule() {
    onChange([...rules, emptyRule(entryType)]);
  }

  function updateCond(ruleIdx, condIdx, patch) {
    const rule = rules[ruleIdx];
    const when = rule.when.map((c, idx) => (idx === condIdx ? { ...c, ...patch } : c));
    updateRule(ruleIdx, { ...rule, when });
  }
  function addCond(ruleIdx) {
    const rule = rules[ruleIdx];
    updateRule(ruleIdx, { ...rule, when: [...rule.when, emptyCond()] });
  }
  function removeCond(ruleIdx, condIdx) {
    const rule = rules[ruleIdx];
    updateRule(ruleIdx, { ...rule, when: rule.when.filter((_, idx) => idx !== condIdx) });
  }

  return (
    <div className="rules-editor">
      <p className="msg-pill" style={{ margin: "0 0 12px", lineHeight: 1.5 }}>
        <strong>Todos os usuários</strong> recebem o valor padrão (campo "valor" da linha
        acima). Cada segmento abaixo <strong>sobrescreve</strong> esse padrão só pra quem
        bate com as condições — o primeiro segmento que bater vence.
      </p>
      {rules.length === 0 && (
        <p className="empty-state" style={{ padding: 16 }}>Nenhum segmento além do padrão. Todo mundo vê o mesmo valor.</p>
      )}
      {rules.map((rule, ruleIdx) => (
        <div className="rule-card" key={ruleIdx}>
          <input
            className="field rule-name"
            placeholder="nome do segmento (ex: usuários iOS no Brasil)"
            value={rule.name ?? ""}
            onChange={(e) => updateRule(ruleIdx, { ...rule, name: e.target.value })}
          />
          <div className="rule-conds">
            <span className="rule-label">quem entra nesse segmento — onde</span>
            {rule.when.map((cond, condIdx) => {
              const preset = presetForAttr(cond.attr);
              const isCustom = !preset;
              return (
                <div className="rule-cond-block" key={condIdx}>
                  <div className="rule-cond">
                    {condIdx > 0 && <span className="rule-and">e</span>}
                    <select
                      className="field"
                      value={preset ? preset.key : CUSTOM_ATTR}
                      onChange={(e) => {
                        const nextKey = e.target.value;
                        const nextPreset = presetForAttr(nextKey);
                        updateCond(ruleIdx, condIdx, {
                          attr: nextKey === CUSTOM_ATTR ? "" : nextKey,
                          value: nextPreset?.options ? nextPreset.options[0] : "",
                        });
                      }}
                      style={{ minWidth: 150 }}
                    >
                      {ATTR_PRESETS.map((p) => <option key={p.key} value={p.key}>{p.label}</option>)}
                      <option value={CUSTOM_ATTR}>Outro atributo…</option>
                    </select>
                    {(isCustom || cond.attr === "") && (
                      <input
                        className="field"
                        placeholder="nome do atributo (ex: deviceType)"
                        value={cond.attr}
                        onChange={(e) => updateCond(ruleIdx, condIdx, { attr: e.target.value })}
                        style={{ minWidth: 150 }}
                      />
                    )}
                    <select
                      className="field"
                      value={cond.op}
                      onChange={(e) => updateCond(ruleIdx, condIdx, { op: e.target.value, value: textToCondValue(e.target.value, condValueToText(cond)) })}
                    >
                      {RULE_OPS.map((op) => <option key={op} value={op}>{OP_LABELS[op]}</option>)}
                    </select>
                    {preset?.options && cond.op === "in" ? (
                      <div className="rule-checkbox-group">
                        {preset.options.map((opt) => {
                          const selected = Array.isArray(cond.value) && cond.value.includes(opt);
                          return (
                            <label key={opt} className="rule-checkbox">
                              <input
                                type="checkbox"
                                checked={selected}
                                onChange={(e) => {
                                  const current = Array.isArray(cond.value) ? cond.value : [];
                                  const next = e.target.checked ? [...current, opt] : current.filter((v) => v !== opt);
                                  updateCond(ruleIdx, condIdx, { value: next });
                                }}
                              />
                              {opt}
                            </label>
                          );
                        })}
                      </div>
                    ) : preset?.options ? (
                      <select
                        className="field"
                        value={condValueToText(cond)}
                        onChange={(e) => updateCond(ruleIdx, condIdx, { value: textToCondValue(cond.op, e.target.value) })}
                      >
                        {preset.options.map((opt) => <option key={opt} value={opt}>{opt}</option>)}
                      </select>
                    ) : (
                      <input
                        className="field"
                        placeholder={cond.op === "in" ? "valores, separados, por vírgula" : "valor"}
                        value={condValueToText(cond)}
                        onChange={(e) => updateCond(ruleIdx, condIdx, { value: textToCondValue(cond.op, e.target.value) })}
                        style={{ minWidth: 150 }}
                      />
                    )}
                    <button className="btn btn-ghost" onClick={() => removeCond(ruleIdx, condIdx)}>×</button>
                  </div>
                  {preset?.hint && (
                    <span className={`attr-hint${preset.unavailable ? " attr-unavailable" : ""}`}>
                      {preset.unavailable ? "⚠ " : ""}{preset.hint}
                    </span>
                  )}
                </div>
              );
            })}
            <button className="btn btn-ghost" style={{ width: "auto" }} onClick={() => addCond(ruleIdx)}>+ condição</button>
          </div>
          <div className="rule-then">
            <span className="rule-label">esse segmento recebe</span>
            {entryType === "bool" ? (
              <select
                className="field"
                value={String(rule.then)}
                onChange={(e) => updateRule(ruleIdx, { ...rule, then: e.target.value === "true" })}
              >
                <option value="true">ligado</option>
                <option value="false">desligado</option>
              </select>
            ) : (
              <input
                className="field"
                placeholder="conteúdo pra esse segmento"
                value={displayNative(entryType, rule.then)}
                onChange={(e) => updateRule(ruleIdx, { ...rule, then: parseValueForType(entryType, e.target.value) })}
                style={{ minWidth: 200 }}
              />
            )}
            <button className="btn btn-ghost" onClick={() => removeRule(ruleIdx)}>remover segmento</button>
          </div>
        </div>
      ))}
      <button className="btn btn-primary" style={{ width: "auto" }} onClick={addRule}>+ novo segmento</button>
    </div>
  );
}

function EntryRow({ entry, scope, env, onSaved }) {
  const [value, setValue] = useState(displayValueForType(entry.type, entry.value));
  const [rules, setRules] = useState(entry.rules ?? []);
  const [showRules, setShowRules] = useState(false);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState("");

  async function save() {
    if (env === "prod" && !confirm(`Confirma alterar "${entry.key}" em PRODUÇÃO?`)) return;
    setSaving(true);
    setErr("");
    try {
      await api.setEntry(buildSetEntryPayload(entry, scope, env, value, rules));
      onSaved();
    } catch (e) {
      setErr(e.message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <tr>
        <td>{entry.key}</td>
        <td><span className="badge">{entry.type}</span></td>
        <td>
          {entry.secret && <span className="badge badge-secret">secret</span>}
          {entry.bootOnly && <span className="badge" style={{ marginLeft: 4 }}>boot</span>}
          {!entry.secret && !entry.bootOnly && <span className="badge">runtime</span>}
        </td>
        <td>v{entry.version}</td>
        <td>
          {entry.type === "bool" ? (
            <input
              type="checkbox"
              checked={value === "true"}
              onChange={(e) => setValue(String(e.target.checked))}
              disabled={entry.secret}
            />
          ) : entry.type === "markdown" ? (
            <textarea
              className="value-input"
              rows={3}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              disabled={entry.secret}
            />
          ) : (
            <input className="value-input" value={value} onChange={(e) => setValue(e.target.value)} disabled={entry.secret} />
          )}
        </td>
        <td>
          <button className="btn btn-ghost" onClick={save} disabled={saving || entry.secret}>salvar</button>
          {supportsSegmentation(entry.type) && (
            <button className="btn btn-ghost" style={{ marginLeft: 4 }} onClick={() => setShowRules((v) => !v)} disabled={entry.secret}>
              segmentação{rules.length > 0 ? ` (${rules.length})` : ""}
            </button>
          )}
          {err && <span className="error-text" style={{ marginLeft: 8 }}>{err}</span>}
        </td>
      </tr>
      {showRules && supportsSegmentation(entry.type) && (
        <tr>
          <td colSpan={6}>
            <RulesEditor rules={rules} entryType={entry.type} onChange={setRules} />
          </td>
        </tr>
      )}
    </>
  );
}

// cada aba cadastra um shape de entry diferente: um form genérico com select de tipo
// não faz sentido quando a aba já fixa o tipo (flag é sempre bool, conteúdo é sempre
// markdown) — só "env" precisa escolher entre string/number/json.
function useCreateEntry(scope, env, onCreated) {
  const [err, setErr] = useState("");

  async function create(key, type, rawValue, secret) {
    setErr("");
    if (!key.trim()) {
      setErr("chave é obrigatória");
      return false;
    }
    if (env === "prod" && !confirm(`Confirma criar "${key.trim()}" em PRODUÇÃO?`)) {
      return false;
    }
    try {
      const parsed = parseValueForType(type, rawValue);
      await api.setEntry({
        Scope: scope,
        Env: env,
        Key: key.trim(),
        Type: type,
        Value: parsed,
        Rules: [],
        Secret: secret,
      });
      onCreated();
      return true;
    } catch (e) {
      setErr(e.message);
      return false;
    }
  }

  return { create, err };
}

function FlagForm({ scope, env, onCreated }) {
  const [key, setKey] = useState("");
  const [on, setOn] = useState(false);
  const { create, err } = useCreateEntry(scope, env, onCreated);

  async function submit() {
    if (await create(key, "bool", String(on), false)) {
      setKey("");
      setOn(false);
    }
  }

  return (
    <div className="new-entry-form">
      <input className="field" placeholder="nome da flag" value={key} onChange={(e) => setKey(e.target.value)} />
      <label>
        <input type="checkbox" checked={on} onChange={(e) => setOn(e.target.checked)} />
        ligada por padrão
      </label>
      <button className="btn btn-primary" style={{ width: "auto" }} onClick={submit}>+ criar flag</button>
      {err && <span className="error-text">{err}</span>}
    </div>
  );
}

function EnvForm({ scope, env, onCreated }) {
  const [key, setKey] = useState("");
  const [type, setType] = useState("string");
  const [value, setValue] = useState("");
  const [secret, setSecret] = useState(false);
  const { create, err } = useCreateEntry(scope, env, onCreated);

  async function submit() {
    if (await create(key, type, value, secret)) {
      setKey("");
      setValue("");
      setSecret(false);
    }
  }

  return (
    <div className="new-entry-form">
      <input className="field" placeholder="chave" value={key} onChange={(e) => setKey(e.target.value)} />
      <select className="field" value={type} onChange={(e) => { setType(e.target.value); setValue(""); }}>
        <option value="string">string</option>
        <option value="number">number</option>
        <option value="json">json</option>
      </select>
      <input
        className="field"
        placeholder={isFreeText(type) ? "valor (texto)" : "valor (JSON)"}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        style={{ minWidth: 180 }}
      />
      <label>
        <input type="checkbox" checked={secret} onChange={(e) => setSecret(e.target.checked)} />
        secret
      </label>
      <button className="btn btn-primary" style={{ width: "auto" }} onClick={submit}>+ criar env</button>
      {err && <span className="error-text">{err}</span>}
    </div>
  );
}

function ContentForm({ scope, env, onCreated }) {
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const { create, err } = useCreateEntry(scope, env, onCreated);

  async function submit() {
    if (await create(key, "markdown", value, false)) {
      setKey("");
      setValue("");
    }
  }

  return (
    <div className="new-entry-form" style={{ alignItems: "flex-start" }}>
      <input className="field" placeholder="chave" value={key} onChange={(e) => setKey(e.target.value)} />
      <textarea
        className="field"
        placeholder="conteúdo (markdown)"
        rows={3}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        style={{ minWidth: 280, flex: 1 }}
      />
      <button className="btn btn-primary" style={{ width: "auto" }} onClick={submit}>+ criar conteúdo</button>
      {err && <span className="error-text">{err}</span>}
    </div>
  );
}

function EntryTable({ entries, scope, env, onSaved, emptyLabel }) {
  if (entries.length === 0) {
    return <div className="empty-state">{emptyLabel}</div>;
  }
  return (
    <table className="card-table">
      <thead>
        <tr><th>key</th><th>tipo</th><th>flags</th><th>versão</th><th>valor</th><th></th></tr>
      </thead>
      <tbody>
        {entries.map((e) => (
          <EntryRow key={e.key} entry={e} scope={scope} env={env} onSaved={onSaved} />
        ))}
      </tbody>
    </table>
  );
}

function Sidebar({ scopes, activeScope, onSelect, onAddScope, onShowAudit, showingAudit, onLogout }) {
  const [draft, setDraft] = useState("");

  function add() {
    const name = draft.trim();
    if (!name) return;
    onAddScope(name);
    setDraft("");
  }

  return (
    <aside className="sidebar">
      <Logo />
      <div className="sidebar-section-title">Serviços</div>
      {scopes.map((s) => (
        <button
          key={s}
          className={`scope-item${s === activeScope && !showingAudit ? " active" : ""}`}
          onClick={() => onSelect(s)}
        >
          {s}
        </button>
      ))}
      <div className="scope-add">
        <input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && add()}
          placeholder="novo serviço"
        />
        <button className="btn btn-ghost" onClick={add}>+</button>
      </div>

      <div className="sidebar-section-title">Sistema</div>
      <button className={`scope-item${showingAudit ? " active" : ""}`} onClick={onShowAudit}>
        Auditoria
      </button>

      <div style={{ marginTop: "auto", paddingTop: 16 }}>
        <button className="btn btn-ghost" style={{ width: "100%" }} onClick={onLogout}>
          sair
        </button>
      </div>
    </aside>
  );
}

function AuditView() {
  const [rows, setRows] = useState([]);
  const [err, setErr] = useState("");

  useEffect(() => {
    api.audit().then(setRows).catch((e) => setErr(e.message));
  }, []);

  return (
    <div>
      <div className="main-title" style={{ marginBottom: 16 }}>Auditoria</div>
      {err && <p className="error-text">{err}</p>}
      {rows.length === 0 ? (
        <div className="empty-state">Nenhum evento registrado ainda.</div>
      ) : (
        <table className="card-table">
          <thead>
            <tr><th>quando</th><th>ator</th><th>ação</th><th>entry</th></tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.ID}>
                <td>{new Date(r.At).toLocaleString("pt-BR")}</td>
                <td>{r.ActorID || "—"}</td>
                <td><span className="badge">{r.Action}</span></td>
                <td>{r.EntryID || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function Dashboard({ onLogout }) {
  const [scopes, setScopes] = useState([]);
  const [envs, setEnvs] = useState(["qa", "prod"]);
  const [scope, setScope] = useState("");
  const [env, setEnv] = useState("qa");
  const [tab, setTab] = useState("env"); // "env" | "flags" | "content"
  const [entries, setEntries] = useState([]);
  const [msg, setMsg] = useState("");
  const [showAudit, setShowAudit] = useState(false);

  useEffect(() => {
    api.listScopes().then((res) => {
      setScopes(res.scopes || []);
      if (res.envs && res.envs.length) setEnvs(res.envs);
      if (!scope && res.scopes && res.scopes.length) setScope(res.scopes[0]);
    });
  }, []);

  async function load() {
    if (!scope) return;
    try {
      setEntries(await api.listEntries(scope, env));
    } catch (e) {
      setMsg(e.message);
    }
  }

  useEffect(() => { load(); }, [scope, env]);

  function addScope(name) {
    setScopes((prev) => (prev.includes(name) ? prev : [...prev, name]));
    setScope(name);
  }

  async function kill() {
    if (!confirm(`Desligar TODOS os flags booleanos de ${scope}/${env}?`)) return;
    const res = await api.kill(scope, env);
    setMsg(`${res.flagsDisabled} flags desligados`);
    load();
  }

  const contentEntries = entries.filter((e) => e.type === "markdown");
  const flagEntries = entries.filter((e) => e.type === "bool");
  const envEntries = entries.filter((e) => e.type !== "markdown" && e.type !== "bool");
  const isProd = env === "prod";

  return (
    <div className={`app-shell${isProd ? " env-prod" : ""}`}>
      <Sidebar
        scopes={scopes}
        activeScope={scope}
        onSelect={(s) => { setScope(s); setShowAudit(false); }}
        onAddScope={addScope}
        onShowAudit={() => setShowAudit(true)}
        showingAudit={showAudit}
        onLogout={onLogout}
      />
      <main className={`main${isProd && !showAudit ? " env-prod" : ""}`}>
        {showAudit ? (
          <AuditView />
        ) : (
        <>
        <div className="main-header">
          <div className="main-title">{scope || "selecione um serviço"}</div>
          <div className="env-pills">
            {envs.map((e) => (
              <button
                key={e}
                className={`env-pill${e === env ? " active" : ""}${e === env && e === "prod" ? " env-prod-active" : ""}`}
                onClick={() => setEnv(e)}
              >
                {e}
              </button>
            ))}
          </div>
        </div>

        <div className={`env-banner ${isProd ? "prod" : "qa"}`}>
          {isProd ? "⚠ ambiente de produção — mudanças afetam usuários reais" : "ambiente qa — seguro para testes"}
        </div>

        <div className="tabs">
          <button className={`tab${tab === "env" ? " active" : ""}`} onClick={() => setTab("env")}>
            Envs
          </button>
          <button className={`tab${tab === "flags" ? " active" : ""}`} onClick={() => setTab("flags")}>
            Flags
          </button>
          <button className={`tab${tab === "content" ? " active" : ""}`} onClick={() => setTab("content")}>
            Conteúdo
          </button>
        </div>

        <div className="toolbar">
          <button className="btn btn-ghost" onClick={load}>recarregar</button>
          {tab === "flags" && (
            <button className="btn btn-danger" onClick={kill}>kill switch</button>
          )}
          {msg && <span className="msg-pill">{msg}</span>}
        </div>

        {scope && tab === "env" && <EnvForm scope={scope} env={env} onCreated={load} />}
        {scope && tab === "flags" && <FlagForm scope={scope} env={env} onCreated={load} />}
        {scope && tab === "content" && <ContentForm scope={scope} env={env} onCreated={load} />}

        {tab === "env" && (
          <EntryTable entries={envEntries} scope={scope} env={env} onSaved={load} emptyLabel="Nenhuma env cadastrada aqui." />
        )}
        {tab === "flags" && (
          <EntryTable entries={flagEntries} scope={scope} env={env} onSaved={load} emptyLabel="Nenhuma flag cadastrada aqui." />
        )}
        {tab === "content" && (
          <EntryTable entries={contentEntries} scope={scope} env={env} onSaved={load} emptyLabel="Nenhum conteúdo cadastrado aqui." />
        )}
        </>
        )}
      </main>
    </div>
  );
}

// O gate real de acesso é o backend (cookie httpOnly + sessionAuth): esse estado só
// reflete o que o servidor confirmou, nunca é assumido a partir da resposta do login.
export default function App() {
  const [session, setSession] = useState(undefined); // undefined = verificando, null = sem sessão

  useEffect(() => {
    api.me().then(setSession).catch(() => setSession(null));
  }, []);

  if (session === undefined) {
    return <div className="login-screen">verificando sessão…</div>;
  }
  if (!session) {
    return <Login onLoggedIn={() => api.me().then(setSession).catch(() => setSession(null))} />;
  }

  async function logout() {
    // revoga a sessão no servidor (jti no deny-list), não só limpa estado local
    await api.logout().catch(() => {});
    setSession(null);
  }

  return <Dashboard onLogout={logout} />;
}
