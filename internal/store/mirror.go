package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// mirrorTables is the allowlist of tables the backup_outbox trigger writes for (see
// migrations/003_backup_outbox.sql). tbl/op read from the outbox are only ever used as a
// lookup key into this map, never interpolated into SQL directly.
var mirrorTables = map[string]bool{
	"environments": true, "scopes": true, "entries": true, "entry_versions": true,
	"users": true, "api_keys": true, "audit_log": true, "instances": true,
}

type outboxRow struct {
	ID  int64
	Tbl string
	Op  string
	Raw []byte // original row_to_json output, applied via jsonb_populate_record so Postgres
	// coerces each field with the real column's input function (e.g. timestamptz text -> time)
	// instead of us guessing Go<->PG type mapping.
	Row map[string]interface{} // decoded only to read "id" and enumerate column names
}

// StartMirror mirrors every row written to primary into a second, isolated Postgres (dsn)
// for credential-loss protection. It runs until ctx is done. Safe to run as
// `go store.StartMirror(...)`: any failure to reach or migrate the backup is logged and
// retried every 5s, it never brings the primary down. masterKey is only needed to satisfy
// Store.Open/Migrate; the mirror never encrypts or decrypts, it copies already-encrypted
// secret blobs verbatim.
//
// ponytail: single worker draining a single outbox table, no lag metric. Fine at scrapy's
// write volume; shard the outbox or add a metric if the backup ever falls meaningfully behind.
func StartMirror(ctx context.Context, primary *pgxpool.Pool, dsn string, masterKey []byte) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Printf("backup mirror: bad BACKUP_DB_DSN: %v (mirror disabled)", err)
		return
	}
	// every connection to the backup sets this flag, so migrations and mirrored writes never
	// re-enqueue themselves into the backup's own backup_outbox (see enqueue_backup()).
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET scrapy.mirror = 'on'`)
		return err
	}

	var backupStore *Store
	for {
		if backupStore == nil {
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				log.Printf("backup mirror: connect: %v", err)
			} else if err := pool.Ping(ctx); err != nil {
				log.Printf("backup mirror: ping: %v", err)
				pool.Close()
			} else {
				s := &Store{Pool: pool, masterKey: masterKey}
				if err := s.Migrate(ctx); err != nil {
					log.Printf("backup mirror: migrate: %v", err)
					pool.Close()
				} else {
					backupStore = s
				}
			}
		}
		if backupStore != nil {
			if err := drainOnce(ctx, primary, backupStore.Pool); err != nil {
				log.Printf("backup mirror: %v (will retry)", err)
			}
		}
		select {
		case <-ctx.Done():
			if backupStore != nil {
				backupStore.Close()
			}
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func drainOnce(ctx context.Context, primary, backup *pgxpool.Pool) error {
	rows, err := primary.Query(ctx, `SELECT id, tbl, op, row FROM backup_outbox ORDER BY id LIMIT 500`)
	if err != nil {
		return fmt.Errorf("read outbox: %w", err)
	}
	var batch []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.Tbl, &r.Op, &r.Raw); err != nil {
			rows.Close()
			return fmt.Errorf("scan outbox: %w", err)
		}
		if err := json.Unmarshal(r.Raw, &r.Row); err != nil {
			rows.Close()
			return fmt.Errorf("decode outbox row %d: %w", r.ID, err)
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(batch) == 0 {
		return nil
	}

	tx, err := backup.Begin(ctx)
	if err != nil {
		return fmt.Errorf("backup begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, r := range batch {
		if err := applyMirrorRow(ctx, tx, r); err != nil {
			return fmt.Errorf("apply outbox row %d (%s %s): %w", r.ID, r.Op, r.Tbl, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("backup commit: %w", err)
	}

	ids := make([]int64, len(batch))
	for i, r := range batch {
		ids[i] = r.ID
	}
	if _, err := primary.Exec(ctx, `DELETE FROM backup_outbox WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("ack outbox: %w", err)
	}
	return nil
}

func applyMirrorRow(ctx context.Context, tx pgx.Tx, r outboxRow) error {
	if !mirrorTables[r.Tbl] {
		return fmt.Errorf("table %q not in mirror allowlist", r.Tbl)
	}
	id, ok := r.Row["id"]
	if !ok {
		return fmt.Errorf("row has no id column")
	}

	if r.Op == "DELETE" {
		_, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = $1::uuid`, r.Tbl), id)
		return err
	}

	cols := make([]string, 0, len(r.Row))
	for c := range r.Row {
		cols = append(cols, c)
	}
	sort.Strings(cols) // deterministic column order for readable SQL/logs

	updates := make([]string, 0, len(cols))
	for _, c := range cols {
		if c != "id" {
			updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", c, c))
		}
	}
	// jsonb_populate_record lets Postgres parse each field with the target column's own
	// input function (dates, UUIDs, booleans, ...) instead of us mapping Go types to PG types.
	q := fmt.Sprintf(
		`INSERT INTO %s SELECT * FROM jsonb_populate_record(NULL::%s, $1::jsonb) ON CONFLICT (id) DO UPDATE SET %s`,
		r.Tbl, r.Tbl, strings.Join(updates, ", "),
	)
	_, err := tx.Exec(ctx, q, r.Raw)
	return err
}
