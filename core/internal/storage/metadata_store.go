package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Targets a metadata value is kept for.
const (
	MetadataTargetToken   = "token"
	MetadataTargetSession = "session"
)

// MetadataValueRecord is a value kept for an access token (its default) or a
// session (its binding). Values from request headers stay in request
// records.
type MetadataValueRecord struct {
	TargetKind string
	TargetID   string
	Key        string
	Value      string
	// SetBy is token_default, model_marker, user_marker or manual.
	SetBy string
	// Turn is the session turn a binding was set in, 0 for none.
	Turn      int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type MetadataStore interface {
	ListMetadataAttributes(context.Context) ([]contract.MetadataAttribute, error)
	// CreateMetadataAttribute returns ErrConflict when the key or the marker
	// is taken.
	CreateMetadataAttribute(context.Context, contract.MetadataAttribute) error
	// UpdateMetadataAttribute changes everything but the key and the owner;
	// ErrNotFound for a missing key, ErrConflict for a marker taken.
	UpdateMetadataAttribute(context.Context, contract.MetadataAttribute) error
	// DeleteMetadataAttribute removes the attribute and every value kept for
	// it; ErrNotFound for a missing key.
	DeleteMetadataAttribute(ctx context.Context, key string) error
	// RegisterPluginAttributes adds or updates the attributes plugin
	// registers. A key or a marker the user or another plugin owns is
	// ErrConflict, and then nothing changes.
	RegisterPluginAttributes(ctx context.Context, plugin string, attributes []contract.MetadataAttribute) error

	// MetadataValues returns the values kept for one target, by key.
	MetadataValues(ctx context.Context, targetKind, targetID string) (map[string]MetadataValueRecord, error)
	// ListMetadataValues returns every value kept for one kind of target.
	ListMetadataValues(ctx context.Context, targetKind string) ([]MetadataValueRecord, error)
	PutMetadataValue(context.Context, MetadataValueRecord) error
	DeleteMetadataValue(ctx context.Context, targetKind, targetID, key string) error
	// ReplaceTokenMetadata makes values the whole of a token's defaults.
	ReplaceTokenMetadata(ctx context.Context, tokenID contract.AccessTokenID, values map[string]string) error
}
