package sqlite

import (
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storage "github.com/QuantumNous/astrlink/core/internal/storage"
)

func auditPayloadFixture(t *testing.T, store *Store, id contract.RequestID) []byte {
	t.Helper()
	if err := store.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: id, StartedAt: store.now(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
	}); err != nil {
		t.Fatal(err)
	}
	key, err := store.GetOrCreateAuditKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func sealedPayload(t *testing.T, key []byte, id contract.RequestID, direction storage.AuditDirection, body string) storage.AuditBlob {
	t.Helper()
	nonce, ciphertext, err := storage.SealAuditBlob(key, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	// Only parts a privacy decision cleared share payloads; raw parts are
	// sealed one by one to the raw key.
	return storage.AuditBlob{RequestID: id, Direction: direction, MediaType: "text/event-stream", Nonce: nonce,
		Ciphertext: ciphertext, CapturedBytes: len(body), CreatedAt: time.Now().UTC(),
		Exposure: storage.AuditExposureShareable}
}

func assertPayloadCount(t *testing.T, store *Store, want int) {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_payloads`).Scan(&count); err != nil || count != want {
		t.Fatalf("payload count=%d want=%d err=%v", count, want, err)
	}
}

func assertAuditPlaintexts(t *testing.T, store *Store, key []byte, id contract.RequestID, want map[storage.AuditDirection]string) {
	t.Helper()
	blobs, err := store.GetAuditBlobsByRequest(context.Background(), id)
	if err != nil || len(blobs) != len(want) {
		t.Fatalf("blobs=%d want=%d err=%v", len(blobs), len(want), err)
	}
	for _, blob := range blobs {
		plain, err := storage.OpenAuditBlob(key, blob.Nonce, blob.Ciphertext)
		if err != nil || string(plain) != want[blob.Direction] {
			t.Fatalf("%s plaintext mismatch: %v", blob.Direction, err)
		}
	}
}

func TestSharedAuditPayloadStoresIdenticalBodiesOnce(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"))
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_shared")
	key := auditPayloadFixture(t, store, id)
	body := string(bytes.Repeat([]byte("captured-output\n"), 8192))
	upstream := sealedPayload(t, key, id, storage.AuditDirectionUpstreamResponse, body)
	client := sealedPayload(t, key, id, storage.AuditDirectionResponse, body)
	if bytes.Equal(client.Ciphertext, upstream.Ciphertext) {
		t.Fatal("fixture must use independent encryption")
	}
	for _, blob := range []storage.AuditBlob{upstream, client} {
		if err := store.InsertAuditBlob(ctx, blob); err != nil {
			t.Fatal(err)
		}
	}
	assertPayloadCount(t, store, 1)
	var inlineBytes, payloadBytes int
	if err := store.db.QueryRow(`SELECT SUM(length(nonce) + length(ciphertext)) FROM audit_blobs`).Scan(&inlineBytes); err != nil || inlineBytes != 0 {
		t.Fatalf("duplicate inline bytes=%d err=%v", inlineBytes, err)
	}
	if err := store.db.QueryRow(`SELECT SUM(length(ciphertext)) FROM audit_payloads`).Scan(&payloadBytes); err != nil || payloadBytes != len(body)+16 {
		t.Fatalf("stored ciphertext bytes=%d want=%d err=%v", payloadBytes, len(body)+16, err)
	}
	assertAuditPlaintexts(t, store, key, id, map[storage.AuditDirection]string{upstream.Direction: body, client.Direction: body})

	// A changed client response splits the payload without altering upstream.
	changed := sealedPayload(t, key, id, client.Direction, "restored client output")
	if err := store.InsertAuditBlob(ctx, changed); err != nil {
		t.Fatal(err)
	}
	assertPayloadCount(t, store, 2)
	assertAuditPlaintexts(t, store, key, id, map[storage.AuditDirection]string{upstream.Direction: body, client.Direction: "restored client output"})
	if err := store.InsertAuditBlob(ctx, client); err != nil {
		t.Fatal(err)
	}
	assertPayloadCount(t, store, 1)
	// Retry reset drops only the upstream reference.
	if err := store.DeleteUpstreamAuditBlobs(ctx, id); err != nil {
		t.Fatal(err)
	}
	assertPayloadCount(t, store, 1)
	assertAuditPlaintexts(t, store, key, id, map[storage.AuditDirection]string{client.Direction: body})
	if err := store.DeleteRequestRecord(ctx, id); err != nil {
		t.Fatal(err)
	}
	assertPayloadCount(t, store, 0)
}

func TestSharedAuditPayloadPreservesMetadataAndRetention(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"))
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_retained")
	key := auditPayloadFixture(t, store, id)
	upstream := sealedPayload(t, key, id, storage.AuditDirectionUpstreamResponse, "same bytes")
	upstream.CreatedAt = time.Now().Add(-48 * time.Hour).UTC()
	upstream.Truncated = true
	client := sealedPayload(t, key, id, storage.AuditDirectionResponse, "same bytes")
	client.MediaType = "text/plain"
	for _, blob := range []storage.AuditBlob{upstream, client} {
		if err := store.InsertAuditBlob(ctx, blob); err != nil {
			t.Fatal(err)
		}
	}
	assertPayloadCount(t, store, 1)
	blobs, err := store.GetAuditBlobsByRequest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range blobs {
		if blob.Direction == upstream.Direction && (!blob.Truncated || blob.MediaType != upstream.MediaType || !blob.CreatedAt.Equal(upstream.CreatedAt)) {
			t.Fatalf("lost upstream metadata: %+v", blob)
		}
		if blob.Direction == client.Direction && (blob.Truncated || blob.MediaType != client.MediaType) {
			t.Fatalf("lost client metadata: %+v", blob)
		}
	}
	if n, err := store.DeleteAuditBlobsOlderThan(ctx, time.Now().Add(-24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("deleted=%d err=%v", n, err)
	}
	assertPayloadCount(t, store, 1)
	assertAuditPlaintexts(t, store, key, id, map[storage.AuditDirection]string{client.Direction: "same bytes"})
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Scope: contract.PurgeScopeAll, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	assertPayloadCount(t, store, 0)
}

func TestAuditPayloadMigrationCompactsLegacyWithoutChangingContent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	store := openTestStore(t, path)
	const id = contract.RequestID("request_legacy")
	key := auditPayloadFixture(t, store, id)
	// Inline rows, as captured before shared payloads existed.
	for _, direction := range []storage.AuditDirection{storage.AuditDirectionResponse, storage.AuditDirectionUpstreamResponse} {
		if err := upsertAuditBlob(ctx, store.db, sealedPayload(t, key, id, direction, "legacy response"), nil); err != nil {
			t.Fatal(err)
		}
	}
	want := map[storage.AuditDirection]string{storage.AuditDirectionResponse: "legacy response", storage.AuditDirectionUpstreamResponse: "legacy response"}
	assertAuditPlaintexts(t, store, key, id, want)
	if _, err := store.SweepExpiredAuditData(ctx); err != nil {
		t.Fatal(err)
	}
	assertPayloadCount(t, store, 1)
	assertAuditPlaintexts(t, store, key, id, want)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openTestStore(t, path)
	defer store.Close()
	assertPayloadCount(t, store, 1)
	assertAuditPlaintexts(t, store, key, id, want)
}

func TestLegacyAuditCompactionCannotOverwriteOrResurrectChangedCapture(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"))
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_compaction_race")
	key := auditPayloadFixture(t, store, id)
	old := sealedPayload(t, key, id, storage.AuditDirectionResponse, "old")
	if err := upsertAuditBlob(ctx, store.db, old, nil); err != nil {
		t.Fatal(err)
	}
	updated := sealedPayload(t, key, id, old.Direction, "new")
	if err := store.InsertAuditBlob(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if err := store.writeSharedAuditBlob(ctx, old, auditContentKey(key, old), true); err != nil {
		t.Fatal(err)
	}
	assertAuditPlaintexts(t, store, key, id, map[storage.AuditDirection]string{old.Direction: "new"})
	assertPayloadCount(t, store, 1)
	if err := store.DeleteRequestRecord(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := store.writeSharedAuditBlob(ctx, old, auditContentKey(key, old), true); err != nil {
		t.Fatal(err)
	}
	assertPayloadCount(t, store, 0)
}

func TestConcurrentAuditWritesShareOnlyWithinTheirRequest(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"))
	defer store.Close()
	ctx := context.Background()
	var blobs []storage.AuditBlob
	for _, id := range []contract.RequestID{"request_parallel_a", "request_parallel_b"} {
		key := auditPayloadFixture(t, store, id)
		for _, direction := range []storage.AuditDirection{storage.AuditDirectionRequest, storage.AuditDirectionUpstreamRequest, storage.AuditDirectionResponse, storage.AuditDirectionUpstreamResponse} {
			blobs = append(blobs, sealedPayload(t, key, id, direction, "identical bytes"))
		}
	}
	var wg sync.WaitGroup
	for _, blob := range blobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.InsertAuditBlob(ctx, blob); err != nil {
				t.Errorf("concurrent capture: %v", err)
			}
		}()
	}
	wg.Wait()
	assertPayloadCount(t, store, 2)
}
