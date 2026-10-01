package migrate

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestAuditExposureMigrationLabelsV41PartsWithoutTouchingCiphertext(t *testing.T) {
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
	versionFortyOne, err := New(SQLDatabase{DB: database}, migrations[:41])
	if err != nil {
		t.Fatal(err)
	}
	if err := versionFortyOne.Up(context.Background()); err != nil {
		t.Fatalf("migrate to version 41: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO request_records (
    id, started_at, status, input_protocol, streaming, audit_json, created_at
) VALUES ('request_v41', '2026-09-20T00:00:00Z', 'succeeded', 'openai.responses', 0, '{}', '2026-09-20T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// One legacy inline row and one shared-payload row per direction.
	directions := []string{"request", "response", "http_meta", "upstream_request", "upstream_response", "upstream_http_meta"}
	for index, direction := range directions {
		ciphertext := []byte("ciphertext-" + direction)
		if index%2 == 0 {
			if _, err := database.Exec(`INSERT INTO audit_blobs (
    request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at
) VALUES ('request_v41', ?, 'application/json', ?, ?, 0, 1, '2026-09-20T00:00:00Z')`,
				direction, []byte("nonce-000000"), ciphertext); err != nil {
				t.Fatal(err)
			}
			continue
		}
		key := make([]byte, 32)
		key[0] = byte(index)
		result, err := database.Exec(`INSERT INTO audit_payloads (request_id, content_key, nonce, ciphertext)
VALUES ('request_v41', ?, ?, ?)`, key, []byte("nonce-000000"), ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		payloadID, _ := result.LastInsertId()
		if _, err := database.Exec(`INSERT INTO audit_blobs (
    request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at, payload_id
) VALUES ('request_v41', ?, 'application/json', x'', x'', 0, 1, '2026-09-20T00:00:00Z', ?)`,
			direction, payloadID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`UPDATE audit_settings SET request_body_enabled = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		full, err := New(SQLDatabase{DB: database}, migrations)
		if err != nil {
			t.Fatal(err)
		}
		if err := full.Up(context.Background()); err != nil {
			t.Fatalf("upgrade attempt %d: %v", attempt, err)
		}
	}

	want := map[string]string{
		"request":            "raw",
		"response":           "raw",
		"http_meta":          "shareable",
		"upstream_request":   "shareable",
		"upstream_response":  "shareable",
		"upstream_http_meta": "shareable",
	}
	rows, err := database.Query(`SELECT b.direction, b.exposure, COALESCE(p.ciphertext, b.ciphertext)
FROM audit_blobs b LEFT JOIN audit_payloads p ON p.id = b.payload_id
WHERE b.request_id = 'request_v41'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var direction, exposure string
		var ciphertext []byte
		if err := rows.Scan(&direction, &exposure, &ciphertext); err != nil {
			t.Fatal(err)
		}
		seen++
		if exposure != want[direction] {
			t.Fatalf("%s exposure=%q want %q", direction, exposure, want[direction])
		}
		if string(ciphertext) != "ciphertext-"+direction {
			t.Fatalf("%s ciphertext altered: %q", direction, ciphertext)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != len(directions) {
		t.Fatalf("blob rows=%d want %d", seen, len(directions))
	}

	var decision, findings sql.NullString
	if err := database.QueryRow(`SELECT privacy_decision, privacy_findings_json FROM request_records WHERE id = 'request_v41'`).
		Scan(&decision, &findings); err != nil || decision.Valid || findings.Valid {
		t.Fatalf("historical decision=%#v findings=%#v err=%v, want unknown", decision, findings, err)
	}
	var captureEnabled, agentRawAccess int
	if err := database.QueryRow(`SELECT request_body_enabled, agent_raw_access_enabled FROM audit_settings WHERE id = 1`).
		Scan(&captureEnabled, &agentRawAccess); err != nil || captureEnabled != 1 || agentRawAccess != 1 {
		t.Fatalf("settings capture=%d agent_raw_access=%d err=%v", captureEnabled, agentRawAccess, err)
	}

	// The CHECK is final from the first release: pending is accepted,
	// anything else is not.
	if _, err := database.Exec(`UPDATE audit_blobs SET exposure = 'pending' WHERE direction = 'request'`); err != nil {
		t.Fatalf("pending rejected: %v", err)
	}
	if _, err := database.Exec(`UPDATE audit_blobs SET exposure = 'public' WHERE direction = 'request'`); err == nil {
		t.Fatal("unknown exposure accepted")
	}
	if _, err := database.Exec(`UPDATE audit_settings SET agent_raw_access_enabled = 2 WHERE id = 1`); err == nil {
		t.Fatal("non-boolean agent_raw_access_enabled accepted")
	}
}
