package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storage "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
)

// rawTestKDF keeps the Argon2id cost negligible in tests.
var rawTestKDF = rawseal.KDFParams{Algorithm: "argon2id", Version: 19, Time: 1, MemoryKiB: 64, Threads: 1}

const rawTestPassword = "raw sealing test password"

type rawTestKey struct {
	id      int64
	private []byte
	public  []byte
}

func newRawTestKey(t *testing.T, id int64) (rawTestKey, storage.NewRawSealingKey) {
	t.Helper()
	private, public, err := rawseal.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := rawseal.WrapPassword(private, []byte(rawTestPassword), rawTestKDF, id, public)
	if err != nil {
		t.Fatal(err)
	}
	kdfJSON, err := wrapped.KDFJSON()
	if err != nil {
		t.Fatal(err)
	}
	return rawTestKey{id: id, private: private, public: public}, storage.NewRawSealingKey{
		KeyID: id, PublicKey: public,
		Envelopes: []storage.RawKeyEnvelope{{
			Kind: rawseal.KindPassword, KDFJSON: kdfJSON, Salt: wrapped.Salt, Nonce: wrapped.Nonce, Wrapped: wrapped.Wrapped,
		}},
	}
}

func createRawTestKey(t *testing.T, store *Store, id int64) rawTestKey {
	t.Helper()
	key, stored := newRawTestKey(t, id)
	if err := store.CreateRawSealingKey(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	return key
}

// openRawPart opens a raw_v1 part the way the vault does, from the private key.
func openRawPart(t *testing.T, key rawTestKey, blob storage.AuditBlob) string {
	t.Helper()
	if blob.Sealing != storage.AuditSealingRawV1 || blob.RawKeyID != key.id {
		t.Fatalf("%s is sealed %q to key %d, want raw_v1 to %d", blob.Direction, blob.Sealing, blob.RawKeyID, key.id)
	}
	partKey, err := rawseal.OpenBlobKey(key.private, rawseal.BlobKeyInfo(string(blob.RequestID), string(blob.Direction)), blob.WrappedKey)
	if err != nil {
		t.Fatalf("open %s part key: %v", blob.Direction, err)
	}
	plain, err := storage.OpenAuditBlob(partKey, blob.Nonce, blob.Ciphertext)
	if err != nil {
		t.Fatalf("open %s part: %v", blob.Direction, err)
	}
	return string(plain)
}

func blobsByDirection(t *testing.T, store *Store, id contract.RequestID) map[storage.AuditDirection]storage.AuditBlob {
	t.Helper()
	blobs, err := store.GetAuditBlobsByRequest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	byDirection := map[storage.AuditDirection]storage.AuditBlob{}
	for _, blob := range blobs {
		byDirection[blob.Direction] = blob
	}
	return byDirection
}

func insertRawTestBlob(t *testing.T, store *Store, blob storage.AuditBlob, exposure storage.AuditExposure) {
	t.Helper()
	blob.Exposure = exposure
	if err := store.InsertAuditBlob(context.Background(), blob); err != nil {
		t.Fatal(err)
	}
}

// insertInlineBlob writes a part the way releases before shared payloads did.
func insertInlineBlob(t *testing.T, database interface {
	Exec(string, ...any) (sql.Result, error)
}, blob storage.AuditBlob, exposure storage.AuditExposure) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO audit_blobs (request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at, exposure)
VALUES (?, ?, ?, ?, ?, 0, ?, '2026-09-20T00:00:00Z', ?)`,
		blob.RequestID, blob.Direction, blob.MediaType, blob.Nonce, blob.Ciphertext, blob.CapturedBytes, string(exposure)); err != nil {
		t.Fatal(err)
	}
}

func insertRecord(t *testing.T, store *Store, id contract.RequestID, status contract.RequestStatus) {
	t.Helper()
	audit := contract.NotCapturedAuditSummary()
	audit.RequestBodyCaptured, audit.ResponseContentCaptured = true, true
	audit.UpstreamRequestBodyCaptured, audit.UpstreamResponseContentCaptured = true, true
	audit.RequestBodyTruncated = true
	if err := store.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: id, StartedAt: store.now(), Status: status,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: audit,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRawSealingSchemaRejectsMalformedRows(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	key := createRawTestKey(t, store, 11)
	insertRecord(t, store, "request_schema", contract.RequestStatusSucceeded)
	for name, statement := range map[string]string{
		"second key":       `INSERT INTO raw_sealing_keys (id, public_key, pk_mac, created_at) VALUES (12, zeroblob(32), zeroblob(31), 'now')`,
		"short public key": `INSERT INTO raw_sealing_keys (id, public_key, pk_mac, created_at) VALUES (12, zeroblob(31), zeroblob(32), 'now')`,
		"local envelope": `INSERT INTO raw_key_envelopes (key_id, kind, kdf_json, salt, nonce, wrapped, created_at)
VALUES (11, 'local', '{}', zeroblob(32), zeroblob(12), x'01', 'now')`,
		"password without salt": `UPDATE raw_key_envelopes SET salt = NULL WHERE kind = 'password'`,
		"unknown kind":          `INSERT INTO raw_key_envelopes (key_id, kind, nonce, wrapped, created_at) VALUES (11, 'recovery', zeroblob(12), x'01', 'now')`,
		"raw payload without key": `INSERT INTO audit_payloads (request_id, content_key, nonce, ciphertext, sealing)
VALUES ('request_schema', zeroblob(32), zeroblob(12), x'01', 'raw_v1')`,
		"raw payload short wrap": `INSERT INTO audit_payloads (request_id, content_key, nonce, ciphertext, sealing, key_id, wrapped_key)
VALUES ('request_schema', zeroblob(32), zeroblob(12), x'01', 'raw_v1', 11, zeroblob(79))`,
		"audit payload with wrap": `INSERT INTO audit_payloads (request_id, content_key, nonce, ciphertext, sealing, key_id, wrapped_key)
VALUES ('request_schema', zeroblob(32), zeroblob(12), x'01', 'audit', 11, zeroblob(80))`,
		"raw payload null wrap": `INSERT INTO audit_payloads (request_id, content_key, nonce, ciphertext, sealing, key_id)
VALUES ('request_schema', zeroblob(32), zeroblob(12), x'01', 'raw_v1', 11)`,
		"unknown sealing": `INSERT INTO audit_payloads (request_id, content_key, nonce, ciphertext, sealing)
VALUES ('request_schema', zeroblob(32), zeroblob(12), x'01', 'raw_v2')`,
	} {
		if name == "second key" {
			// Only the store enforces one key; the schema allows history.
			continue
		}
		if _, err := store.db.Exec(statement); err == nil {
			t.Fatalf("%s: the schema accepted it", name)
		}
	}
	if _, err := store.db.Exec(`INSERT INTO audit_payloads (request_id, content_key, nonce, ciphertext, sealing, key_id, wrapped_key)
VALUES ('request_schema', zeroblob(32), zeroblob(12), x'01', 'raw_v1', 11, zeroblob(80))`); err != nil {
		t.Fatalf("a well-formed raw payload was refused: %v", err)
	}
	_, second := newRawTestKey(t, 12)
	if err := store.CreateRawSealingKey(context.Background(), second); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("a second raw sealing key: %v", err)
	}
	state, err := store.LoadRawSealing(context.Background())
	if err != nil || state.KeyID != key.id || !bytes.Equal(state.PublicKey, key.public) || !state.MACValid || state.Password == nil {
		t.Fatalf("LoadRawSealing = %+v, %v", state, err)
	}
}

func TestRawKeyEnvelopesAreValidatedAndReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	store := openWithKey(t, path, testLocalKey(t, 0x41), nil)
	ctx := context.Background()
	_, missing := newRawTestKey(t, 5)
	if err := store.PutRawKeyEnvelope(ctx, 5, missing.Envelopes[0]); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("an envelope without a key: %v", err)
	}
	key := createRawTestKey(t, store, 5)
	for _, bad := range []storage.RawKeyEnvelope{
		{Kind: "local", Nonce: make([]byte, 12), Wrapped: []byte{1}},
		{Kind: "passkey", Nonce: make([]byte, 12), Wrapped: []byte{1}, KDFJSON: `{}`, Salt: make([]byte, 32)},
		{Kind: rawseal.KindPassword, Nonce: make([]byte, 12), Wrapped: []byte{1}, KDFJSON: `{"alg":"argon2i"}`, Salt: make([]byte, 16)},
		{Kind: "recovery", Nonce: make([]byte, 12), Wrapped: []byte{1}},
	} {
		if err := store.PutRawKeyEnvelope(ctx, key.id, bad); !errors.Is(err, storage.ErrInvalidArgument) {
			t.Fatalf("PutRawKeyEnvelope(%+v) = %v", bad, err)
		}
	}
	// Replacing the password envelope keeps one row per kind.
	replacement, err := rawseal.WrapPassword(key.private, []byte("another raw password"), rawTestKDF, key.id, key.public)
	if err != nil {
		t.Fatal(err)
	}
	kdfJSON, _ := replacement.KDFJSON()
	if err := store.PutRawKeyEnvelope(ctx, key.id, storage.RawKeyEnvelope{
		Kind: rawseal.KindPassword, KDFJSON: kdfJSON, Salt: replacement.Salt, Nonce: replacement.Nonce, Wrapped: replacement.Wrapped,
	}); err != nil {
		t.Fatal(err)
	}
	if count := countRows(t, store, "raw_key_envelopes"); count != 1 {
		t.Fatalf("raw_key_envelopes = %d, want 1", count)
	}
	state, _ := store.LoadRawSealing(ctx)
	parsed, _ := rawseal.ParseKDFJSON(state.Password.KDFJSON)
	envelope := rawseal.PasswordEnvelope{KDF: parsed, Salt: state.Password.Salt, Nonce: state.Password.Nonce, Wrapped: state.Password.Wrapped}
	if _, err := rawseal.UnwrapPassword(envelope, []byte(rawTestPassword), key.id, key.public); !errors.Is(err, rawseal.ErrPassword) {
		t.Fatalf("the old password still opens: %v", err)
	}
	if _, err := rawseal.UnwrapPassword(envelope, []byte("another raw password"), key.id, key.public); err != nil {
		t.Fatalf("the new password does not open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRawCapturesSealToTheRawKey(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_raw_capture")
	auditKey := auditPayloadFixture(t, store, id)
	key := createRawTestKey(t, store, 21)

	request := sealedPayload(t, auditKey, id, storage.AuditDirectionRequest, "raw prompt")
	insertRawTestBlob(t, store, request, storage.AuditExposureRaw)
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, id, storage.AuditDirectionUpstreamRequest, "shareable prompt"), storage.AuditExposureShareable)
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, id, storage.AuditDirectionResponse, "pending answer"), storage.AuditExposurePending)

	blobs := blobsByDirection(t, store, id)
	if got := openRawPart(t, key, blobs[storage.AuditDirectionRequest]); got != "raw prompt" {
		t.Fatalf("raw part = %q", got)
	}
	if len(blobs[storage.AuditDirectionRequest].WrappedKey) != rawseal.WrappedBlobKeyBytes {
		t.Fatal("wrapped key is not 80 bytes")
	}
	if _, err := storage.OpenAuditBlob(auditKey, blobs[storage.AuditDirectionRequest].Nonce, blobs[storage.AuditDirectionRequest].Ciphertext); err == nil {
		t.Fatal("a raw_v1 part opened under the audit key")
	}
	for _, direction := range []storage.AuditDirection{storage.AuditDirectionUpstreamRequest, storage.AuditDirectionResponse} {
		if blobs[direction].Sealing != storage.AuditSealingAudit {
			t.Fatalf("%s is sealed %q, want audit", direction, blobs[direction].Sealing)
		}
	}
	var contentKey []byte
	if err := store.db.QueryRow(`SELECT content_key FROM audit_payloads WHERE sealing = 'raw_v1'`).Scan(&contentKey); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(contentKey, auditContentKey(auditKey, request)) {
		t.Fatal("a raw part is indexed by the keyed digest of its content")
	}

	// A withheld part shows neither ciphertext nor wrapped key to a
	// shareable reader.
	shareable, err := store.GetShareableAuditBlobsByRequest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range shareable {
		if blob.Direction == storage.AuditDirectionRequest && (len(blob.Ciphertext) != 0 || blob.WrappedKey != nil) {
			t.Fatal("the shareable view carries a raw part")
		}
	}

	// A pending part that settles to raw moves onto the raw key.
	if err := store.UpdateAuditExposure(ctx, id, storage.AuditDirectionResponse, storage.AuditExposureRaw); err != nil {
		t.Fatal(err)
	}
	blobs = blobsByDirection(t, store, id)
	if got := openRawPart(t, key, blobs[storage.AuditDirectionResponse]); got != "pending answer" {
		t.Fatalf("settled part = %q", got)
	}
	// A recapture of a raw part stays raw_v1 even when labelled shareable.
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, id, storage.AuditDirectionRequest, "retried prompt"), storage.AuditExposureShareable)
	blobs = blobsByDirection(t, store, id)
	if got := openRawPart(t, key, blobs[storage.AuditDirectionRequest]); got != "retried prompt" || blobs[storage.AuditDirectionRequest].Exposure != storage.AuditExposureRaw {
		t.Fatalf("recaptured part = %q (%s)", got, blobs[storage.AuditDirectionRequest].Exposure)
	}
	// Replaced raw payloads do not linger.
	var rawPayloads int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_payloads WHERE sealing = 'raw_v1'`).Scan(&rawPayloads); err != nil || rawPayloads != 2 {
		t.Fatalf("raw payloads = %d, %v", rawPayloads, err)
	}
	// A raw part that does not decrypt cannot be sealed, so it is not kept:
	// it might open under some other audit key.
	garbage := sealedPayload(t, auditKey, id, storage.AuditDirectionUpstreamResponse, "x")
	garbage.Ciphertext = bytes.Repeat([]byte{0xAB}, 40)
	insertRawTestBlob(t, store, garbage, storage.AuditExposureRaw)
	if blob := blobsByDirection(t, store, id)[storage.AuditDirectionUpstreamResponse]; blob.Sealing != storage.AuditSealingNone || len(blob.Ciphertext) != 0 {
		t.Fatalf("undecryptable part = %+v", blob)
	}
}

func TestResealMovesWithheldPartsOntoTheRawKeyAndScrubsTheOldCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	store := openWithKey(t, path, testLocalKey(t, 0x51), nil)
	defer store.Close()
	ctx := context.Background()
	const (
		done     = contract.RequestID("request_done")
		inFlight = contract.RequestID("request_in_flight")
	)
	auditKey := auditPayloadFixture(t, store, done)
	insertRecord(t, store, inFlight, contract.RequestStatusPending)

	// Raw parts still under the audit key, as an unfinished pass leaves them:
	// one inline and one in a shared payload.
	legacyBody := "LEGACY-" + strings.Repeat("inline raw body ", 64)
	legacy := sealedPayload(t, auditKey, done, storage.AuditDirectionRequest, legacyBody)
	insertInlineBlob(t, store.db, legacy, storage.AuditExposureRaw)
	shared := sealedPayload(t, auditKey, done, storage.AuditDirectionResponse, "shared raw answer")
	insertRawTestBlob(t, store, shared, storage.AuditExposurePending)
	if _, err := store.db.Exec(`UPDATE audit_blobs SET exposure = 'raw' WHERE request_id = ? AND direction = 'response'`, done); err != nil {
		t.Fatal(err)
	}
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, done, storage.AuditDirectionUpstreamResponse, "shareable answer"), storage.AuditExposureShareable)
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, inFlight, storage.AuditDirectionRequest, "in-flight prompt"), storage.AuditExposurePending)
	garbage := sealedPayload(t, auditKey, inFlight, storage.AuditDirectionResponse, "x")
	garbage.Ciphertext = bytes.Repeat([]byte{0xCD}, 40)
	insertInlineBlob(t, store.db, garbage, storage.AuditExposureRaw)

	// Without a key nothing moves, and no raw part is dropped.
	if result, err := store.ResealRawParts(ctx, 0); err != nil || result.Resealed != 0 || result.Dropped != 0 || !result.Done {
		t.Fatalf("reseal without a key = %+v, %v", result, err)
	}
	key := createRawTestKey(t, store, 31)
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, done, storage.AuditDirectionUpstreamRequest, "settled pending"), storage.AuditExposurePending)
	result, err := store.ResealRawParts(ctx, 1)
	if err != nil || result.Resealed != 3 || !result.Done {
		t.Fatalf("ResealRawParts = %+v, %v", result, err)
	}
	blobs := blobsByDirection(t, store, done)
	for direction, want := range map[storage.AuditDirection]string{
		storage.AuditDirectionRequest:         legacyBody,
		storage.AuditDirectionResponse:        "shared raw answer",
		storage.AuditDirectionUpstreamRequest: "settled pending",
	} {
		if got := openRawPart(t, key, blobs[direction]); got != want {
			t.Fatalf("%s = %q", direction, got)
		}
		if blobs[direction].Exposure != storage.AuditExposureRaw {
			t.Fatalf("%s exposure = %s", direction, blobs[direction].Exposure)
		}
	}
	if blobs[storage.AuditDirectionUpstreamResponse].Sealing != storage.AuditSealingAudit ||
		blobs[storage.AuditDirectionUpstreamResponse].Exposure != storage.AuditExposureShareable {
		t.Fatal("a shareable part was resealed")
	}
	pending := blobsByDirection(t, store, inFlight)
	if pending[storage.AuditDirectionRequest].Sealing != storage.AuditSealingAudit ||
		pending[storage.AuditDirectionRequest].Exposure != storage.AuditExposurePending {
		t.Fatal("an in-flight pending part was resealed")
	}
	if pending[storage.AuditDirectionResponse].Sealing != storage.AuditSealingAudit {
		t.Fatal("an undecryptable part was rewritten")
	}
	if count := countRows(t, store, "audit_payloads WHERE sealing = 'audit' AND request_id = 'request_done'"); count != 1 {
		t.Fatalf("old audit payloads left for the finished request: %d", count)
	}
	if fileContains(t, path, legacy.Ciphertext) || fileContains(t, path, shared.Ciphertext) {
		t.Fatal("the database or its WAL still holds a resealed part's old ciphertext")
	}
	// Idempotent: a second pass rewrites nothing.
	var before []byte
	if err := store.db.QueryRow(`SELECT group_concat(hex(wrapped_key)) FROM (SELECT wrapped_key FROM audit_payloads WHERE sealing = 'raw_v1' ORDER BY id)`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	result, err = store.ResealRawParts(ctx, 0)
	if err != nil || result.Resealed != 0 || !result.Done {
		t.Fatalf("second pass = %+v, %v", result, err)
	}
	var after []byte
	if err := store.db.QueryRow(`SELECT group_concat(hex(wrapped_key)) FROM (SELECT wrapped_key FROM audit_payloads WHERE sealing = 'raw_v1' ORDER BY id)`).Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("second pass rewrote parts: %v", err)
	}
	// The in-flight part is resealed once its request finishes.
	if _, err := store.db.Exec(`UPDATE request_records SET status = 'succeeded' WHERE id = ?`, inFlight); err != nil {
		t.Fatal(err)
	}
	if result, err := store.ResealRawParts(ctx, 0); err != nil || result.Resealed != 1 {
		t.Fatalf("reseal after the request finished = %+v, %v", result, err)
	}
	if got := openRawPart(t, key, blobsByDirection(t, store, inFlight)[storage.AuditDirectionRequest]); got != "in-flight prompt" {
		t.Fatalf("finished part = %q", got)
	}
}

func TestResetDropsRawPartsAndClearsOnlyTheirCaptureFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	store := openWithKey(t, path, testLocalKey(t, 0x61), nil)
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_reset")
	insertRecord(t, store, id, contract.RequestStatusSucceeded)
	auditKey, err := store.GetAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE request_records SET privacy_findings_json = '[{"kind":"email","json_path":"$.input","count":1}]' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	first := createRawTestKey(t, store, 41)
	raw := sealedPayload(t, auditKey, id, storage.AuditDirectionRequest, "forgotten prompt")
	insertRawTestBlob(t, store, raw, storage.AuditExposureRaw)
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, id, storage.AuditDirectionUpstreamRequest, "kept prompt"), storage.AuditExposureShareable)
	var rawCiphertext []byte
	if err := store.db.QueryRow(`SELECT ciphertext FROM audit_payloads WHERE sealing = 'raw_v1'`).Scan(&rawCiphertext); err != nil {
		t.Fatal(err)
	}

	second, replacement := newRawTestKey(t, 42)
	result, err := store.ReplaceRawSealingKey(ctx, replacement)
	if err != nil || result.DeletedParts != 1 || result.AffectedRecords != 1 {
		t.Fatalf("ReplaceRawSealingKey = %+v, %v", result, err)
	}
	blobs := blobsByDirection(t, store, id)
	if _, ok := blobs[storage.AuditDirectionRequest]; ok || blobs[storage.AuditDirectionUpstreamRequest].Sealing != storage.AuditSealingAudit {
		t.Fatalf("after reset: %+v", blobs)
	}
	record, err := store.GetRequestRecord(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if record.Audit.RequestBodyCaptured || record.Audit.RequestBodyTruncated || !record.Audit.UpstreamRequestBodyCaptured || !record.Audit.ResponseContentCaptured {
		t.Fatalf("capture flags after reset: %+v", record.Audit)
	}
	var findings string
	if err := store.db.QueryRow(`SELECT privacy_findings_json FROM request_records WHERE id = ?`, id).Scan(&findings); err != nil || findings != `[{"kind":"email","json_path":"$.input","count":1}]` {
		t.Fatalf("privacy findings = %q, %v", findings, err)
	}
	if fileContains(t, path, rawCiphertext) {
		t.Fatal("the database or its WAL still holds a reset part")
	}
	state, err := store.LoadRawSealing(ctx)
	if err != nil || state.KeyID != second.id || !bytes.Equal(state.PublicKey, second.public) || countRows(t, store, "raw_key_envelopes") != 1 {
		t.Fatalf("after reset LoadRawSealing = %+v, %v", state, err)
	}
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, id, storage.AuditDirectionResponse, "new answer"), storage.AuditExposureRaw)
	blob := blobsByDirection(t, store, id)[storage.AuditDirectionResponse]
	if got := openRawPart(t, second, blob); got != "new answer" {
		t.Fatalf("new capture = %q", got)
	}
	if _, err := rawseal.OpenBlobKey(first.private, rawseal.BlobKeyInfo(string(id), "response"), blob.WrappedKey); err == nil {
		t.Fatal("the old key opens a capture made after the reset")
	}
}

func TestRawPublicKeyThatFailsItsMACIsNotUsed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	localKey := testLocalKey(t, 0x71)
	store := openWithKey(t, path, localKey, nil)
	key := createRawTestKey(t, store, 51)
	if !store.HasRawSealingKey() {
		t.Fatal("a created key is not loaded")
	}
	// Swap in an attacker's public key without the audit key.
	_, attacker, err := rawseal.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE raw_sealing_keys SET public_key = ?`, attacker); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var logs []string
	store = openWithKey(t, path, localKey, &logs)
	defer store.Close()
	if store.HasRawSealingKey() {
		t.Fatal("a public key that fails its MAC was loaded")
	}
	if !strings.Contains(strings.Join(logs, "\n"), "raw sealing key does not match") {
		t.Fatalf("logs = %q", logs)
	}
	state, err := store.LoadRawSealing(context.Background())
	if err != nil || state.MACValid {
		t.Fatalf("LoadRawSealing = %+v, %v", state, err)
	}
	const id = contract.RequestID("request_mac")
	auditKey := auditPayloadFixture(t, store, id)
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, id, storage.AuditDirectionRequest, "not kept"), storage.AuditExposureRaw)
	if blob := blobsByDirection(t, store, id)[storage.AuditDirectionRequest]; blob.Sealing != storage.AuditSealingNone || len(blob.Ciphertext) != 0 {
		t.Fatalf("a raw part was kept without a verified public key: %+v", blob)
	}
	// The vault restores the real public key after a proof, then refreshes.
	if _, err := store.db.Exec(`UPDATE raw_sealing_keys SET public_key = ?`, key.public); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshRawSealingMAC(context.Background(), key.id); err != nil || !store.HasRawSealingKey() {
		t.Fatalf("RefreshRawSealingMAC: %v", err)
	}
	if err := store.RefreshRawSealingMAC(context.Background(), key.id+1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("refresh of a missing key: %v", err)
	}
}

func TestASettledPartTheStoreCannotResealGoesToTheVault(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_raw_deferred")
	auditKey := auditPayloadFixture(t, store, id)
	key := createRawTestKey(t, store, 22)
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, id, storage.AuditDirectionResponse, "pending answer"), storage.AuditExposurePending)
	insertRawTestBlob(t, store, sealedPayload(t, auditKey, id, storage.AuditDirectionUpstreamResponse, "pending upstream"), storage.AuditExposurePending)
	retries := 0
	store.OnResealDeferred(func() { retries++ })

	// A settle reseal that succeeds hands nothing on.
	if err := store.UpdateAuditExposure(ctx, id, storage.AuditDirectionUpstreamResponse, storage.AuditExposureRaw); err != nil {
		t.Fatal(err)
	}
	if blob := blobsByDirection(t, store, id)[storage.AuditDirectionUpstreamResponse]; retries != 0 || blob.Sealing != storage.AuditSealingRawV1 {
		t.Fatalf("settled part: retries=%d sealing=%q", retries, blob.Sealing)
	}

	// A row the reseal cannot read makes it fail after the label changed.
	if _, err := store.db.Exec(`UPDATE audit_blobs SET created_at = 'unreadable' WHERE request_id = ? AND direction = ?`, id, string(storage.AuditDirectionResponse)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAuditExposure(ctx, id, storage.AuditDirectionResponse, storage.AuditExposureRaw); err != nil {
		t.Fatal(err)
	}
	var exposure, sealing string
	if err := store.db.QueryRow(`SELECT b.exposure, p.sealing FROM audit_blobs b JOIN audit_payloads p ON p.id = b.payload_id
WHERE b.request_id = ? AND b.direction = ?`, id, string(storage.AuditDirectionResponse)).Scan(&exposure, &sealing); err != nil {
		t.Fatal(err)
	}
	if retries != 1 || exposure != string(storage.AuditExposureRaw) || sealing != string(storage.AuditSealingAudit) {
		t.Fatalf("deferred part: retries=%d exposure=%q sealing=%q", retries, exposure, sealing)
	}

	// The retry pass moves the part once the row reads again.
	if _, err := store.db.Exec(`UPDATE audit_blobs SET created_at = ? WHERE request_id = ? AND direction = ?`, time.Now().UTC().Format(time.RFC3339Nano), id, string(storage.AuditDirectionResponse)); err != nil {
		t.Fatal(err)
	}
	if result, err := store.ResealRawParts(ctx, 8); err != nil || result.Resealed != 1 {
		t.Fatalf("reseal = %+v, %v", result, err)
	}
	if got := openRawPart(t, key, blobsByDirection(t, store, id)[storage.AuditDirectionResponse]); got != "pending answer" {
		t.Fatalf("resealed part = %q", got)
	}

	store.OnResealDeferred(nil)
	store.deferReseal()
	if retries != 1 {
		t.Fatalf("an unregistered retry still ran: %d", retries)
	}
}
