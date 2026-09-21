import React, { useEffect, useState } from "react";
import { api } from "./api.js";

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
    <form onSubmit={submit} style={{ maxWidth: 320, margin: "80px auto", fontFamily: "sans-serif" }}>
      <h2>scrapy</h2>
      <input value={email} onChange={(e) => setEmail(e.target.value)} placeholder="email" style={{ width: "100%", marginBottom: 8 }} />
      <input value={password} onChange={(e) => setPassword(e.target.value)} placeholder="senha" type="password" style={{ width: "100%", marginBottom: 8 }} />
      <button type="submit" style={{ width: "100%" }}>entrar</button>
      {error && <p style={{ color: "red" }}>{error}</p>}
    </form>
  );
}

function EntryRow({ entry, scope, env, onSaved }) {
  const [value, setValue] = useState(JSON.stringify(JSON.parse(entry.value ?? "null")));
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState("");

  async function save() {
    setSaving(true);
    setErr("");
    try {
      let parsed;
      try {
        parsed = JSON.parse(value);
      } catch {
        throw new Error("valor não é JSON válido para o tipo declarado");
      }
      await api.setEntry({
        Scope: scope,
        Env: env,
        Key: entry.key,
        Type: entry.type,
        Value: parsed,
        Rules: JSON.parse(entry.rules || "[]"),
        Secret: entry.secret,
      });
      onSaved();
    } catch (e) {
      setErr(e.message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <tr>
      <td>{entry.key}</td>
      <td>{entry.type}</td>
      <td>{entry.bootOnly ? "boot (exige rollout)" : "runtime"}</td>
      <td>v{entry.version}</td>
      <td>
        <input value={value} onChange={(e) => setValue(e.target.value)} disabled={entry.secret} style={{ width: 220 }} />
      </td>
      <td>
        <button onClick={save} disabled={saving || entry.secret}>salvar</button>
        {err && <span style={{ color: "red", marginLeft: 8 }}>{err}</span>}
      </td>
    </tr>
  );
}

function Dashboard() {
  const [scope, setScope] = useState("ms-inventory");
  const [env, setEnv] = useState("qa");
  const [entries, setEntries] = useState([]);
  const [msg, setMsg] = useState("");

  async function load() {
    try {
      setEntries(await api.listEntries(scope, env));
    } catch (e) {
      setMsg(e.message);
    }
  }

  useEffect(() => { load(); }, [scope, env]);

  async function kill() {
    if (!confirm(`Desligar TODOS os flags booleanos de ${scope}/${env}?`)) return;
    const res = await api.kill(scope, env);
    setMsg(`${res.flagsDisabled} flags desligados`);
    load();
  }

  return (
    <div style={{ fontFamily: "sans-serif", maxWidth: 900, margin: "40px auto" }}>
      <h2>scrapy — {scope} / {env}</h2>
      <div style={{ marginBottom: 12 }}>
        <input value={scope} onChange={(e) => setScope(e.target.value)} placeholder="scope" />
        <select value={env} onChange={(e) => setEnv(e.target.value)}>
          <option value="qa">qa</option>
          <option value="prod">prod</option>
        </select>
        <button onClick={load}>recarregar</button>
        <button onClick={kill} style={{ background: "#c0392b", color: "white", marginLeft: 12 }}>
          kill switch
        </button>
        {msg && <span style={{ marginLeft: 12 }}>{msg}</span>}
      </div>
      <table border="1" cellPadding="4" style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead>
          <tr><th>key</th><th>type</th><th>escopo</th><th>versão</th><th>valor</th><th></th></tr>
        </thead>
        <tbody>
          {entries.map((e) => (
            <EntryRow key={e.key} entry={e} scope={scope} env={env} onSaved={load} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

export default function App() {
  const [session, setSession] = useState(null);
  if (!session) return <Login onLoggedIn={setSession} />;
  return <Dashboard />;
}
