package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storage "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/envelope"
)

// Plaintext secrets as a test build stored them before sealing. Each must
// only exist sealed after the first start of a release.
const (
	fixtureServiceID     = contract.ServiceID("service_before_sealing")
	fixtureServiceKey    = "sk-PLAINTEXT-service-key-before-sealing"
	fixtureToolKey       = "tvly-PLAINTEXT-tool-key-before-sealing"
	fixtureProxyPassword = "PLAINTEXT-proxy-password"
	fixtureTokenID       = contract.AccessTokenID("access_token_before_sealing")
)

var fixtureToken = "astr_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))

// writeTestBuildFixture builds a database as a test build left it at
// version: its plaintext audit key in audit_keys, one body sealed under that
// key, and one plaintext row in each secret table. It returns the audit key.
func writeTestBuildFixture(t *testing.T, path string, version int64) (auditKey []byte) {
	t.Helper()
	writeSchemaFixture(t, path, version)
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	auditKey, err = envelope.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	service := contract.ServiceFromEndpoint(testEndpoint(fixtureServiceID))
	service.HTTP.CredentialRef = localRef(fixtureServiceID)
	document, err := encodeService(service)
	if err != nil {
		t.Fatal(err)
	}
	name, nameKey, err := normalizeAccessTokenName("before sealing")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(fixtureToken))
	body := sealedPayload(t, auditKey, bodyRequestID, storage.AuditDirectionRequest, knownPrompt)
	stamp := "2026-09-20T00:00:00Z"
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`CREATE TABLE audit_keys (
    id INTEGER PRIMARY KEY CHECK(id = 1),
    key_bytes BLOB NOT NULL CHECK(length(key_bytes) = 32),
    created_at TEXT NOT NULL
)`, nil},
		{`INSERT INTO audit_keys (id, key_bytes, created_at) VALUES (1, ?, ?)`, []any{auditKey, stamp}},
		{`INSERT INTO request_records (id, started_at, status, input_protocol, streaming, audit_json, created_at)
VALUES (?, ?, 'succeeded', 'openai.responses', 0, '{}', ?)`, []any{bodyRequestID, stamp, stamp}},
		{`INSERT INTO audit_blobs (request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at)
VALUES (?, ?, ?, ?, ?, 0, ?, ?)`, []any{body.RequestID, body.Direction, body.MediaType, body.Nonce, body.Ciphertext, body.CapturedBytes, stamp}},
		{`INSERT INTO services (id, document_json, created_at, updated_at, sort_position) VALUES (?, ?, ?, ?, 0)`,
			[]any{fixtureServiceID, string(document), stamp, stamp}},
		{`INSERT INTO service_credentials (service_id, credential_value, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			[]any{fixtureServiceID, []byte(fixtureServiceKey), stamp, stamp}},
		{`INSERT INTO builtin_tool_credentials (kind, credential_value) VALUES ('web_search', ?)`,
			[]any{[]byte(fixtureToolKey)}},
		{`INSERT INTO service_proxy_credentials (service_id, credential_value) VALUES (?, ?)`,
			[]any{fixtureServiceID, []byte(`{"username":"proxy-user","password":"` + fixtureProxyPassword + `"}`)}},
		{`INSERT INTO local_access_tokens (id, name, name_key, token_hash, token_hint, source, created_at) VALUES (?, ?, ?, ?, ?, 'user', ?)`,
			[]any{fixtureTokenID, name, nameKey, hash[:], accessTokenHint(fixtureToken), stamp}},
		{`INSERT INTO local_access_token_secrets (token_id, token_value) VALUES (?, ?)`,
			[]any{fixtureTokenID, fixtureToken}},
		{`INSERT INTO local_access_token_bootstrap_state (singleton, completed_at) VALUES (1, ?)`,
			[]any{stamp}},
	} {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("fixture %q: %v", statement.query, err)
		}
	}
	return auditKey
}

// writeReleaseRanFixture is a test build's database after a release that
// ignored audit_keys and the plaintext rows ran on it: migrated, with data
// keys of its own. It returns the test build's audit key and the release's.
func writeReleaseRanFixture(t *testing.T, path string, localKey []byte) (legacy, current []byte) {
	t.Helper()
	legacy = writeTestBuildFixture(t, path, 42)
	writeSchemaFixture(t, path, math.MaxInt64)
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, kind := range []string{envelope.KindSecrets, envelope.KindAudit} {
		dek, err := envelope.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		nonce, wrapped, err := envelope.Wrap(localKey, dek, kind)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO key_envelopes (kind, nonce, wrapped, created_at) VALUES (?, ?, ?, '2026-09-30T15:00:00Z')`,
			kind, nonce, wrapped); err != nil {
			t.Fatal(err)
		}
		if kind == envelope.KindAudit {
			current = dek
		}
	}
	return legacy, current
}

func readSealedFlags(t *testing.T, store *Store) map[string]map[string][]byte {
	t.Helper()
	values := map[string]map[string][]byte{}
	for _, column := range secretColumns {
		if !column.flagged {
			continue
		}
		rows, err := store.db.Query(`SELECT ` + column.key + `, ` + column.value + `, sealed FROM ` + column.table)
		if err != nil {
			t.Fatal(err)
		}
		values[column.table] = map[string][]byte{}
		for rows.Next() {
			var key string
			var value []byte
			var sealed bool
			if err := rows.Scan(&key, &value, &sealed); err != nil {
				t.Fatal(err)
			}
			if !sealed {
				t.Fatalf("%s %s is not sealed", column.table, key)
			}
			values[column.table][key] = value
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
	}
	return values
}

func assertFixtureSecretsWork(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	if secret, err := store.Get(ctx, secretstore.Ref(localRef(fixtureServiceID))); err != nil || string(secret) != fixtureServiceKey {
		t.Fatalf("service credential = %v", err)
	}
	if secret, err := store.Get(ctx, "local://builtin-tool/web_search"); err != nil || string(secret) != fixtureToolKey {
		t.Fatalf("tool credential = %v", err)
	}
	if secret, err := store.Get(ctx, secretstore.Ref("local://service-proxy/"+string(fixtureServiceID))); err != nil ||
		!bytes.Contains(secret, []byte(fixtureProxyPassword)) {
		t.Fatalf("proxy credential = %v", err)
	}
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	if tokens, err := manager.List(ctx); err != nil || len(tokens) != 1 {
		t.Fatalf("List = %d tokens, %v", len(tokens), err)
	}
	if id, err := manager.Authenticate(ctx, fixtureToken); err != nil || id != fixtureTokenID {
		t.Fatalf("Authenticate = %q, %v", id, err)
	}
	if revealed, err := manager.Reveal(ctx, fixtureTokenID); err != nil || revealed != fixtureToken {
		t.Fatalf("Reveal = %v", err)
	}
	status, err := store.LocalDataStatus(ctx)
	if err != nil || status.UnreadableCredentials != 0 || status.UnreadableAccessTokens != 0 {
		t.Fatalf("LocalDataStatus = %+v, %v", status, err)
	}
}

func assertNoTestBuildPlaintext(t *testing.T, path string, keys ...[]byte) {
	t.Helper()
	for _, plaintext := range []string{fixtureServiceKey, fixtureToolKey, fixtureProxyPassword, fixtureToken, knownPrompt} {
		if fileContains(t, path, []byte(plaintext)) {
			t.Fatalf("plaintext %q survives in the database file", plaintext[:8])
		}
	}
	for _, key := range keys {
		if fileContains(t, path, key) {
			t.Fatal("a plaintext audit key survives in the database file")
		}
	}
}

func hasTable(t *testing.T, store *Store, name string) bool {
	t.Helper()
	var exists bool
	if err := store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestTestBuildUpgradeSealsSecretsAndAdoptsTheAuditKey(t *testing.T) {
	for _, version := range []int64{40, 41, 42} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "astrlink.db")
			auditKey := writeTestBuildFixture(t, path, version)
			localKey := testLocalKey(t, 0x21)

			var logs []string
			store := openWithKey(t, path, localKey, &logs)
			if len(logs) != 1 || logs[0] != "astrlink storage: sealed 4 stored credential(s) under the local key" {
				t.Fatalf("logs = %q", logs)
			}
			sealed := readSealedFlags(t, store)
			assertFixtureSecretsWork(t, store)
			assertBodyReadable(t, store, auditKey)
			if store.HasOrphanedAuditKey() {
				t.Fatal("an adopted audit key reports an orphaned one")
			}
			if hasTable(t, store, "audit_keys") {
				t.Fatal("audit_keys survives the scrub")
			}
			// The scrub already emptied the WAL; nothing waits for Close.
			assertNoTestBuildPlaintext(t, path, auditKey)
			envelopes := readEnvelopes(t, store)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			assertNoTestBuildPlaintext(t, path, auditKey)

			logs = nil
			store = openWithKey(t, path, localKey, &logs)
			defer store.Close()
			if len(logs) != 0 {
				t.Fatalf("restart logs = %q", logs)
			}
			for table, rows := range readSealedFlags(t, store) {
				for key, value := range rows {
					if !bytes.Equal(value, sealed[table][key]) {
						t.Fatalf("restart rewrote %s %s", table, key)
					}
				}
			}
			for kind, row := range readEnvelopes(t, store) {
				if !bytes.Equal(row.wrapped, envelopes[kind].wrapped) {
					t.Fatalf("restart rewrote the %s envelope", kind)
				}
			}
			assertBodyReadable(t, store, auditKey)
		})
	}
}

func TestReleaseThatIgnoredTheTestBuildIsRepairedOnTheNextStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	localKey := testLocalKey(t, 0x31)
	legacy, current := writeReleaseRanFixture(t, path, localKey)

	var logs []string
	store := openWithKey(t, path, localKey, &logs)
	if len(logs) != 2 || !strings.Contains(logs[0], "saved audit data could not be decrypted") ||
		logs[1] != "astrlink storage: sealed 4 stored credential(s) under the local key" {
		t.Fatalf("logs = %q", logs)
	}
	assertFixtureSecretsWork(t, store)
	// Bodies captured since the release keep opening under its key.
	if key, err := store.GetAuditKey(context.Background()); err != nil || !bytes.Equal(key, current) {
		t.Fatalf("dek_audit changed: %v", err)
	}
	if !store.HasOrphanedAuditKey() {
		t.Fatal("bodies under the test build's key do not report a missing key")
	}
	legacyEnvelopes := 0
	for kind, row := range readEnvelopes(t, store) {
		if !strings.HasPrefix(kind, envelope.KindAudit+".legacy.") {
			continue
		}
		legacyEnvelopes++
		if dek, err := envelope.Unwrap(localKey, row.nonce, row.wrapped, envelope.KindAudit); err != nil || !bytes.Equal(dek, legacy) {
			t.Fatalf("the test build's key is not kept wrapped: %v", err)
		}
	}
	if legacyEnvelopes != 1 || hasTable(t, store, "audit_keys") {
		t.Fatalf("legacy envelopes = %d, audit_keys kept = %t", legacyEnvelopes, hasTable(t, store, "audit_keys"))
	}
	assertNoTestBuildPlaintext(t, path, legacy)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	logs = nil
	store = openWithKey(t, path, localKey, &logs)
	defer store.Close()
	if len(logs) != 0 {
		t.Fatalf("restart logs = %q", logs)
	}
	if got := len(readEnvelopes(t, store)); got != 3 {
		t.Fatalf("restart envelopes = %d, want secrets, audit and the legacy key", got)
	}
}

func TestLeftoverLegacyTablesAreScrubbedAndDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	localKey := testLocalKey(t, 0x41)
	store := openWithKey(t, path, localKey, nil)
	// A build that sealed its secrets emptied audit_keys and queued a scrub
	// no release ran.
	mustExec(t, store, `CREATE TABLE audit_keys (id INTEGER PRIMARY KEY CHECK(id = 1), key_bytes BLOB NOT NULL, created_at TEXT NOT NULL)`)
	mustExec(t, store, `CREATE TABLE pending_file_scrub (id INTEGER PRIMARY KEY CHECK(id = 1), requested_at TEXT NOT NULL)`)
	mustExec(t, store, `INSERT INTO pending_file_scrub (id, requested_at) VALUES (1, '2026-09-29T00:00:00Z')`)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var logs []string
	store = openWithKey(t, path, localKey, &logs)
	defer store.Close()
	if len(logs) != 0 || hasTable(t, store, "audit_keys") || hasTable(t, store, "pending_file_scrub") {
		t.Fatalf("logs = %q, audit_keys = %t, pending_file_scrub = %t", logs,
			hasTable(t, store, "audit_keys"), hasTable(t, store, "pending_file_scrub"))
	}
}
