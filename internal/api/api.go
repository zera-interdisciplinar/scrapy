// Package api wires HTTP handlers (Gin) for the three consumer classes: admin UI (cookie
// session + RBAC), SDKs (API key, bootstrap + WebSocket), and mobile (public evaluate).
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"

	"github.com/zera/scrapy/internal/auth"
	"github.com/zera/scrapy/internal/eval"
	"github.com/zera/scrapy/internal/hub"
	"github.com/zera/scrapy/internal/store"
)

type Server struct {
	Store      *store.Store
	Hub        *hub.Hub
	SessionKey []byte
	UI         http.FileSystem
}

func (s *Server) Routes() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	r.GET("/v1/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.POST("/v1/auth/login", s.handleLogin)

	sdk := r.Group("/v1", s.apiKeyAuth())
	sdk.GET("/bootstrap", s.handleBootstrap)
	sdk.GET("/connect", s.handleConnect)

	r.POST("/v1/evaluate", s.handleEvaluate)

	admin := r.Group("/v1/admin", s.sessionAuth(roleViewer))
	admin.GET("/entries", s.handleListEntries)
	admin.GET("/audit", s.handleAudit)

	editors := r.Group("/v1/admin", s.sessionAuth(roleEditor))
	editors.PUT("/entries", s.handleSetEntry)
	editors.POST("/kill/:scope", s.handleKill)

	admins := r.Group("/v1/admin", s.sessionAuth(roleAdmin))
	admins.POST("/keys", s.handleCreateKey)

	if s.UI != nil {
		r.NoRoute(gin.WrapH(http.FileServer(s.UI)))
	}
	return r
}

// --- auth middleware ---

const (
	roleViewer = "viewer"
	roleEditor = "editor"
	roleAdmin  = "admin"
)

var roleRank = map[string]int{"viewer": 0, "editor": 1, "admin": 2}

func (s *Server) sessionAuth(minRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie("scrapy_session")
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		claims, err := auth.ParseSession(s.SessionKey, cookie)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if roleRank[claims.Role] < roleRank[minRole] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Set("uid", claims.UserID)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// apiKeyAuth authenticates SDK calls. Keys are read-only by construction: there is no
// write endpoint behind this middleware.
func (s *Server) apiKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
			return
		}
		plain := strings.TrimPrefix(h, "Bearer ")
		sum := sha256.Sum256([]byte(plain))
		hash := hex.EncodeToString(sum[:])

		var scope, env string
		err := s.Store.Pool.QueryRow(c.Request.Context(), `
			SELECT sc.name, en.name FROM api_keys k
			JOIN scopes sc ON sc.id = k.scope_id
			JOIN environments en ON en.id = k.env_id
			WHERE k.hash = $1 AND k.revoked_at IS NULL`, hash).Scan(&scope, &env)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}
		c.Set("scope", scope)
		c.Set("env", env)
		c.Next()
	}
}

// --- login ---

func (s *Server) handleLogin(c *gin.Context) {
	var body struct{ Email, Password string }
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	var id, hash, role string
	var mustChange bool
	err := s.Store.Pool.QueryRow(c.Request.Context(), `
		SELECT id, password_hash, role, must_change_password FROM users WHERE email=$1`,
		body.Email).Scan(&id, &hash, &role, &mustChange)
	if err != nil || !auth.VerifyPassword(body.Password, hash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	tok, err := auth.IssueSession(s.SessionKey, id, role, 12*time.Hour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("scrapy_session", tok, 12*3600, "/", "", true, true)
	c.JSON(http.StatusOK, gin.H{"mustChangePassword": mustChange, "role": role})
}

// --- SDK: bootstrap (replaces env:) ---

func (s *Server) handleBootstrap(c *gin.Context) {
	scope, env := c.GetString("scope"), c.GetString("env")
	entries, err := s.Store.ListByScope(c.Request.Context(), scope, env)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	out := make(map[string]json.RawMessage, len(entries))
	for _, e := range entries {
		out[e.Key] = e.Value
	}
	c.JSON(http.StatusOK, out)
}

// --- SDK: WebSocket (hello/state/change/ack) ---

type helloMsg struct {
	Type     string   `json:"type"`
	Scope    string   `json:"scope"`
	Instance string   `json:"instance"`
	Image    string   `json:"image"`
	Keys     []string `json:"keys"`
}

type stateMsg struct {
	Type    string                     `json:"type"`
	Entries map[string]json.RawMessage `json:"entries"`
	Version int                        `json:"version"`
}

type ackMsg struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
}

func (s *Server) handleConnect(c *gin.Context) {
	scope, env := c.GetString("scope"), c.GetString("env")

	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	wsCtx := c.Request.Context()

	cl := &hub.Client{Send: make(chan []byte, 32)}

	var hello helloMsg
	if err := wsjson.Read(wsCtx, conn, &hello); err == nil {
		cl.Instance = hello.Instance
		_ = s.Store.UpsertInstance(wsCtx, scope, env, hello.Instance, hello.Image)
	}

	entries, err := s.Store.ListByScope(wsCtx, scope, env)
	if err == nil {
		state := stateMsg{Type: "state", Entries: map[string]json.RawMessage{}}
		maxVer := 0
		for _, e := range entries {
			state.Entries[e.Key] = e.Value
			if e.Version > maxVer {
				maxVer = e.Version
			}
		}
		state.Version = maxVer
		wsjson.Write(wsCtx, conn, state)
	}

	s.Hub.Register(scope, env, cl)
	defer s.Hub.Unregister(scope, env, cl)

	go func() {
		for msg := range cl.Send {
			if conn.Write(wsCtx, websocket.MessageText, msg) != nil {
				return
			}
		}
	}()

	for {
		var ack ackMsg
		if err := wsjson.Read(wsCtx, conn, &ack); err != nil {
			return
		}
		if ack.Type == "ack" && cl.Instance != "" {
			_ = s.Store.AckInstance(wsCtx, cl.Instance, ack.Version)
		}
	}
}

// --- mobile ---

func (s *Server) handleEvaluate(c *gin.Context) {
	var body struct {
		Scope string                 `json:"scope"`
		Attrs map[string]interface{} `json:"attrs"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	entries, err := s.Store.ListByScope(c.Request.Context(), body.Scope, "prod")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	out := make(map[string]json.RawMessage, len(entries))
	for _, e := range entries {
		if e.Secret {
			continue // mobile never receives secret entries
		}
		out[e.Key] = eval.Resolve(e.Value, e.Rules, body.Attrs)
	}
	payload, _ := json.Marshal(out)
	etag := `"` + hashETag(payload) + `"`
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("ETag", etag)
	c.Data(http.StatusOK, "application/json", payload)
}

func hashETag(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// --- admin ---

func (s *Server) handleListEntries(c *gin.Context) {
	scope, env := c.Query("scope"), c.Query("env")
	entries, err := s.Store.ListByScope(c.Request.Context(), scope, env)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for i := range entries {
		if entries[i].Secret {
			entries[i].Value = json.RawMessage(`"••••••••"`)
		}
	}
	c.JSON(http.StatusOK, entries)
}

func (s *Server) handleSetEntry(c *gin.Context) {
	var body struct {
		Scope, Env, Key, Type string
		Value                 json.RawMessage
		Rules                 json.RawMessage
		Secret                bool
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	e, err := s.Store.Upsert(c.Request.Context(), body.Scope, body.Env, body.Key, body.Type,
		body.Value, body.Rules, body.Secret, c.GetString("uid"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, e)
}

func (s *Server) handleKill(c *gin.Context) {
	scope := c.Param("scope")
	env := c.Query("env")
	n, err := s.Store.KillScope(c.Request.Context(), scope, env, c.GetString("uid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"flagsDisabled": n})
}

func (s *Server) handleAudit(c *gin.Context) {
	rows, err := s.Store.Pool.Query(c.Request.Context(), `
		SELECT id, actor_id, action, entry_id, before, after, at
		FROM audit_log ORDER BY at DESC LIMIT 200`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	type row struct {
		ID, ActorID, Action, EntryID string
		Before, After                json.RawMessage
		At                           time.Time
	}
	var out []row
	for rows.Next() {
		var rr row
		var actorID, entryID *string
		if err := rows.Scan(&rr.ID, &actorID, &rr.Action, &entryID, &rr.Before, &rr.After, &rr.At); err != nil {
			continue
		}
		if actorID != nil {
			rr.ActorID = *actorID
		}
		if entryID != nil {
			rr.EntryID = *entryID
		}
		out = append(out, rr)
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleCreateKey(c *gin.Context) {
	var body struct{ Scope, Env string }
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	plain, hash := auth.NewAPIKey()
	var scopeID, envID string
	if err := s.Store.Pool.QueryRow(c.Request.Context(), `SELECT id FROM scopes WHERE name=$1`, body.Scope).Scan(&scopeID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown scope"})
		return
	}
	if err := s.Store.Pool.QueryRow(c.Request.Context(), `SELECT id FROM environments WHERE name=$1`, body.Env).Scan(&envID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown environment"})
		return
	}
	if _, err := s.Store.Pool.Exec(c.Request.Context(), `
		INSERT INTO api_keys (scope_id, env_id, prefix, hash) VALUES ($1,$2,$3,$4)`,
		scopeID, envID, plain[:9], hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// plaintext key is returned exactly once; only the hash persists
	c.JSON(http.StatusOK, gin.H{"key": plain})
}
