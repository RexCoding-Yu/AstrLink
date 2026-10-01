package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	storage "github.com/QuantumNous/astrlink/core/internal/storage"
)

func auditExposures(t *testing.T, store *Store, id contract.RequestID) map[storage.AuditDirection]storage.AuditExposure {
	t.Helper()
	blobs, err := store.GetAuditBlobsByRequest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[storage.AuditDirection]storage.AuditExposure, len(blobs))
	for _, blob := range blobs {
		result[blob.Direction] = blob.Exposure
	}
	return result
}

func TestAuditExposureDefaultsToRawAndRawIsSticky(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"))
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_exposure_sticky")
	key := auditPayloadFixture(t, store, id)

	unlabelled := sealedPayload(t, key, id, storage.AuditDirectionRequest, "body")
	unlabelled.Exposure = ""
	if err := store.InsertAuditBlob(ctx, unlabelled); err != nil {
		t.Fatal(err)
	}
	if got := auditExposures(t, store, id)[storage.AuditDirectionRequest]; got != storage.AuditExposureRaw {
		t.Fatalf("unlabelled exposure=%q, want raw", got)
	}
	shared := sealedPayload(t, key, id, storage.AuditDirectionRequest, "body again")
	shared.Exposure = storage.AuditExposureShareable
	if err := store.InsertAuditBlob(ctx, shared); err != nil {
		t.Fatal(err)
	}
	if got := auditExposures(t, store, id)[storage.AuditDirectionRequest]; got != storage.AuditExposureRaw {
		t.Fatalf("recapture widened raw to %q", got)
	}

	pending := sealedPayload(t, key, id, storage.AuditDirectionUpstreamRequest, "upstream")
	pending.Exposure = storage.AuditExposurePending
	if err := store.InsertAuditBlob(ctx, pending); err != nil {
		t.Fatal(err)
	}
	pending.Exposure = storage.AuditExposureShareable
	if err := store.InsertAuditBlob(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if got := auditExposures(t, store, id)[storage.AuditDirectionUpstreamRequest]; got != storage.AuditExposureShareable {
		t.Fatalf("pending recapture exposure=%q, want shareable", got)
	}

	invalid := sealedPayload(t, key, id, storage.AuditDirectionResponse, "response")
	invalid.Exposure = "public"
	if err := store.InsertAuditBlob(ctx, invalid); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("invalid exposure err=%v", err)
	}
}

func TestUpdateAuditExposureOnlyNarrowsAccess(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"))
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_exposure_update")
	key := auditPayloadFixture(t, store, id)
	for direction, exposure := range map[storage.AuditDirection]storage.AuditExposure{
		storage.AuditDirectionRequest:          storage.AuditExposurePending,
		storage.AuditDirectionResponse:         storage.AuditExposurePending,
		storage.AuditDirectionUpstreamRequest:  storage.AuditExposureShareable,
		storage.AuditDirectionUpstreamResponse: storage.AuditExposureRaw,
	} {
		blob := sealedPayload(t, key, id, direction, string(direction))
		blob.Exposure = exposure
		if err := store.InsertAuditBlob(ctx, blob); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		direction storage.AuditDirection
		next      storage.AuditExposure
		wantErr   error
		want      storage.AuditExposure
	}{
		{storage.AuditDirectionRequest, storage.AuditExposureShareable, nil, storage.AuditExposureShareable},
		{storage.AuditDirectionResponse, storage.AuditExposureRaw, nil, storage.AuditExposureRaw},
		{storage.AuditDirectionUpstreamRequest, storage.AuditExposureRaw, nil, storage.AuditExposureRaw},
		{storage.AuditDirectionUpstreamResponse, storage.AuditExposureShareable, storage.ErrPrecondition, storage.AuditExposureRaw},
		{storage.AuditDirectionUpstreamResponse, storage.AuditExposurePending, storage.ErrPrecondition, storage.AuditExposureRaw},
		{storage.AuditDirectionUpstreamResponse, storage.AuditExposureRaw, nil, storage.AuditExposureRaw},
		{storage.AuditDirectionHTTPMeta, storage.AuditExposureRaw, storage.ErrNotFound, ""},
	}
	for _, testCase := range cases {
		err := store.UpdateAuditExposure(ctx, id, testCase.direction, testCase.next)
		if !errors.Is(err, testCase.wantErr) && !(testCase.wantErr == nil && err == nil) {
			t.Fatalf("%s → %s err=%v want %v", testCase.direction, testCase.next, err, testCase.wantErr)
		}
		if got := auditExposures(t, store, id)[testCase.direction]; got != testCase.want {
			t.Fatalf("%s → %s exposure=%q want %q", testCase.direction, testCase.next, got, testCase.want)
		}
	}
	if err := store.UpdateAuditExposure(ctx, id, storage.AuditDirectionRequest, "public"); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("invalid exposure err=%v", err)
	}
}

func TestRecoverPendingRequestRecordsWithholdsUndecidedBodies(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"))
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_exposure_recover")
	key := auditPayloadFixture(t, store, id)
	pending := sealedPayload(t, key, id, storage.AuditDirectionRequest, "undecided")
	pending.Exposure = storage.AuditExposurePending
	shared := sealedPayload(t, key, id, storage.AuditDirectionUpstreamRequest, "sent")
	shared.Exposure = storage.AuditExposureShareable
	for _, blob := range []storage.AuditBlob{pending, shared} {
		if err := store.InsertAuditBlob(ctx, blob); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.RecoverPendingRequestRecords(ctx); err != nil {
		t.Fatal(err)
	}
	got := auditExposures(t, store, id)
	if got[storage.AuditDirectionRequest] != storage.AuditExposureRaw ||
		got[storage.AuditDirectionUpstreamRequest] != storage.AuditExposureShareable {
		t.Fatalf("recovered exposures=%v", got)
	}
}

func TestRequestRecordPersistsPrivacyDecisionAndFindings(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"))
	defer store.Close()
	ctx := context.Background()
	decision := contract.PrivacyDecisionRedact
	record := contract.RequestRecord{
		ID: "request_privacy_decision", StartedAt: store.now(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		PrivacyDecision: &decision,
		PrivacyFindings: []contract.PrivacyFinding{{Kind: contract.CanonicalKindEmail, JSONPath: "$.input[0].content", Count: 2}},
	}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetRequestRecord(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PrivacyDecision == nil || *loaded.PrivacyDecision != decision {
		t.Fatalf("decision=%v", loaded.PrivacyDecision)
	}
	if len(loaded.PrivacyFindings) != 1 || loaded.PrivacyFindings[0] != record.PrivacyFindings[0] {
		t.Fatalf("findings=%#v", loaded.PrivacyFindings)
	}

	settings, err := store.GetAuditSettings(ctx)
	if err != nil || !settings.AgentRawAccessEnabled {
		t.Fatalf("default agent raw access=%v err=%v", settings.AgentRawAccessEnabled, err)
	}
	settings.AgentRawAccessEnabled = false
	if err := store.UpdateAuditSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if settings, err = store.GetAuditSettings(ctx); err != nil || settings.AgentRawAccessEnabled {
		t.Fatalf("agent raw access=%v err=%v, want false", settings.AgentRawAccessEnabled, err)
	}
}

func TestShareableAuditBlobsOmitWithheldCiphertext(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "audit.db"))
	defer store.Close()
	ctx := context.Background()
	const id = contract.RequestID("request_exposure_shareable_view")
	key := auditPayloadFixture(t, store, id)
	parts := map[storage.AuditDirection]storage.AuditExposure{
		storage.AuditDirectionRequest:         storage.AuditExposureRaw,
		storage.AuditDirectionResponse:        storage.AuditExposurePending,
		storage.AuditDirectionUpstreamRequest: storage.AuditExposureShareable,
	}
	for direction, exposure := range parts {
		blob := sealedPayload(t, key, id, direction, "body-"+string(direction))
		blob.Exposure = exposure
		if err := store.InsertAuditBlob(ctx, blob); err != nil {
			t.Fatal(err)
		}
	}
	blobs, err := store.GetShareableAuditBlobsByRequest(ctx, id)
	if err != nil || len(blobs) != len(parts) {
		t.Fatalf("blobs=%d err=%v", len(blobs), err)
	}
	for _, blob := range blobs {
		if blob.Exposure != parts[blob.Direction] || blob.CapturedBytes == 0 || blob.MediaType == "" {
			t.Fatalf("%s metadata=%#v", blob.Direction, blob)
		}
		if blob.Exposure != storage.AuditExposureShareable {
			if len(blob.Nonce) != 0 || len(blob.Ciphertext) != 0 {
				t.Fatalf("%s withheld ciphertext loaded", blob.Direction)
			}
			continue
		}
		plain, err := storage.OpenAuditBlob(key, blob.Nonce, blob.Ciphertext)
		if err != nil || string(plain) != "body-"+string(blob.Direction) {
			t.Fatalf("%s plaintext=%q err=%v", blob.Direction, plain, err)
		}
	}
	full, err := store.GetAuditBlobsByRequest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, blob := range full {
		// Without a raw password the raw part keeps no content at all.
		if raw := parts[blob.Direction] == storage.AuditExposureRaw; raw != (len(blob.Ciphertext) == 0) {
			t.Fatalf("%s full read ciphertext bytes=%d sealing=%q", blob.Direction, len(blob.Ciphertext), blob.Sealing)
		}
	}
}
