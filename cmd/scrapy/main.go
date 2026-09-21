package main

import (
	"context"
	"encoding/base64"
	"io/fs"
	"log"
	"net/http"
	"os"

	"github.com/zera/scrapy/internal/api"
	"github.com/zera/scrapy/internal/auth"
	"github.com/zera/scrapy/internal/hub"
	"github.com/zera/scrapy/internal/store"
	uiassets "github.com/zera/scrapy/ui"
)

func main() {
	ctx := context.Background()

	dsn := mustEnv("DB_DSN")
	masterKeyB64 := mustEnv("SCRAPY_MASTER_KEY")
	masterKey, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		log.Fatalf("SCRAPY_MASTER_KEY must be base64: %v", err)
	}
	sessionKey := []byte(mustEnv("SCRAPY_SESSION_SECRET"))

	st, err := store.Open(ctx, dsn, masterKey)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("migrations: %v", err)
	}
	if err := seedAdmin(ctx, st); err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	h := hub.New()
	go h.ListenNotify(ctx, st.Pool, func(scopeID, envID string) (string, string, bool) {
		return st.ResolveNames(ctx, scopeID, envID)
	})

	uiRoot, err := fs.Sub(uiassets.DistFS, "dist")
	if err != nil {
		log.Fatalf("ui embed: %v", err)
	}

	srv := &api.Server{Store: st, Hub: h, SessionKey: sessionKey, UI: http.FS(uiRoot)}
	router := srv.Routes()

	addr := ":" + envOr("PORT", "8080")
	log.Printf("scrapy listening on %s", addr)
	log.Fatal(router.Run(addr))
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing required env %s", k)
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// seedAdmin creates the default admin user directly in the database if none exists.
// Password comes from SCRAPY_BOOTSTRAP_PASSWORD, or is generated and printed to the log
// exactly once — never written to any file in the repository.
func seedAdmin(ctx context.Context, st *store.Store) error {
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	pw := os.Getenv("SCRAPY_BOOTSTRAP_PASSWORD")
	generated := pw == ""
	if generated {
		pw = auth.RandomPassword()
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if _, err := st.Pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, role, must_change_password)
		VALUES ('admin@scrapy.local', $1, 'admin', true)`, hash); err != nil {
		return err
	}
	if generated {
		log.Printf("=== scrapy: generated admin password (shown once): %s ===", pw)
	}
	return nil
}
