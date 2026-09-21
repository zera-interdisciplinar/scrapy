// Package hub keeps the WebSocket connections per (scope, env) and fans out entry changes
// received from Postgres LISTEN/NOTIFY, so every scrapy replica pushes to its own clients
// regardless of which replica handled the write.
package hub

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Client struct {
	Instance string
	Send     chan []byte
}

type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*Client]bool // key = "scope/env"
}

func New() *Hub {
	return &Hub{clients: make(map[string]map[*Client]bool)}
}

func key(scope, env string) string { return scope + "/" + env }

func (h *Hub) Register(scope, env string, c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := key(scope, env)
	if h.clients[k] == nil {
		h.clients[k] = make(map[*Client]bool)
	}
	h.clients[k][c] = true
}

func (h *Hub) Unregister(scope, env string, c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients[key(scope, env)], c)
	close(c.Send)
}

func (h *Hub) Broadcast(scope, env string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients[key(scope, env)] {
		select {
		case c.Send <- msg:
		default: // slow consumer: drop, it'll get the next `state` on reconnect
		}
	}
}

// entryChangedRow mirrors what the DB trigger emits via pg_notify (row_to_json of `entries`).
type entryChangedRow struct {
	ScopeID string          `json:"scope_id"`
	EnvID   string          `json:"env_id"`
	Key     string          `json:"key"`
	Value   json.RawMessage `json:"value"`
	Version int             `json:"version"`
}

type ChangeMsg struct {
	Type    string          `json:"type"`
	Key     string          `json:"key"`
	Value   json.RawMessage `json:"value"`
	Version int             `json:"version"`
}

// ListenNotify runs for the process lifetime on a dedicated connection, translating
// Postgres notifications into WS broadcasts. Scope/env names are resolved once per event
// against the small in-memory idMap the caller refreshes on scope/env changes (rare).
func (h *Hub) ListenNotify(ctx context.Context, pool *pgxpool.Pool, resolveNames func(scopeID, envID string) (scope, env string, ok bool)) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		log.Printf("hub: acquire conn for LISTEN failed: %v", err)
		return
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN entry_changed"); err != nil {
		log.Printf("hub: LISTEN failed: %v", err)
		return
	}
	for {
		notif, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("hub: wait notification error: %v", err)
			continue
		}
		var row entryChangedRow
		if err := json.Unmarshal([]byte(notif.Payload), &row); err != nil {
			continue
		}
		scope, env, ok := resolveNames(row.ScopeID, row.EnvID)
		if !ok {
			continue
		}
		msg, _ := json.Marshal(ChangeMsg{Type: "change", Key: row.Key, Value: row.Value, Version: row.Version})
		h.Broadcast(scope, env, msg)
	}
}
