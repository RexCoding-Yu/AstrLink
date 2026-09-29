package sqlite

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func (store *Store) GetAuditSettings(ctx context.Context) (contract.AuditSettings, error) {
	row := store.db.QueryRowContext(ctx, `SELECT
    request_body_enabled, response_content_enabled, http_meta_enabled,
    request_body_max_bytes, response_content_max_bytes,
    metadata_retention_days, content_retention_days, extensions_json
FROM audit_settings WHERE id = 1`)
	var (
		requestEnabled, responseEnabled, httpMetaEnabled   int
		requestMax, responseMax, metadataDays, contentDays int
		extensionsJSON                                     sql.NullString
	)
	if err := row.Scan(
		&requestEnabled, &responseEnabled, &httpMetaEnabled, &requestMax, &responseMax,
		&metadataDays, &contentDays, &extensionsJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contract.DefaultAuditSettings(), nil
		}
		return contract.AuditSettings{}, fmt.Errorf("get audit settings: %w", err)
	}
	settings := contract.AuditSettings{
		RequestBodyEnabled:      requestEnabled != 0,
		ResponseContentEnabled:  responseEnabled != 0,
		HTTPMetaEnabled:         httpMetaEnabled != 0,
		RequestBodyMaxBytes:     requestMax,
		ResponseContentMaxBytes: responseMax,
		MetadataRetentionDays:   metadataDays,
		ContentRetentionDays:    contentDays,
	}
	if extensionsJSON.Valid && extensionsJSON.String != "" {
		if err := json.Unmarshal([]byte(extensionsJSON.String), &settings.Extensions); err != nil {
			return contract.AuditSettings{}, fmt.Errorf("%w: audit settings extensions", storagecontract.ErrInvalidRecord)
		}
	}
	if err := settings.Validate(); err != nil {
		return contract.AuditSettings{}, fmt.Errorf("%w: %v", storagecontract.ErrInvalidRecord, err)
	}
	return settings, nil
}

func (store *Store) UpdateAuditSettings(ctx context.Context, settings contract.AuditSettings) error {
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	var extensions any
	if settings.Extensions != nil {
		encoded, err := json.Marshal(settings.Extensions)
		if err != nil {
			return fmt.Errorf("encode audit settings extensions: %w", err)
		}
		extensions = string(encoded)
	}
	result, err := store.db.ExecContext(ctx, `UPDATE audit_settings SET
    request_body_enabled = ?,
    response_content_enabled = ?,
    http_meta_enabled = ?,
    request_body_max_bytes = ?,
    response_content_max_bytes = ?,
    metadata_retention_days = ?,
    content_retention_days = ?,
    extensions_json = ?,
    updated_at = ?
WHERE id = 1`,
		boolToInt(settings.RequestBodyEnabled),
		boolToInt(settings.ResponseContentEnabled),
		boolToInt(settings.HTTPMetaEnabled),
		settings.RequestBodyMaxBytes,
		settings.ResponseContentMaxBytes,
		settings.MetadataRetentionDays,
		settings.ContentRetentionDays,
		extensions,
		store.now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("update audit settings: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read audit settings update result: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: audit settings", storagecontract.ErrNotFound)
	}
	return nil
}

func (store *Store) GetOrCreateAuditKey(ctx context.Context) ([]byte, error) {
	key, err := store.GetAuditKey(ctx)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, storagecontract.ErrNotFound) {
		return nil, err
	}
	key = make([]byte, storagecontract.AuditKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate audit key: %w", err)
	}
	_, err = store.db.ExecContext(ctx, `INSERT OR IGNORE INTO audit_keys (id, key_bytes, created_at) VALUES (1, ?, ?)`,
		key, store.now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("insert audit key: %w", err)
	}
	return store.GetAuditKey(ctx)
}

func (store *Store) GetAuditKey(ctx context.Context) ([]byte, error) {
	var key []byte
	err := store.db.QueryRowContext(ctx, `SELECT key_bytes FROM audit_keys WHERE id = 1`).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: audit key", storagecontract.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get audit key: %w", err)
	}
	if len(key) != storagecontract.AuditKeyBytes {
		return nil, fmt.Errorf("%w: audit key length", storagecontract.ErrInvalidRecord)
	}
	return append([]byte(nil), key...), nil
}

func (store *Store) InsertAuditBlob(ctx context.Context, blob storagecontract.AuditBlob) error {
	if err := blob.RequestID.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	if !blob.Direction.Valid() {
		return fmt.Errorf("%w: audit direction", storagecontract.ErrInvalidArgument)
	}
	if blob.MediaType == "" || len(blob.Nonce) != storagecontract.AuditNonceBytes ||
		len(blob.Ciphertext) == 0 || blob.CapturedBytes < 0 {
		return fmt.Errorf("%w: audit blob fields", storagecontract.ErrInvalidArgument)
	}
	createdAt := blob.CreatedAt
	if createdAt.IsZero() {
		createdAt = store.now().UTC()
	}
	blob.CreatedAt = createdAt
	key, err := store.GetAuditKey(ctx)
	if err != nil && !errors.Is(err, storagecontract.ErrNotFound) {
		return err
	}
	defer clear(key)
	contentKey := auditContentKey(key, blob)
	if contentKey == nil {
		// Preserve historical behavior for opaque/corrupt captures. They must
		// remain available for the reader to report the decryption failure.
		return upsertAuditBlob(ctx, store.db, blob, nil)
	}
	return store.writeSharedAuditBlob(ctx, blob, contentKey, false)
}

func auditContentKey(key []byte, blob storagecontract.AuditBlob) []byte {
	plain, err := storagecontract.OpenAuditBlob(key, blob.Nonce, blob.Ciphertext)
	if err != nil {
		return nil
	}
	defer clear(plain)
	// Keyed and request-scoped: the index cannot be used as a public hash of
	// private content or to correlate equal bodies across conversations.
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte(blob.RequestID))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(plain)
	return digest.Sum(nil)
}

func (store *Store) writeSharedAuditBlob(ctx context.Context, blob storagecontract.AuditBlob, contentKey []byte, legacy bool) (err error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin shared audit write: %w", err)
	}
	defer transaction.Rollback()
	if legacy {
		// Acquire the writer lock and compare the entire old row before
		// converting it. A concurrent retry, deletion, or capture must win.
		result, updateErr := transaction.ExecContext(ctx, `UPDATE audit_blobs SET payload_id = payload_id
WHERE request_id = ? AND direction = ? AND payload_id IS NULL
  AND nonce = ? AND ciphertext = ? AND media_type = ? AND truncated = ?
  AND captured_bytes = ? AND created_at = ?`, blob.RequestID, blob.Direction,
			blob.Nonce, blob.Ciphertext, blob.MediaType, boolToInt(blob.Truncated), blob.CapturedBytes,
			blob.CreatedAt.UTC().Format(time.RFC3339Nano))
		if updateErr != nil {
			return fmt.Errorf("lock legacy audit blob: %w", updateErr)
		}
		changed, updateErr := result.RowsAffected()
		if updateErr != nil {
			return updateErr
		}
		if changed == 0 {
			return nil
		}
	}
	// Write first, before reading, so concurrent captures acquire SQLite's
	// writer lock without a deferred read transaction upgrade.
	if _, err = transaction.ExecContext(ctx, `INSERT INTO audit_payloads (request_id, content_key, nonce, ciphertext)
VALUES (?, ?, ?, ?) ON CONFLICT(request_id, content_key) DO NOTHING`, blob.RequestID, contentKey, blob.Nonce, blob.Ciphertext); err != nil {
		return fmt.Errorf("insert shared audit payload: %w", err)
	}
	var payloadID int64
	if err = transaction.QueryRowContext(ctx, `SELECT id FROM audit_payloads WHERE request_id = ? AND content_key = ?`, blob.RequestID, contentKey).Scan(&payloadID); err != nil {
		return fmt.Errorf("read shared audit payload: %w", err)
	}
	blob.Nonce = []byte{}
	blob.Ciphertext = []byte{}
	if err = upsertAuditBlob(ctx, transaction, blob, payloadID); err != nil {
		return err
	}
	return transaction.Commit()
}

func upsertAuditBlob(ctx context.Context, executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, blob storagecontract.AuditBlob, payloadID any) error {
	_, err := executor.ExecContext(ctx, `INSERT INTO audit_blobs (
    request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at, payload_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(request_id, direction) DO UPDATE SET
    media_type = excluded.media_type,
    nonce = excluded.nonce,
    ciphertext = excluded.ciphertext,
    truncated = excluded.truncated,
    captured_bytes = excluded.captured_bytes,
    created_at = excluded.created_at,
    payload_id = excluded.payload_id`,
		string(blob.RequestID), string(blob.Direction), blob.MediaType,
		blob.Nonce, blob.Ciphertext, boolToInt(blob.Truncated), blob.CapturedBytes,
		blob.CreatedAt.UTC().Format(time.RFC3339Nano), payloadID,
	)
	if err != nil {
		return fmt.Errorf("upsert audit blob: %w", err)
	}
	return nil
}

func (store *Store) GetAuditBlobsByRequest(
	ctx context.Context,
	id contract.RequestID,
) ([]storagecontract.AuditBlob, error) {
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT
    b.request_id, b.direction, b.media_type,
    COALESCE(p.nonce, b.nonce), COALESCE(p.ciphertext, b.ciphertext),
    b.truncated, b.captured_bytes, b.created_at
FROM audit_blobs b LEFT JOIN audit_payloads p ON p.id = b.payload_id
WHERE b.request_id = ? ORDER BY b.direction ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("list audit blobs: %w", err)
	}
	defer rows.Close()
	blobs := make([]storagecontract.AuditBlob, 0, 2)
	for rows.Next() {
		blob, err := scanAuditBlob(rows)
		if err != nil {
			return nil, err
		}
		blobs = append(blobs, blob)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit blobs: %w", err)
	}
	return blobs, nil
}

func (store *Store) DeleteAuditBlobsByRequest(ctx context.Context, id contract.RequestID) (int, error) {
	if err := id.Validate(); err != nil {
		return 0, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	result, err := store.db.ExecContext(ctx, `DELETE FROM audit_blobs WHERE request_id = ?`, id)
	if err != nil {
		return 0, fmt.Errorf("delete audit blobs: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read audit blob delete result: %w", err)
	}
	return int(deleted), nil
}

func (store *Store) DeleteAuditBlobsOlderThan(ctx context.Context, before time.Time) (int, error) {
	result, err := store.db.ExecContext(
		ctx,
		`DELETE FROM audit_blobs WHERE created_at < ?`,
		before.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("delete old audit blobs: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read old audit blob delete result: %w", err)
	}
	return int(deleted), nil
}

func (store *Store) SweepExpiredAuditData(ctx context.Context) (storagecontract.SweepResult, error) {
	settings, err := store.GetAuditSettings(ctx)
	if err != nil {
		return storagecontract.SweepResult{}, err
	}
	now := store.now().UTC()
	metadataCutoff := now.AddDate(0, 0, -settings.MetadataRetentionDays)
	contentCutoff := now.AddDate(0, 0, -settings.ContentRetentionDays)

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return storagecontract.SweepResult{}, fmt.Errorf("begin audit sweep: %w", err)
	}
	defer rollbackOnError(transaction, &err)

	var blobCountBefore int
	if err = transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_blobs
WHERE request_id IN (SELECT id FROM request_records WHERE started_at < ?)`,
		metadataCutoff.Format(time.RFC3339Nano),
	).Scan(&blobCountBefore); err != nil {
		return storagecontract.SweepResult{}, fmt.Errorf("count metadata-cascade blobs: %w", err)
	}
	result, err := transaction.ExecContext(ctx,
		`DELETE FROM request_records WHERE started_at < ?`,
		metadataCutoff.Format(time.RFC3339Nano),
	)
	if err != nil {
		return storagecontract.SweepResult{}, fmt.Errorf("sweep request records: %w", err)
	}
	deletedRecords, err := result.RowsAffected()
	if err != nil {
		return storagecontract.SweepResult{}, err
	}

	contentResult, err := transaction.ExecContext(ctx,
		`DELETE FROM audit_blobs WHERE created_at < ?`,
		contentCutoff.Format(time.RFC3339Nano),
	)
	if err != nil {
		return storagecontract.SweepResult{}, fmt.Errorf("sweep audit blobs by age: %w", err)
	}
	deletedByAge, err := contentResult.RowsAffected()
	if err != nil {
		return storagecontract.SweepResult{}, err
	}

	orphanResult, err := transaction.ExecContext(ctx, `DELETE FROM audit_blobs
WHERE request_id NOT IN (SELECT id FROM request_records)`)
	if err != nil {
		return storagecontract.SweepResult{}, fmt.Errorf("sweep orphan audit blobs: %w", err)
	}
	deletedOrphans, err := orphanResult.RowsAffected()
	if err != nil {
		return storagecontract.SweepResult{}, err
	}

	if err = transaction.Commit(); err != nil {
		return storagecontract.SweepResult{}, fmt.Errorf("commit audit sweep: %w", err)
	}
	if err := store.compactLegacyAuditBlobs(ctx); err != nil {
		return storagecontract.SweepResult{}, err
	}
	return storagecontract.SweepResult{
		DeletedRecords:    int(deletedRecords),
		DeletedAuditBlobs: blobCountBefore + int(deletedByAge) + int(deletedOrphans),
	}, nil
}

// Upgrade old inline ciphertext gradually, without a full-database rewrite or
// VACUUM on startup. Freed pages are reusable by subsequent captures.
func (store *Store) compactLegacyAuditBlobs(ctx context.Context) error {
	key, err := store.GetAuditKey(ctx)
	if errors.Is(err, storagecontract.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	defer clear(key)
	var cursor int64
	var processedBytes int
	for count := 0; count < 128 && processedBytes < 16*1024*1024; count++ {
		// Read one capture at a time, including for maximum-size legacy blobs.
		var rowID int64
		err := store.db.QueryRowContext(ctx, `SELECT rowid FROM audit_blobs
WHERE payload_id IS NULL AND rowid > ? ORDER BY rowid LIMIT 1`, cursor).Scan(&rowID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find legacy audit blob: %w", err)
		}
		cursor = rowID
		blob, err := scanAuditBlob(store.db.QueryRowContext(ctx, `SELECT
request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at
FROM audit_blobs WHERE rowid = ? AND payload_id IS NULL`, rowID))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		processedBytes += len(blob.Ciphertext)
		contentKey := auditContentKey(key, blob)
		if contentKey == nil {
			continue
		}
		if err := store.writeSharedAuditBlob(ctx, blob, contentKey, true); err != nil {
			return err
		}
	}
	return nil
}

func scanAuditBlob(row scannable) (storagecontract.AuditBlob, error) {
	var (
		requestID, direction, mediaType, createdAt string
		nonce, ciphertext                          []byte
		truncated, capturedBytes                   int
	)
	if err := row.Scan(
		&requestID, &direction, &mediaType, &nonce, &ciphertext,
		&truncated, &capturedBytes, &createdAt,
	); err != nil {
		return storagecontract.AuditBlob{}, err
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return storagecontract.AuditBlob{}, fmt.Errorf("%w: audit blob created_at", storagecontract.ErrInvalidRecord)
	}
	blob := storagecontract.AuditBlob{
		RequestID:     contract.RequestID(requestID),
		Direction:     storagecontract.AuditDirection(direction),
		MediaType:     mediaType,
		Nonce:         append([]byte(nil), nonce...),
		Ciphertext:    append([]byte(nil), ciphertext...),
		Truncated:     truncated != 0,
		CapturedBytes: capturedBytes,
		CreatedAt:     created.UTC(),
	}
	if err := blob.RequestID.Validate(); err != nil || !blob.Direction.Valid() {
		return storagecontract.AuditBlob{}, fmt.Errorf("%w: audit blob identity", storagecontract.ErrInvalidRecord)
	}
	return blob, nil
}

var (
	_ storagecontract.AuditSettingsStore  = (*Store)(nil)
	_ storagecontract.AuditKeyStore       = (*Store)(nil)
	_ storagecontract.AuditBlobStore      = (*Store)(nil)
	_ storagecontract.AuditRetentionStore = (*Store)(nil)
)

// DeleteUpstreamAuditBlobs resets the root's attempt-local audit after its old
// upstream content has been saved on a failed child. Client-side audit remains.
func (store *Store) DeleteUpstreamAuditBlobs(ctx context.Context, id contract.RequestID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	_, err := store.db.ExecContext(ctx, `DELETE FROM audit_blobs WHERE request_id = ? AND direction IN ('upstream_request', 'upstream_response', 'upstream_http_meta')`, id)
	return err
}
