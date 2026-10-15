package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func (store *Store) GetModelRoutingBinding(ctx context.Context, principal string, sessionID contract.SessionID, protocol contract.ProtocolID, virtualModelID string) (contract.ModelRoutingBinding, error) {
	var binding contract.ModelRoutingBinding
	var createdAt, updatedAt, expiresAt string
	err := store.db.QueryRowContext(ctx, `SELECT principal, session_id, protocol, virtual_model_id, group_id, member_id, service_id, model, config_snapshot_version, created_at, updated_at, expires_at FROM model_routing_bindings WHERE principal = ? AND session_id = ? AND protocol = ? AND virtual_model_id = ? AND expires_at > ?`, principal, sessionID, protocol, virtualModelID, store.now().UTC().Format(time.RFC3339Nano)).Scan(
		&binding.Principal, &binding.SessionID, &binding.Protocol, &binding.VirtualModelID, &binding.GroupID, &binding.MemberID, &binding.ServiceID, &binding.Model, &binding.ConfigSnapshotVersion, &createdAt, &updatedAt, &expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.ModelRoutingBinding{}, fmt.Errorf("%w: model routing binding", storagecontract.ErrNotFound)
	}
	if err != nil {
		return contract.ModelRoutingBinding{}, err
	}
	binding.CreatedAt, binding.UpdatedAt, binding.ExpiresAt = createdAt, updatedAt, expiresAt
	if err := binding.Validate(); err != nil {
		return contract.ModelRoutingBinding{}, fmt.Errorf("%w: model routing binding: %v", storagecontract.ErrInvalidRecord, err)
	}
	return binding, nil
}

func (store *Store) UpsertModelRoutingBinding(ctx context.Context, binding contract.ModelRoutingBinding) error {
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO model_routing_bindings (principal, session_id, protocol, virtual_model_id, group_id, member_id, service_id, model, config_snapshot_version, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(principal, session_id, protocol, virtual_model_id) DO UPDATE SET group_id = excluded.group_id, member_id = excluded.member_id, service_id = excluded.service_id, model = excluded.model, config_snapshot_version = excluded.config_snapshot_version, updated_at = excluded.updated_at, expires_at = excluded.expires_at`, binding.Principal, binding.SessionID, binding.Protocol, binding.VirtualModelID, binding.GroupID, binding.MemberID, binding.ServiceID, binding.Model, binding.ConfigSnapshotVersion, binding.CreatedAt, binding.UpdatedAt, binding.ExpiresAt)
	return err
}

func (store *Store) DeleteModelRoutingBinding(ctx context.Context, principal string, sessionID contract.SessionID, protocol contract.ProtocolID, virtualModelID string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM model_routing_bindings WHERE principal = ? AND session_id = ? AND protocol = ? AND virtual_model_id = ?`, principal, sessionID, protocol, virtualModelID)
	return err
}

func (store *Store) UpdateRoutingSettingsAtomic(ctx context.Context, settings contract.RoutingSettings) (err error) {
	if settings.ModelRoutingGroups == nil {
		settings.ModelRoutingGroups = []contract.ModelRoutingGroup{}
	}
	if settings.ModelRedirects == nil {
		settings.ModelRedirects = []contract.ModelRedirect{}
	}
	if err := settings.ValidateWithDimensions(store.routingSettingsDimensions(ctx)); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackOnError(tx, &err)
	if err = validateModelRoutingReferencesTx(ctx, tx, settings.ModelRoutingGroups); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE routing_settings SET document_json = ? WHERE id = 1`, string(document)); err != nil {
		return err
	}
	return tx.Commit()
}

func validateModelRoutingReferencesTx(ctx context.Context, tx *sql.Tx, groups []contract.ModelRoutingGroup) error {
	for _, group := range groups {
		for _, member := range group.Members {
			var document string
			if err := tx.QueryRowContext(ctx, `SELECT document_json FROM services WHERE id = ?`, member.ServiceID).Scan(&document); err != nil {
				return fmt.Errorf("member %q service %q is unavailable", member.ID, member.ServiceID)
			}
			var service contract.Service
			if err := json.Unmarshal([]byte(document), &service); err != nil {
				return fmt.Errorf("service %q is invalid", member.ServiceID)
			}
			if !service.Enabled {
				return fmt.Errorf("member %q service %q is disabled", member.ID, member.ServiceID)
			}
			found := false
			for _, model := range service.Models {
				if model == member.Model {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("member %q model %q is not configured on service %q", member.ID, member.Model, member.ServiceID)
			}
		}
	}
	return nil
}

func validateStoredRoutingReferencesTx(ctx context.Context, tx *sql.Tx) error {
	var document string
	if err := tx.QueryRowContext(ctx, `SELECT document_json FROM routing_settings WHERE id = 1`).Scan(&document); err != nil {
		return err
	}
	settings := contract.DefaultRoutingSettings()
	if err := json.Unmarshal([]byte(document), &settings); err != nil {
		return err
	}
	// The dimensions live in their own singleton; read them inside the same
	// transaction so a group naming a custom dimension still validates here.
	// A missing or unreadable document leaves dimensions nil, which is the
	// strict reading: the custom-dimension rule is then refused rather than
	// admitted unvalidated.
	var classifierDocument string
	var dimensions []contract.ClassifierDimension
	if err := tx.QueryRowContext(ctx, `SELECT document_json FROM classifier_settings WHERE id = 1`).Scan(&classifierDocument); err == nil {
		var classifier contract.ClassifierSettings
		if json.Unmarshal([]byte(classifierDocument), &classifier) == nil {
			dimensions = classifier.Dimensions
		}
	}
	if err := settings.ValidateWithDimensions(dimensions); err != nil {
		return err
	}
	if settings.ModelRoutingGroups == nil {
		settings.ModelRoutingGroups = []contract.ModelRoutingGroup{}
	}
	return validateModelRoutingReferencesTx(ctx, tx, settings.ModelRoutingGroups)
}
