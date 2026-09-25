async function call(path, opts = {}) {
  const res = await fetch(import.meta.env.BASE_URL + path.replace(/^\//, ""), {
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    ...opts,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || res.statusText);
  }
  if (res.status === 204) return null;
  return res.json();
}

export const api = {
  login: (email, password) =>
    call("/v1/auth/login", { method: "POST", body: JSON.stringify({ Email: email, Password: password }) }),
  me: () => call("/v1/auth/me"),
  logout: () => call("/v1/auth/logout", { method: "POST" }),
  listEntries: (scope, env) => call(`/v1/admin/entries?scope=${scope}&env=${env}`),
  listScopes: () => call("/v1/admin/scopes"),
  setEntry: (body) => call("/v1/admin/entries", { method: "PUT", body: JSON.stringify(body) }),
  deleteEntry: (scope, env, key) =>
    call(`/v1/admin/entries?scope=${scope}&env=${env}&key=${encodeURIComponent(key)}`, { method: "DELETE" }),
  kill: (scope, env) => call(`/v1/admin/kill/${scope}?env=${env}`, { method: "POST" }),
  audit: () => call("/v1/admin/audit"),
  createKey: (scope, env) => call("/v1/admin/keys", { method: "POST", body: JSON.stringify({ Scope: scope, Env: env }) }),
};
