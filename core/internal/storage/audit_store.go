package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type AuditDirection string

const (
	AuditDirectionRequest  AuditDirection = "request"
	AuditDirectionResponse AuditDirection = "response"
	// AuditDirectionHTTPMeta stores the redacted HTTP envelope (method, URL,
	// headers, response status) as encrypted JSON (ADR 0008).
	AuditDirectionHTTPMeta AuditDirection = "http_meta"
	// Per-attempt upstream capture directions. Each retry has its own
	// request_id, so attempt index is not part of the blob key.
	AuditDirectionUpstreamRequest  AuditDirection = "upstream_request"
	AuditDirectionUpstreamResponse AuditDirection = "upstream_response"
	AuditDirectionUpstreamHTTPMeta AuditDirection = "upstream_http_meta"
)

func (direction AuditDirection) Valid() bool {
	switch direction {
	case AuditDirectionRequest,
		AuditDirectionResponse,
		AuditDirectionHTTPMeta,
		AuditDirectionUpstreamRequest,
		AuditDirectionUpstreamResponse,
		AuditDirectionUpstreamHTTPMeta:
		return true
	default:
		return false
	}
}

// AuditExposure is the capture-time sharing level of one audit part (plan
// §5.11). Readers select by it and never re-inspect content.
type AuditExposure string

const (
	// AuditExposurePending marks a client request body stored before its
	// privacy decision. Readers withhold it exactly like raw.
	AuditExposurePending AuditExposure = "pending"
	// AuditExposureShareable parts may be returned to observers (L1).
	AuditExposureShareable AuditExposure = "shareable"
	// AuditExposureRaw parts need operator access or a per-request grant (L2).
	AuditExposureRaw AuditExposure = "raw"
)

func (exposure AuditExposure) Valid() bool {
	switch exposure {
	case AuditExposurePending, AuditExposureShareable, AuditExposureRaw:
		return true
	default:
		return false
	}
}

// CanBecome reports whether moving from exposure to next never widens who can
// read the part: pending may settle either way, shareable may only tighten.
func (exposure AuditExposure) CanBecome(next AuditExposure) bool {
	switch exposure {
	case AuditExposurePending:
		return next.Valid()
	case AuditExposureShareable:
		return next == AuditExposureShareable || next == AuditExposureRaw
	case AuditExposureRaw:
		return next == AuditExposureRaw
	default:
		return false
	}
}

type AuditBlob struct {
	RequestID     contract.RequestID
	Direction     AuditDirection
	MediaType     string
	Nonce         []byte
	Ciphertext    []byte
	Truncated     bool
	CapturedBytes int
	CreatedAt     time.Time
	// Exposure defaults to raw when empty so an unlabelled write is never
	// shared by accident.
	Exposure AuditExposure
	// Sealing is how the stored ciphertext is keyed. Writers leave it empty
	// and seal under the audit key; the store seals raw parts to the raw
	// sealing key once a raw password protects it, and keeps no content for
	// them before that. Readers get it with RawKeyID and WrappedKey set for
	// raw_v1.
	Sealing    AuditSealing
	RawKeyID   int64
	WrappedKey []byte
}

// AuditSealing names the key a stored part opens with.
type AuditSealing string

const (
	// AuditSealingAudit parts open with dek_audit.
	AuditSealingAudit AuditSealing = "audit"
	// AuditSealingRawV1 parts open with their own part key, which is
	// wrapped to the raw sealing public key (plan §5.11.9).
	AuditSealingRawV1 AuditSealing = "raw_v1"
	// AuditSealingNone parts kept no content: they were raw while no raw
	// password was set. Readers get no nonce or ciphertext for them.
	AuditSealingNone AuditSealing = "none"
)

type AuditSettingsStore interface {
	GetAuditSettings(context.Context) (contract.AuditSettings, error)
	UpdateAuditSettings(context.Context, contract.AuditSettings) error
}

type AuditKeyStore interface {
	// GetOrCreateAuditKey returns the 32-byte AES key, generating it once.
	GetOrCreateAuditKey(context.Context) ([]byte, error)
	// GetAuditKey returns the key when present without creating one.
	GetAuditKey(context.Context) ([]byte, error)
}

type AuditBlobStore interface {
	InsertAuditBlob(context.Context, AuditBlob) error
	GetAuditBlobsByRequest(context.Context, contract.RequestID) ([]AuditBlob, error)
	// GetShareableAuditBlobsByRequest lists every part but loads ciphertext
	// only for shareable ones; withheld parts keep their metadata alone.
	GetShareableAuditBlobsByRequest(context.Context, contract.RequestID) ([]AuditBlob, error)
	DeleteAuditBlobsByRequest(context.Context, contract.RequestID) (int, error)
	DeleteAuditBlobsOlderThan(context.Context, time.Time) (int, error)
}

// AuditExposureStore settles the exposure of an already stored part without
// rewriting its ciphertext. Transitions that would widen access are rejected
// with ErrPrecondition; a missing part reports ErrNotFound.
type AuditExposureStore interface {
	UpdateAuditExposure(context.Context, contract.RequestID, AuditDirection, AuditExposure) error
}

type AuditRetentionStore interface {
	SweepExpiredAuditData(context.Context) (SweepResult, error)
}

type SweepResult struct {
	DeletedRecords    int
	DeletedAuditBlobs int
}
