package controlapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// OfflineAuditStore is the storage an offline audit read needs.
type OfflineAuditStore interface {
	storage.RequestRecordStore
	storage.AuditSettingsStore
	storage.AuditKeyStore
	storage.AuditBlobStore
}

// ErrAuditUndecryptable reports captured content this device cannot open.
var ErrAuditUndecryptable = errors.New("audit content cannot be decrypted")

// provenRawVault serves one offline read with a key the caller has proved.
type provenRawVault struct{ opener RawKeyOpener }

func (vault provenRawVault) Status(context.Context) (RawVaultStatus, error) {
	return RawVaultStatus{Configured: vault.opener != nil, PasswordSet: vault.opener != nil}, nil
}

func (vault provenRawVault) UnlockedOpener() (RawKeyOpener, bool) {
	return vault.opener, vault.opener != nil
}

func (provenRawVault) WithProof(context.Context, RawProof, func(RawKeyOpener) error) error {
	return ErrRawProofRequired
}

func (provenRawVault) HoldKey(context.Context, RawProof) (RawKeyHolder, error) {
	return nil, ErrRawProofRequired
}

// ReadFullAudit returns the full audit view of one request as an unlocked
// operator sees it. opener is the raw key the caller proved with the raw
// password, or nil when none is set; no raw part was kept then. It serves
// `astrlink-core audit show`, so the private key only lives in that
// short-lived process.
func ReadFullAudit(ctx context.Context, store OfflineAuditStore, opener RawKeyOpener, id contract.RequestID) (contract.AuditContent, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, RequestsPath, nil)
	if err != nil {
		return contract.AuditContent{}, err
	}
	handler := &Handler{
		requestRecords: store, auditSettings: store, auditKeys: store, auditBlobs: store,
		rawVault: provenRawVault{opener: opener},
	}
	record, err := store.GetRequestRecord(ctx, id)
	if err != nil {
		return contract.AuditContent{}, err
	}
	blobs, err := store.GetAuditBlobsByRequest(ctx, id)
	if err != nil {
		return contract.AuditContent{}, err
	}
	reader := &auditReader{handler: handler, request: request, view: contract.AuditContentViewFull, record: record}
	defer reader.close()
	reader.sealing, _ = handler.rawVaultStatus(ctx)
	content, err := reader.content(id, blobs)
	switch {
	case err == nil:
		return content, nil
	case reader.keyErr != nil && errors.Is(err, reader.keyErr) && !errors.Is(err, errAuditKeyMissing):
		return contract.AuditContent{}, err
	case errors.Is(err, errAuditKeyMissing):
		return contract.AuditContent{}, fmt.Errorf("%w: the audit key is missing on this device", ErrAuditUndecryptable)
	default:
		return contract.AuditContent{}, fmt.Errorf("%w: %v", ErrAuditUndecryptable, err)
	}
}
