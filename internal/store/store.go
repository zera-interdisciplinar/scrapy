// Package store is the only place that talks to Postgres. There is no cache layer here on
// purpose: the table is small, indexed on (scope_id, env_id), and every read must reflect
// the last write — see the plan's "Postgres é a fonte de verdade" decision.
package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed all:migrations
var migrationsFS embed.FS

// Migrate applies migrations/*.sql in filename order, tracked in schema_migrations.
// No external migration tool: one small ordered loop covers a POC.
// ponytail: no down-migrations; add a real migrator (goose/atlas) if rollback is ever needed.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var already bool
		s.Pool.QueryRow(ctx, `SELECT true FROM schema_migrations WHERE name=$1`, name).Scan(&already)
		if already {
			continue
		}
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := s.Pool.Exec(ctx, string(sqlBytes)); err != nil {
			return err
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES ($1)`, name); err != nil {
			return err
		}
		log.Printf("applied migration %s", name)
	}
	return nil
}

type Store struct {
	Pool      *pgxpool.Pool
	masterKey []byte // AES-256 key for entries marked secret=true
}

func Open(ctx context.Context, dsn string, masterKey []byte) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if len(masterKey) != 32 {
		return nil, errors.New("SCRAPY_MASTER_KEY must decode to 32 bytes (AES-256)")
	}
	return &Store{Pool: pool, masterKey: masterKey}, nil
}

func (s *Store) Close() { s.Pool.Close() }

type Entry struct {
	ID        string          `json:"id"`
	ScopeID   string          `json:"scopeId"`
	EnvID     string          `json:"envId"`
	Key       string          `json:"key"`
	Type      string          `json:"type"`
	Value     json.RawMessage `json:"value"`
	Rules     json.RawMessage `json:"rules"`
	Secret    bool            `json:"secret"`
	BootOnly  bool            `json:"bootOnly"`
	Version   int             `json:"version"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

var validTypes = map[string]bool{"bool": true, "string": true, "number": true, "json": true, "markdown": true}

func (s *Store) encrypt(plain []byte) (string, error) {
	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, plain, nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (s *Store) decrypt(enc string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// ListByScope returns every entry for a scope/environment, decrypting secrets. Used for
// /v1/bootstrap (env replacement) and the WS `state` message.
func (s *Store) ListByScope(ctx context.Context, scope, env string) ([]Entry, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.id, e.scope_id, e.env_id, e.key, e.type, e.value, e.rules,
		       e.secret, e.boot_only, e.version, e.updated_at
		FROM entries e
		JOIN scopes s ON s.id = e.scope_id
		JOIN environments en ON en.id = e.env_id
		WHERE s.name = $1 AND en.name = $2
		ORDER BY e.key`, scope, env)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		var valueEnc string
		if err := rows.Scan(&e.ID, &e.ScopeID, &e.EnvID, &e.Key, &e.Type, &valueEnc,
			&e.Rules, &e.Secret, &e.BootOnly, &e.Version, &e.UpdatedAt); err != nil {
			return nil, err
		}
		if e.Secret {
			plain, err := s.decrypt(valueEnc)
			if err != nil {
				return nil, fmt.Errorf("decrypt %s: %w", e.Key, err)
			}
			e.Value = plain
		} else {
			e.Value = json.RawMessage(valueEnc)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Upsert validates the value against the entry's declared type, encrypts it if secret,
// bumps version, and writes an entry_versions row + audit_log row in one transaction.
func (s *Store) Upsert(ctx context.Context, scope, env, key, typ string, value json.RawMessage, rules json.RawMessage, secret bool, actorID string) (*Entry, error) {
	if !validTypes[typ] {
		return nil, fmt.Errorf("invalid type %q", typ)
	}
	if err := validateValue(typ, value); err != nil {
		return nil, err
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var scopeID, envID string
	if err := tx.QueryRow(ctx, `SELECT id FROM scopes WHERE name=$1`, scope).Scan(&scopeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx, `INSERT INTO scopes(name) VALUES ($1) RETURNING id`, scope).Scan(&scopeID); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM environments WHERE name=$1`, env).Scan(&envID); err != nil {
		return nil, fmt.Errorf("unknown environment %q: %w", env, err)
	}

	storedValue := string(value)
	if secret {
		enc, err := s.encrypt(value)
		if err != nil {
			return nil, err
		}
		storedValue = enc
	}
	if rules == nil {
		rules = json.RawMessage("[]")
	}

	var before json.RawMessage
	var e Entry
	err = tx.QueryRow(ctx, `
		INSERT INTO entries (scope_id, env_id, key, type, value, rules, secret, version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,1)
		ON CONFLICT (scope_id, env_id, key) DO UPDATE
		SET value = EXCLUDED.value, rules = EXCLUDED.rules, type = EXCLUDED.type,
		    secret = EXCLUDED.secret, version = entries.version + 1, updated_at = now()
		RETURNING id, key, type, version, updated_at,
		          (SELECT value FROM entries e2 WHERE e2.id = entries.id) `,
		scopeID, envID, key, typ, storedValue, string(rules), secret,
	).Scan(&e.ID, &e.Key, &e.Type, &e.Version, &e.UpdatedAt, &before)
	if err != nil {
		return nil, err
	}
	e.ScopeID, e.EnvID, e.Value, e.Rules, e.Secret = scopeID, envID, value, rules, secret

	if _, err := tx.Exec(ctx, `
		INSERT INTO entry_versions (entry_id, value, rules, version, actor_id)
		VALUES ($1,$2,$3,$4,$5)`, e.ID, storedValue, string(rules), e.Version, nullable(actorID)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (actor_id, action, entry_id, before, after)
		VALUES ($1,'entry.set',$2,$3,$4)`, nullable(actorID), e.ID, before, storedValue); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &e, nil
}

func nullable(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func validateValue(typ string, value json.RawMessage) error {
	var v interface{}
	if err := json.Unmarshal(value, &v); err != nil {
		return fmt.Errorf("value is not valid JSON: %w", err)
	}
	switch typ {
	case "bool":
		if _, ok := v.(bool); !ok {
			return errors.New("value must be a boolean")
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return errors.New("value must be a number")
		}
	case "string", "markdown":
		if _, ok := v.(string); !ok {
			return errors.New("value must be a string")
		}
	case "json":
		// any valid JSON accepted
	}
	return nil
}

// KillScope flips every bool entry in a scope/env to false in one transaction — the
// feira's red button.
func (s *Store) KillScope(ctx context.Context, scope, env, actorID string) (int, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE entries e SET value = 'false', version = e.version + 1, updated_at = now()
		FROM scopes s, environments en
		WHERE e.scope_id = s.id AND e.env_id = en.id
		  AND s.name = $1 AND en.name = $2 AND e.type = 'bool' AND e.value <> 'false'`, scope, env)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() > 0 {
		_, _ = s.Pool.Exec(ctx, `
			INSERT INTO audit_log (actor_id, action, before, after)
			VALUES ($1, 'scope.kill', $2, 'false')`, nullable(actorID), fmt.Sprintf("%s/%s", scope, env))
	}
	return int(tag.RowsAffected()), nil
}

// UpsertInstance records/refreshes a connected pod (WS hello + periodic touch).
func (s *Store) UpsertInstance(ctx context.Context, scope, env, instance, image string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO instances (scope_id, env_id, instance, image, last_seen)
		SELECT s.id, en.id, $3, $4, now() FROM scopes s, environments en
		WHERE s.name=$1 AND en.name=$2
		ON CONFLICT DO NOTHING`, scope, env, instance, image)
	return err
}

func (s *Store) AckInstance(ctx context.Context, instance string, version int) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE instances SET acked_version=$2, last_seen=now() WHERE instance=$1`, instance, version)
	return err
}

// ResolveNames looks up scope/environment names by id. Scopes/envs are a handful of rows
// created once (services + "qa"/"prod"), so a direct query per notification is cheap and
// needs no invalidation logic.
func (s *Store) ResolveNames(ctx context.Context, scopeID, envID string) (scope, env string, ok bool) {
	err := s.Pool.QueryRow(ctx, `
		SELECT s.name, e.name FROM scopes s, environments e
		WHERE s.id = $1 AND e.id = $2`, scopeID, envID).Scan(&scope, &env)
	return scope, env, err == nil
}
