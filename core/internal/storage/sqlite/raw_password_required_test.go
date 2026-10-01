package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	storage "github.com/QuantumNous/astrlink/core/internal/storage"
)

// checkpoint folds the WAL into the database file, as the next checkpoint
// would, so fileContains sees only what is still stored.
func checkpoint(t *testing.T, store *Store) {
	t.Helper()
	if _, err := store.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
}

// assertRawMarker checks that a part is kept only as a withheld marker, with
// nothing left that dek_audit opens.
func assertRawMarker(t *testing.T, store *Store, id contract.RequestID, direction storage.AuditDirection) {
	t.Helper()
	blob, ok := blobsByDirection(t, store, id)[direction]
	if !ok {
		t.Fatalf("%s part is missing", direction)
	}
	if blob.Sealing != storage.AuditSealingNone || blob.Exposure != storage.AuditExposureRaw || len(blob.Ciphertext) != 0 {
		t.Fatalf("%s part = sealing %q exposure %q with %d ciphertext bytes, want a raw marker",
			direction, blob.Sealing, blob.Exposure, len(blob.Ciphertext))
	}
	if count := countRows(t, store, "audit_blobs WHERE payload_id IS NOT NULL AND request_id = '"+string(id)+"' AND direction = '"+string(direction)+"'"); count != 0 {
		t.Fatalf("%s part still references a payload", direction)
	}
}

func TestRawCapturesAreNotKeptWithoutARawPassword(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, *Store)
	}{
		{name: "no raw key", setup: func(*testing.T, *Store) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "astrlink.db")
			store := openWithKey(t, path, testLocalKey(t, 0x81), nil)
			defer store.Close()
			ctx := context.Background()
			const id = contract.RequestID("request_without_password")
			auditKey := auditPayloadFixture(t, store, id)
			test.setup(t, store)
			if store.keepsRawCaptures() {
				t.Fatal("raw captures are kept without a raw password")
			}

			raw := sealedPayload(t, auditKey, id, storage.AuditDirectionRequest, "RAW-"+strings.Repeat("prompt without password ", 32))
			insertRawTestBlob(t, store, raw, storage.AuditExposureRaw)
			shareable := sealedPayload(t, auditKey, id, storage.AuditDirectionUpstreamRequest, "shareable prompt")
			insertRawTestBlob(t, store, shareable, storage.AuditExposureShareable)

			assertRawMarker(t, store, id, storage.AuditDirectionRequest)
			if fileContains(t, path, raw.Ciphertext) {
				t.Fatal("the database or its WAL holds the raw part's ciphertext")
			}
			// Shareable content is kept as before.
			upstream := blobsByDirection(t, store, id)[storage.AuditDirectionUpstreamRequest]
			if plain, err := storage.OpenAuditBlob(auditKey, upstream.Nonce, upstream.Ciphertext); err != nil || string(plain) != "shareable prompt" {
				t.Fatalf("shareable part = %q, %v", plain, err)
			}

			// Raw is sticky: a recapture labelled shareable is not kept either.
			recapture := sealedPayload(t, auditKey, id, storage.AuditDirectionRequest, "RECAPTURE-"+strings.Repeat("sticky ", 32))
			insertRawTestBlob(t, store, recapture, storage.AuditExposureShareable)
			assertRawMarker(t, store, id, storage.AuditDirectionRequest)
			if fileContains(t, path, recapture.Ciphertext) {
				t.Fatal("the database or its WAL holds the recaptured raw part")
			}
			if result, err := store.ResealRawParts(ctx, 0); err != nil || result.Resealed != 0 {
				t.Fatalf("ResealRawParts = %+v, %v", result, err)
			}
			assertRawMarker(t, store, id, storage.AuditDirectionRequest)
		})
	}
}

func TestPendingPartsAreDroppedWhenTheySettleWithoutARawPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	store := openWithKey(t, path, testLocalKey(t, 0x82), nil)
	defer store.Close()
	ctx := context.Background()
	const (
		settled   = contract.RequestID("request_settled_raw")
		cleared   = contract.RequestID("request_settled_shareable")
		recovered = contract.RequestID("request_interrupted")
		ended     = contract.RequestID("request_ended_unsettled")
		inFlight  = contract.RequestID("request_still_running")
	)
	auditKey := auditPayloadFixture(t, store, settled)
	insertRecord(t, store, cleared, contract.RequestStatusPending)
	pendingBody := func(id contract.RequestID, body string) storage.AuditBlob {
		blob := sealedPayload(t, auditKey, id, storage.AuditDirectionRequest, body+"-"+strings.Repeat("pending body ", 32))
		insertRawTestBlob(t, store, blob, storage.AuditExposurePending)
		return blob
	}

	// A decision that the part is raw drops its content.
	settledBlob := pendingBody(settled, "SETTLED")
	if err := store.UpdateAuditExposure(ctx, settled, storage.AuditDirectionRequest, storage.AuditExposureRaw); err != nil {
		t.Fatal(err)
	}
	assertRawMarker(t, store, settled, storage.AuditDirectionRequest)
	// A decision that clears it keeps it.
	pendingBody(cleared, "CLEARED")
	if err := store.UpdateAuditExposure(ctx, cleared, storage.AuditDirectionRequest, storage.AuditExposureShareable); err != nil {
		t.Fatal(err)
	}
	if blob := blobsByDirection(t, store, cleared)[storage.AuditDirectionRequest]; blob.Sealing != storage.AuditSealingAudit ||
		blob.Exposure != storage.AuditExposureShareable {
		t.Fatalf("cleared part = %+v", blob)
	}
	checkpoint(t, store)
	if fileContains(t, path, settledBlob.Ciphertext) {
		t.Fatal("the settled raw part's content is still stored")
	}

	// A body an interrupted request left pending is dropped by recovery.
	insertRecord(t, store, recovered, contract.RequestStatusPending)
	recoveredBlob := pendingBody(recovered, "RECOVERED")
	if _, err := store.db.Exec(`UPDATE request_records SET status = 'succeeded' WHERE id = ?`, cleared); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecoverPendingRequestRecords(ctx); err != nil {
		t.Fatal(err)
	}
	assertRawMarker(t, store, recovered, storage.AuditDirectionRequest)
	if fileContains(t, path, recoveredBlob.Ciphertext) {
		t.Fatal("recovery kept an interrupted request's pending content")
	}

	// A pending part whose request ended without a decision goes with the
	// next reseal pass; one still in flight waits for its decision.
	insertRecord(t, store, ended, contract.RequestStatusSucceeded)
	endedBlob := pendingBody(ended, "ENDED")
	insertRecord(t, store, inFlight, contract.RequestStatusPending)
	pendingBody(inFlight, "IN-FLIGHT")
	result, err := store.ResealRawParts(ctx, 0)
	if err != nil || result.Dropped != 1 || result.Resealed != 0 || !result.Done {
		t.Fatalf("ResealRawParts = %+v, %v", result, err)
	}
	assertRawMarker(t, store, ended, storage.AuditDirectionRequest)
	if fileContains(t, path, endedBlob.Ciphertext) {
		t.Fatal("the reseal pass kept an ended request's pending content")
	}
	if blob := blobsByDirection(t, store, inFlight)[storage.AuditDirectionRequest]; blob.Sealing != storage.AuditSealingAudit ||
		blob.Exposure != storage.AuditExposurePending {
		t.Fatalf("in-flight part = %+v", blob)
	}
}

func TestAPartAFailedSettleLeftPendingGoesWithItsRequestsEnd(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, *Store) *rawTestKey
		check func(*testing.T, *Store, *rawTestKey, contract.RequestID)
	}{
		{
			name:  "no raw password",
			setup: func(*testing.T, *Store) *rawTestKey { return nil },
			check: func(t *testing.T, store *Store, _ *rawTestKey, id contract.RequestID) {
				assertRawMarker(t, store, id, storage.AuditDirectionRequest)
			},
		},
		{
			name: "raw password set",
			setup: func(t *testing.T, store *Store) *rawTestKey {
				key := createRawTestKey(t, store, 63)
				return &key
			},
			check: func(t *testing.T, store *Store, key *rawTestKey, id contract.RequestID) {
				if got := openRawPart(t, *key, blobsByDirection(t, store, id)[storage.AuditDirectionRequest]); got != "FAILED SETTLE" {
					t.Fatalf("resealed part = %q", got)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openWithKey(t, filepath.Join(t.TempDir(), "astrlink.db"), testLocalKey(t, 0x83), nil)
			defer store.Close()
			ctx := context.Background()
			const (
				id    = contract.RequestID("request_settle_failed")
				ended = contract.RequestID("request_settle_failed_late")
			)
			auditKey := auditPayloadFixture(t, store, "request_settle_fixture")
			key := test.setup(t, store)
			insertRecord(t, store, id, contract.RequestStatusPending)
			insertRawTestBlob(t, store, sealedPayload(t, auditKey, id, storage.AuditDirectionRequest, "FAILED SETTLE"), storage.AuditExposurePending)
			passes := 0
			store.OnResealDeferred(func() {
				passes++
				if _, err := store.ResealRawParts(ctx, 0); err != nil {
					t.Errorf("ResealRawParts: %v", err)
				}
			})
			stillPending := func(when string) {
				t.Helper()
				if blob := blobsByDirection(t, store, id)[storage.AuditDirectionRequest]; blob.Sealing != storage.AuditSealingAudit ||
					blob.Exposure != storage.AuditExposurePending {
					t.Fatalf("%s: part = sealing %q exposure %q", when, blob.Sealing, blob.Exposure)
				}
			}

			// A settle whose context ended first leaves the part pending, and
			// the pass it asks for leaves a part of an in-flight request alone.
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if err := store.UpdateAuditExposure(cancelled, id, storage.AuditDirectionRequest, storage.AuditExposureRaw); err == nil {
				t.Fatal("a settle under an ended context succeeded")
			}
			if passes != 1 {
				t.Fatalf("passes after the failed settle = %d, want 1", passes)
			}
			stillPending("after the failed settle")
			// A pending snapshot does not end the request.
			record := contract.RequestRecord{
				ID: id, StartedAt: store.now(), Status: contract.RequestStatusPending,
				InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
			}
			if err := store.UpsertRequestRecord(ctx, record); err != nil {
				t.Fatal(err)
			}
			if passes != 1 {
				t.Fatalf("passes after a pending snapshot = %d, want 1", passes)
			}

			// The request's end asks for the pass that settles the part.
			record.Status = contract.RequestStatusSucceeded
			if err := store.UpsertRequestRecord(ctx, record); err != nil {
				t.Fatal(err)
			}
			if passes != 2 {
				t.Fatalf("passes after the request ended = %d, want 2", passes)
			}
			test.check(t, store, key, id)
			if err := store.UpsertRequestRecord(ctx, record); err != nil {
				t.Fatal(err)
			}
			if passes != 2 {
				t.Fatalf("a request ended twice asked for %d passes, want 2", passes)
			}

			// A settle that fails after its request ended is covered by the
			// pass it asks for at once and is not remembered.
			insertRecord(t, store, ended, contract.RequestStatusSucceeded)
			insertRawTestBlob(t, store, sealedPayload(t, auditKey, ended, storage.AuditDirectionRequest, "LATE"), storage.AuditExposurePending)
			if err := store.UpdateAuditExposure(cancelled, ended, storage.AuditDirectionRequest, storage.AuditExposureRaw); err == nil {
				t.Fatal("a settle under an ended context succeeded")
			}
			if _, waiting := store.settleDeferred.Load(ended); waiting || passes != 3 {
				t.Fatalf("late settle: remembered=%v passes=%d", waiting, passes)
			}
		})
	}
}
