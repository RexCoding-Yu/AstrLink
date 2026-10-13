package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// classifierRefPrefix routes local://classifier/<id> secrets to the sealed
// classifier_credentials table. V1 has exactly one slot: default.
const classifierRefPrefix = "local://classifier/"

// GetClassifierSettings returns the singleton document, or defaults when no
// row exists yet. A stored document that no longer validates loads as
// disabled: an unreadable classifier must never take routing down.
func (store *Store) GetClassifierSettings(ctx context.Context) (contract.ClassifierSettings, error) {
	settings := contract.ClassifierSettings{ClassifierSource: contract.ClassifierSourceInline}
	var document string
	err := store.db.QueryRowContext(ctx, `SELECT document_json FROM classifier_settings WHERE id = 1`).Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("read classifier settings: %w", err)
	}
	if err := json.Unmarshal([]byte(document), &settings); err != nil {
		return contract.ClassifierSettings{ClassifierSource: contract.ClassifierSourceInline}, fmt.Errorf("%w: classifier settings", storagecontract.ErrInvalidRecord)
	}
	return settings, nil
}

func (store *Store) UpdateClassifierSettings(ctx context.Context, settings contract.ClassifierSettings) (err error) {
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode classifier settings: %w", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin classifier settings write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if _, err = transaction.ExecContext(ctx, `INSERT INTO classifier_settings (id, document_json, updated_at) VALUES (1, ?, ?)
ON CONFLICT(id) DO UPDATE SET document_json = excluded.document_json, updated_at = excluded.updated_at`,
		string(document), store.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("write classifier settings: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit classifier settings write: %w", err)
	}
	return nil
}

// classifierCredential serves the local://classifier/default secret. The AAD
// binds the table name and the singleton key, so a ciphertext copied onto any
// other secret row does not open.
func (store *Store) classifierCredential(ctx context.Context, ref secretstore.Ref, operation string, secret []byte) ([]byte, error) {
	key := strings.TrimPrefix(string(ref), classifierRefPrefix)
	if key != "default" {
		return nil, fmt.Errorf("%w: %s", storagecontract.ErrUnsupportedRef, ref)
	}
	switch operation {
	case "put":
		if err := validateCredential(secret); err != nil {
			return nil, err
		}
		sealed, err := store.keys.sealColumn(classifierCredentialsTable, key, secret)
		if err != nil {
			return nil, fmt.Errorf("seal classifier credential: %w", err)
		}
		_, err = store.db.ExecContext(ctx, `INSERT INTO classifier_credentials (id, credential_value, sealed) VALUES (1, ?, 1)
ON CONFLICT(id) DO UPDATE SET credential_value = excluded.credential_value, sealed = 1`, sealed)
		return nil, err
	case "delete":
		_, err := store.db.ExecContext(ctx, `DELETE FROM classifier_credentials WHERE id = 1`)
		return nil, err
	default:
		var value []byte
		var sealed bool
		err := store.db.QueryRowContext(ctx, `SELECT credential_value, sealed FROM classifier_credentials WHERE id = 1`).Scan(&value, &sealed)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", secretstore.ErrNotFound, ref)
		}
		if err != nil {
			return nil, err
		}
		return store.openSecret(classifierCredentialsTable, key, value, sealed)
	}
}

// ClassifierCredentialConfigured reports whether the sealed credential row
// exists. It never reads or returns the secret itself.
func (store *Store) ClassifierCredentialConfigured(ctx context.Context) (bool, error) {
	var exists bool
	if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM classifier_credentials WHERE id = 1)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("read classifier credential status: %w", err)
	}
	return exists, nil
}
