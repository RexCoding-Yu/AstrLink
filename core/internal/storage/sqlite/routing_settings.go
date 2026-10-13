package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func (store *Store) GetRoutingSettings(ctx context.Context) (contract.RoutingSettings, error) {
	var document string
	settings := contract.DefaultRoutingSettings()
	if err := store.db.QueryRowContext(ctx, `SELECT document_json FROM routing_settings WHERE id = 1`).Scan(&document); err != nil {
		return settings, err
	}
	if err := json.Unmarshal([]byte(document), &settings); err != nil {
		return settings, fmt.Errorf("%w: routing settings", storage.ErrInvalidRecord)
	}
	// Documents saved before redirects existed, or with an explicit null,
	// load as an empty table so readers never see a nil list.
	if settings.ModelRedirects == nil {
		settings.ModelRedirects = []contract.ModelRedirect{}
	}
	if settings.ModelRoutingGroups == nil {
		settings.ModelRoutingGroups = []contract.ModelRoutingGroup{}
	}
	if err := settings.Validate(); err != nil {
		return settings, fmt.Errorf("%w: routing settings: %v", storage.ErrInvalidRecord, err)
	}
	return settings, nil
}

func (store *Store) UpdateRoutingSettings(ctx context.Context, settings contract.RoutingSettings) error {
	return store.UpdateRoutingSettingsAtomic(ctx, settings)
}
