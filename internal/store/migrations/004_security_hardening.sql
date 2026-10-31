-- allowed_scopes NULL means "all scopes" (used for the bootstrap admin and any operator
-- who legitimately needs cross-service access). Non-null restricts an editor/admin to the
-- listed scopes only, so one service's credentials can't touch another's config.
ALTER TABLE users ADD COLUMN allowed_scopes TEXT[];

-- session revocation: JWTs are stateless by design, so "log out" or "kill this session"
-- needs an explicit deny-list checked on every request. Rows here are pruned by TTL
-- (expires_at) rather than kept forever.
CREATE TABLE revoked_sessions (
    jti        TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER revoked_sessions_backup AFTER INSERT OR UPDATE OR DELETE ON revoked_sessions FOR EACH ROW EXECUTE FUNCTION enqueue_backup();
