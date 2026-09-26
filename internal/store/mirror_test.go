package store

import "testing"

// TestApplyMirrorRow_Allowlist locks the mirror's table allowlist to what
// migrations/003_backup_outbox.sql and 004_security_hardening.sql actually trigger on,
// without needing a live Postgres.
func TestApplyMirrorRow_Allowlist(t *testing.T) {
	want := []string{"environments", "scopes", "entries", "entry_versions", "users", "api_keys", "audit_log", "instances", "revoked_sessions"}
	if len(mirrorTables) != len(want) {
		t.Fatalf("mirrorTables has %d entries, want %d", len(mirrorTables), len(want))
	}
	for _, tbl := range want {
		if !mirrorTables[tbl] {
			t.Errorf("mirrorTables missing %q", tbl)
		}
	}

	r := outboxRow{ID: 1, Tbl: "not_a_real_table", Op: "INSERT", Row: map[string]interface{}{"id": "x"}}
	if err := applyMirrorRow(nil, nil, r); err == nil {
		t.Fatal("expected error for table outside the allowlist, got nil")
	}
}
