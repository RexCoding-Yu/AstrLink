package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
	storage "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/envelope"
	"github.com/QuantumNous/astrlink/core/internal/storage/migrate"
)

const bodyRequestID = contract.RequestID("request_with_body")

// knownPrompt stands in for a captured prompt; it must only exist encrypted.
const knownPrompt = "ZEBRA-QUARTZ prompt fragment"

type envelopeRow struct {
	kind, createdAt string
	nonce, wrapped  []byte
}

func testLocalKey(t *testing.T, fill byte) []byte {
	t.Helper()
	return bytes.Repeat([]byte{fill}, envelope.KeyBytes)
}

// writeSchemaFixture migrates a database to version without the store's
// key setup.
func writeSchemaFixture(t *testing.T, path string, version int64) {
	t.Helper()
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var migrations []migrate.Migration
	for _, migration := range migrate.DefaultMigrations() {
		if migration.Version <= version {
			migrations = append(migrations, migration)
		}
	}
	runner, err := migrate.New(migrate.SQLDatabase{DB: database}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// writeBodyFixture creates a database with one captured body sealed under
// its audit key and returns that key.
func writeBodyFixture(t *testing.T, path string, localKey []byte) (auditKey []byte) {
	t.Helper()
	ctx := context.Background()
	store := openWithKey(t, path, localKey, nil)
	defer store.Close()
	auditKey, err := store.GetAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: bodyRequestID, StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAuditBlob(ctx, sealedPayload(t, auditKey, bodyRequestID, storage.AuditDirectionRequest, knownPrompt)); err != nil {
		t.Fatal(err)
	}
	return auditKey
}

func openWithKey(t *testing.T, path string, localKey []byte, logs *[]string) *Store {
	t.Helper()
	store, err := Open(context.Background(), path, WithLocalKey(localKey), WithLogger(func(format string, args ...any) {
		if logs != nil {
			*logs = append(*logs, fmt.Sprintf(format, args...))
		}
	}))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func readEnvelopes(t *testing.T, store *Store) map[string]envelopeRow {
	t.Helper()
	rows, err := store.db.Query(`SELECT kind, nonce, wrapped, created_at FROM key_envelopes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	envelopes := map[string]envelopeRow{}
	for rows.Next() {
		var row envelopeRow
		if err := rows.Scan(&row.kind, &row.nonce, &row.wrapped, &row.createdAt); err != nil {
			t.Fatal(err)
		}
		envelopes[row.kind] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return envelopes
}

func countRows(t *testing.T, store *Store, table string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// fileContains reports whether the database or its WAL holds needle.
func fileContains(t *testing.T, path string, needle []byte) bool {
	t.Helper()
	for _, candidate := range []string{path, path + "-wal"} {
		content, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(content, needle) {
			return true
		}
	}
	return false
}

func assertBodyReadable(t *testing.T, store *Store, auditKey []byte) {
	t.Helper()
	key, err := store.GetAuditKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, auditKey) {
		t.Fatal("dek_audit changed")
	}
	assertAuditPlaintexts(t, store, key, bodyRequestID, map[storage.AuditDirection]string{
		storage.AuditDirectionRequest: knownPrompt,
	})
}

func TestRestartKeepsEnvelopesUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	localKey := testLocalKey(t, 0x22)
	auditKey := writeBodyFixture(t, path, localKey)
	store := openWithKey(t, path, localKey, nil)
	before := readEnvelopes(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		var logs []string
		store = openWithKey(t, path, localKey, &logs)
		after := readEnvelopes(t, store)
		if len(after) != len(before) {
			t.Fatalf("restart %d: %d envelopes, want %d", attempt, len(after), len(before))
		}
		for kind, row := range before {
			got := after[kind]
			if !bytes.Equal(got.nonce, row.nonce) || !bytes.Equal(got.wrapped, row.wrapped) || got.createdAt != row.createdAt {
				t.Fatalf("restart %d rewrote the %s envelope", attempt, kind)
			}
		}
		assertBodyReadable(t, store, auditKey)
		if len(logs) != 0 {
			t.Fatalf("restart %d logged %q", attempt, logs)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFreshDatabaseCreatesBothEnvelopes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	store := openWithKey(t, path, testLocalKey(t, 0x33), nil)
	defer store.Close()
	envelopes := readEnvelopes(t, store)
	if _, ok := envelopes[envelope.KindSecrets]; !ok {
		t.Fatal("missing secrets envelope")
	}
	if _, ok := envelopes[envelope.KindAudit]; !ok {
		t.Fatal("missing audit envelope")
	}
	first, err := store.GetOrCreateAuditKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.GetAuditKey(context.Background())
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("GetAuditKey() differs from GetOrCreateAuditKey(): %v", err)
	}
}

func TestOpenWithoutAKeyUsesLocalKeyFileBesideTheDatabase(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "astrlink.db")
	store := openTestStore(t, path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(directory, localkey.FileName))
	if err != nil {
		t.Fatalf("local.key was not created: %v", err)
	}
	key, err := localkey.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	store = openWithKey(t, path, key, nil)
	defer store.Close()
	if store.HasOrphanedAuditKey() || len(readEnvelopes(t, store)) != 2 {
		t.Fatal("the generated local.key does not open its own envelopes")
	}
}

func TestMissingLocalKeySetsEnvelopesAsideAndReportsTheAuditKeyMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	original := testLocalKey(t, 0x44)
	auditKey := writeBodyFixture(t, path, original)

	var logs []string
	replacement := testLocalKey(t, 0x55)
	store := openWithKey(t, path, replacement, &logs)
	envelopes := readEnvelopes(t, store)
	var orphaned []string
	for kind := range envelopes {
		if strings.Contains(kind, ".orphaned.") {
			orphaned = append(orphaned, strings.SplitN(kind, ".", 2)[0])
		}
	}
	if len(envelopes) != 4 || len(orphaned) != 2 {
		t.Fatalf("envelopes = %v, want two current and two orphaned", envelopes)
	}
	if !store.HasOrphanedAuditKey() {
		t.Fatal("HasOrphanedAuditKey() = false after the local key changed")
	}
	key, err := store.GetAuditKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(key, auditKey) {
		t.Fatal("a replacement local key recovered the old audit key")
	}
	blobs, err := store.GetAuditBlobsByRequest(context.Background(), bodyRequestID)
	if err != nil || len(blobs) != 1 {
		t.Fatalf("metadata was lost: %d blobs, %v", len(blobs), err)
	}
	if _, err := storage.OpenAuditBlob(key, blobs[0].Nonce, blobs[0].Ciphertext); !errors.Is(err, storage.ErrAuditDecrypt) {
		t.Fatalf("old body opened under the new key: %v", err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "could not be decrypted") || strings.Contains(logs[0], "key") {
		t.Fatalf("logs = %q", logs)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Restarting with the replacement key is stable.
	store = openWithKey(t, path, replacement, nil)
	if got := len(readEnvelopes(t, store)); got != 4 {
		t.Fatalf("restart with the replacement key changed envelopes to %d", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// The original key coming back restores its envelopes.
	store = openWithKey(t, path, original, nil)
	defer store.Close()
	assertBodyReadable(t, store, auditKey)
	if got := len(readEnvelopes(t, store)); got != 4 {
		t.Fatalf("recovery left %d envelopes, want 4", got)
	}
}

func TestExistingDatabaseOpenNeverCreatesOrSetsAside(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "astrlink.db")
	if _, err := Open(ctx, path, WithLocalKey(testLocalKey(t, 1)), WithExistingDatabase()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database: %v", err)
	}
	if entries, _ := os.ReadDir(directory); len(entries) != 0 {
		t.Fatalf("a missing database left %d file(s) behind", len(entries))
	}
	if _, err := Open(ctx, path, WithExistingDatabase()); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("open without a local key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, localkey.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an existing-database open created a local key")
	}

	store := openWithKey(t, path, testLocalKey(t, 1), nil)
	before := readEnvelopes(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, path, WithLocalKey(testLocalKey(t, 2)), WithExistingDatabase()); !errors.Is(err, ErrLocalKeyMismatch) {
		t.Fatalf("wrong local key: %v", err)
	}
	store, err := Open(ctx, path, WithLocalKey(testLocalKey(t, 1)), WithExistingDatabase())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after := readEnvelopes(t, store)
	if len(after) != len(before) || store.HasOrphanedAuditKey() {
		t.Fatalf("envelopes changed: before=%d after=%d", len(before), len(after))
	}
	for kind, row := range before {
		if !bytes.Equal(after[kind].wrapped, row.wrapped) {
			t.Fatalf("%s envelope was rewritten", kind)
		}
	}
}

// databaseFiles reads the database and its WAL; the -shm index is left out
// because every reader updates it.
func databaseFiles(t *testing.T, path string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, candidate := range []string{path, path + "-wal"} {
		content, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(candidate)] = content
	}
	return files
}

func assertDatabaseFilesUnchanged(t *testing.T, path string, before map[string][]byte) {
	t.Helper()
	after := databaseFiles(t, path)
	for name, content := range before {
		if !bytes.Equal(after[name], content) {
			t.Fatalf("a read-only open changed %s", name)
		}
	}
}

func TestReadOnlyOpenReadsBesideAServingStoreAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "astrlink.db")
	if _, err := Open(ctx, path, WithLocalKey(testLocalKey(t, 1)), WithReadOnly()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database: %v", err)
	}
	if entries, _ := os.ReadDir(directory); len(entries) != 0 {
		t.Fatalf("a missing database left %d file(s) behind", len(entries))
	}

	// A serving Core keeps the database open, with its latest writes still
	// in the WAL.
	serving := openWithKey(t, path, testLocalKey(t, 1), nil)
	defer serving.Close()
	auditKey, err := serving.GetAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := serving.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: bodyRequestID, StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat,
	}); err != nil {
		t.Fatal(err)
	}
	if err := serving.InsertAuditBlob(ctx, sealedPayload(t, auditKey, bodyRequestID, storage.AuditDirectionRequest, knownPrompt)); err != nil {
		t.Fatal(err)
	}
	before := databaseFiles(t, path)
	if len(before["astrlink.db-wal"]) == 0 {
		t.Fatal("the serving store left nothing in the WAL")
	}

	if _, err := Open(ctx, path, WithLocalKey(testLocalKey(t, 2)), WithReadOnly()); !errors.Is(err, ErrLocalKeyMismatch) {
		t.Fatalf("wrong local key: %v", err)
	}
	reader, err := Open(ctx, path, WithLocalKey(testLocalKey(t, 1)), WithReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	assertBodyReadable(t, reader, auditKey)
	if err := reader.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: "request_read_only_write", StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat,
	}); err == nil {
		t.Fatal("a read-only store wrote a request record")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	assertDatabaseFilesUnchanged(t, path, before)
	if err := serving.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: "request_after_reader", StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat,
	}); err != nil {
		t.Fatalf("the serving store cannot write after a reader: %v", err)
	}
}

func TestReadOnlyOpenRefusesADatabaseThatNeedsACoreStart(t *testing.T) {
	ctx := context.Background()
	for _, testCase := range []struct {
		name     string
		write    func(t *testing.T, path string)
		fragment string
	}{
		{"older schema", func(t *testing.T, path string) { writeSchemaFixture(t, path, 42) }, "older"},
		{"no data keys", func(t *testing.T, path string) { writeSchemaFixture(t, path, math.MaxInt64) }, "has not been created"},
		{"plaintext secrets", func(t *testing.T, path string) {
			writeTestBuildFixture(t, path, 42)
			writeSchemaFixture(t, path, math.MaxInt64)
		}, "waiting to be sealed"},
		{"plaintext audit key", func(t *testing.T, path string) {
			writeSchemaFixture(t, path, math.MaxInt64)
			database, err := sql.Open(driverName, sqliteFileDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if _, err := database.Exec(`CREATE TABLE audit_keys (id INTEGER PRIMARY KEY, key_bytes BLOB NOT NULL, created_at TEXT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO audit_keys VALUES (1, ?, '2026-09-20T00:00:00Z')`, testLocalKey(t, 9)); err != nil {
				t.Fatal(err)
			}
		}, "plaintext audit key"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "astrlink.db")
			testCase.write(t, path)
			before := databaseFiles(t, path)
			_, err := Open(ctx, path, WithLocalKey(testLocalKey(t, 1)), WithReadOnly())
			if !errors.Is(err, ErrNeedsCoreStart) || !strings.Contains(err.Error(), testCase.fragment) {
				t.Fatalf("read-only open = %v", err)
			}
			assertDatabaseFilesUnchanged(t, path, before)
			store := openWithKey(t, path, testLocalKey(t, 1), nil)
			defer store.Close()
			if len(readEnvelopes(t, store)) != 2 {
				t.Fatal("a normal open after the refusal did not set up the data keys")
			}
		})
	}
}
