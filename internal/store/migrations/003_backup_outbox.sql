-- Outbox for mirroring every write to an isolated backup Postgres (see internal/store/mirror.go).
-- current_setting('scrapy.mirror', true) is set by the mirror worker's own connection so
-- writes it applies to the backup don't re-enqueue themselves there.
CREATE TABLE backup_outbox (
    id         BIGSERIAL PRIMARY KEY,
    tbl        TEXT NOT NULL,
    op         TEXT NOT NULL CHECK (op IN ('INSERT','UPDATE','DELETE')),
    row        JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION enqueue_backup() RETURNS trigger AS $$
BEGIN
    IF current_setting('scrapy.mirror', true) = 'on' THEN
        RETURN COALESCE(NEW, OLD);
    END IF;
    INSERT INTO backup_outbox (tbl, op, row)
    VALUES (TG_TABLE_NAME, TG_OP, row_to_json(COALESCE(NEW, OLD))::jsonb);
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER environments_backup  AFTER INSERT OR UPDATE OR DELETE ON environments  FOR EACH ROW EXECUTE FUNCTION enqueue_backup();
CREATE TRIGGER scopes_backup        AFTER INSERT OR UPDATE OR DELETE ON scopes        FOR EACH ROW EXECUTE FUNCTION enqueue_backup();
CREATE TRIGGER entries_backup       AFTER INSERT OR UPDATE OR DELETE ON entries       FOR EACH ROW EXECUTE FUNCTION enqueue_backup();
CREATE TRIGGER entry_versions_backup AFTER INSERT OR UPDATE OR DELETE ON entry_versions FOR EACH ROW EXECUTE FUNCTION enqueue_backup();
CREATE TRIGGER users_backup         AFTER INSERT OR UPDATE OR DELETE ON users         FOR EACH ROW EXECUTE FUNCTION enqueue_backup();
CREATE TRIGGER api_keys_backup      AFTER INSERT OR UPDATE OR DELETE ON api_keys      FOR EACH ROW EXECUTE FUNCTION enqueue_backup();
CREATE TRIGGER audit_log_backup     AFTER INSERT OR UPDATE OR DELETE ON audit_log     FOR EACH ROW EXECUTE FUNCTION enqueue_backup();
CREATE TRIGGER instances_backup     AFTER INSERT OR UPDATE OR DELETE ON instances     FOR EACH ROW EXECUTE FUNCTION enqueue_backup();
