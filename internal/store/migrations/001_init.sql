CREATE TABLE environments (
    id   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT UNIQUE NOT NULL
);

CREATE TABLE scopes (
    id   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT UNIQUE NOT NULL
);

CREATE TABLE entries (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_id   UUID NOT NULL REFERENCES scopes(id),
    env_id     UUID NOT NULL REFERENCES environments(id),
    key        TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('bool','string','number','json','markdown')),
    value      JSONB NOT NULL,
    rules      JSONB NOT NULL DEFAULT '[]',
    secret     BOOLEAN NOT NULL DEFAULT false,
    boot_only  BOOLEAN NOT NULL DEFAULT false,
    version    INTEGER NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (scope_id, env_id, key)
);

CREATE TABLE entry_versions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entry_id   UUID NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    value      JSONB NOT NULL,
    rules      JSONB NOT NULL,
    version    INTEGER NOT NULL,
    actor_id   UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email               TEXT UNIQUE NOT NULL,
    password_hash       TEXT NOT NULL,
    role                TEXT NOT NULL CHECK (role IN ('viewer','editor','admin')),
    must_change_password BOOLEAN NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_id   UUID NOT NULL REFERENCES scopes(id),
    env_id     UUID NOT NULL REFERENCES environments(id),
    prefix     TEXT NOT NULL,
    hash       TEXT NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id  UUID,
    action    TEXT NOT NULL,
    entry_id  UUID,
    before    JSONB,
    after     JSONB,
    ip        TEXT,
    at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- append-only: no UPDATE/DELETE grant issued to the app role (see 003_roles.sql)

CREATE TABLE instances (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_id      UUID NOT NULL REFERENCES scopes(id),
    env_id        UUID NOT NULL REFERENCES environments(id),
    instance      TEXT NOT NULL,
    image         TEXT,
    acked_version INTEGER,
    connected_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_entries_scope_env ON entries (scope_id, env_id);
CREATE INDEX idx_instances_scope_env ON instances (scope_id, env_id);

-- notify all scrapy replicas on any entry change, for WS fanout across pods
CREATE OR REPLACE FUNCTION notify_entry_change() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('entry_changed', row_to_json(NEW)::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER entries_notify
AFTER INSERT OR UPDATE ON entries
FOR EACH ROW EXECUTE FUNCTION notify_entry_change();

INSERT INTO environments (name) VALUES ('qa'), ('prod');
