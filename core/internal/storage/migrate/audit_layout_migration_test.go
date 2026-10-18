package migrate

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Parts stored before migration 49 read as whole, and the triggers standing
// in for a column CHECK accept exactly the three layouts.
func TestAuditLayoutMigrationKeepsOldPartsWholeAndRejectsUnknownLayouts(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	migrations := DefaultMigrations()
	target := -1
	for position, migration := range migrations {
		if migration.Name == "privacy_token_kinds_audit_chunks" {
			target = position
		}
	}
	if target < 0 {
		t.Fatal("privacy_token_kinds_audit_chunks migration is missing")
	}
	before, err := New(SQLDatabase{DB: database}, migrations[:target])
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO request_records (
    id, started_at, status, input_protocol, streaming, audit_json, created_at
) VALUES ('request_v48', '2026-10-01T00:00:00Z', 'succeeded', 'openai.responses', 0, '{}', '2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO audit_blobs (
    request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at
) VALUES ('request_v48', 'request', 'application/json', ?, ?, 0, 1, '2026-10-01T00:00:00Z')`,
		[]byte("nonce-000000"), []byte("inline ciphertext")); err != nil {
		t.Fatal(err)
	}
	latest, err := New(SQLDatabase{DB: database}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := latest.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	var layout string
	if err := database.QueryRow(`SELECT layout FROM audit_blobs WHERE request_id = 'request_v48' AND direction = 'request'`).
		Scan(&layout); err != nil || layout != "whole" {
		t.Fatalf("existing part layout=%q err=%v, want whole", layout, err)
	}
	insert := `INSERT INTO audit_blobs (
    request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at, exposure, layout
) VALUES ('request_v48', ?, 'application/json', x'', x'', 0, 1, '2026-10-01T00:00:00Z', 'shareable', ?)`
	if _, err := database.Exec(insert, "response", "chunks"); err != nil {
		t.Fatalf("insert chunks layout: %v", err)
	}
	if _, err := database.Exec(insert, "http_meta", "scattered"); err == nil || !strings.Contains(err.Error(), "audit_blobs.layout") {
		t.Fatalf("insert unknown layout err=%v, want the layout trigger to reject it", err)
	}
	update := `UPDATE audit_blobs SET layout = ? WHERE request_id = 'request_v48' AND direction = 'request'`
	if _, err := database.Exec(update, "recipe"); err != nil {
		t.Fatalf("update to recipe layout: %v", err)
	}
	if _, err := database.Exec(update, "scattered"); err == nil || !strings.Contains(err.Error(), "audit_blobs.layout") {
		t.Fatalf("update to unknown layout err=%v, want the layout trigger to reject it", err)
	}
}

// SQLite verifies a CHECK on an added column against every existing row, so
// that ADD COLUMN reads the whole table. On tables that grow with captured
// traffic it outlasted the desktop's ready timeout; enforce new values with
// triggers. Migrations before 49 are applied everywhere and stay as they are.
func TestGrowingTablesGainColumnsWithoutCheck(t *testing.T) {
	growing := map[string]bool{
		"request_records":   true,
		"audit_blobs":       true,
		"audit_payloads":    true,
		"audit_chunks":      true,
		"audit_part_chunks": true,
	}
	addColumn := regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+["\[]?(\w+)["\]]?\s+ADD\s+(?:COLUMN\s+)?(.*)$`)
	check := regexp.MustCompile(`(?i)\bCHECK\s*\(`)
	for _, migration := range DefaultMigrations() {
		if migration.Version < 49 {
			continue
		}
		for _, statement := range migration.Statements {
			match := addColumn.FindStringSubmatch(statement)
			if match != nil && growing[strings.ToLower(match[1])] && check.MatchString(match[2]) {
				t.Errorf("migration %d (%s) adds a column with a CHECK to %s", migration.Version, migration.Name, match[1])
			}
		}
	}
}
