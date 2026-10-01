package sqlite

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func (store *Store) GetAuditSettings(ctx context.Context) (contract.AuditSettings, error) {
	row := store.db.QueryRowContext(ctx, `SELECT
    request_body_enabled, response_content_enabled, http_meta_enabled,
    request_body_max_bytes, response_content_max_bytes,
    metadata_retention_days, content_retention_days, extensions_json, agent_raw_access_enabled
FROM audit_settings WHERE id = 1`)
	var (
		requestEnabled, responseEnabled, httpMetaEnabled   int
		requestMax, responseMax, metadataDays, contentDays int
		agentRawAccess                                     int
		extensionsJSON                                     sql.NullString
	)
	if err := row.Scan(
		&requestEnabled, &responseEnabled, &httpMetaEnabled, &requestMax, &responseMax,
		&metadataDays, &contentDays, &extensionsJSON, &agentRawAccess,
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
		AgentRawAccessEnabled:   agentRawAccess != 0,
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
    agent_raw_access_enabled = ?,
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
		boolToInt(settings.AgentRawAccessEnabled),
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

// GetOrCreateAuditKey returns dek_audit. The key ring creates it when the
// store opens, so this never generates one.
func (store *Store) GetOrCreateAuditKey(ctx context.Context) ([]byte, error) {
	return store.GetAuditKey(ctx)
}

// GetAuditKey returns a copy of dek_audit from the key ring.
func (store *Store) GetAuditKey(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := store.keys.audit()
	if key == nil {
		return nil, fmt.Errorf("%w: audit key", storagecontract.ErrNotFound)
	}
	return key, nil
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
	if blob.Exposure == "" {
		blob.Exposure = storagecontract.AuditExposureRaw
	}
	if !blob.Exposure.Valid() {
		return fmt.Errorf("%w: audit exposure", storagecontract.ErrInvalidArgument)
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
	if blob.Exposure == storagecontract.AuditExposureRaw {
		return store.writeRawCapture(ctx, key, blob)
	}
	contentKey := auditContentKey(key, blob)
	if contentKey == nil {
		// Preserve historical behavior for opaque/corrupt captures. They must
		// remain available for the reader to report the decryption failure.
		return upsertAuditBlob(ctx, store.db, blob, nil)
	}
	err = store.writeSharedAuditBlob(ctx, blob, contentKey, false)
	if errors.Is(err, errStoredRaw) {
		// Raw is sticky: a recapture of a part already raw is raw too.
		blob.Exposure = storagecontract.AuditExposureRaw
		return store.writeRawCapture(ctx, key, blob)
	}
	return err
}

// errStoredRaw stops a shared write whose part is already stored as raw.
var errStoredRaw = errors.New("audit part is stored as raw")

// writeRawCapture seals a raw part to the raw sealing key once a raw
// password protects it. Before that, or when it will not seal, the part is
// kept only as a withheld marker: raw content never lands under dek_audit
// alone, where anyone holding the local key could read it.
func (store *Store) writeRawCapture(ctx context.Context, auditKey []byte, blob storagecontract.AuditBlob) error {
	keyID, public := store.rawKey.captureKey()
	if keyID != 0 && auditKey != nil {
		sealed, contentKey, err := sealRawPart(auditKey, blob, keyID, public)
		if err == nil {
			defer clear(contentKey)
			return store.writeRawAuditBlob(ctx, sealed, contentKey)
		}
	}
	blob.Nonce, blob.Ciphertext = []byte{}, []byte{}
	return store.withSecureDelete(ctx, false, func(conn *sql.Conn) error {
		return upsertAuditBlob(ctx, conn, blob, nil)
	})
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
	if !legacy {
		// Under the writer lock, so no settle can slip in between: a part
		// already raw would stay raw through the upsert below.
		var stored string
		err = transaction.QueryRowContext(ctx, `SELECT exposure FROM audit_blobs WHERE request_id = ? AND direction = ?`,
			blob.RequestID, string(blob.Direction)).Scan(&stored)
		if err == nil && stored == string(storagecontract.AuditExposureRaw) {
			return errStoredRaw
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read stored audit exposure: %w", err)
		}
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
	exposure := blob.Exposure
	if exposure == "" {
		exposure = storagecontract.AuditExposureRaw
	}
	// Raw is sticky: a later capture of the same part may tighten its
	// exposure but never share what an earlier decision withheld.
	_, err := executor.ExecContext(ctx, `INSERT INTO audit_blobs (
    request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at, payload_id, exposure
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(request_id, direction) DO UPDATE SET
    media_type = excluded.media_type,
    nonce = excluded.nonce,
    ciphertext = excluded.ciphertext,
    truncated = excluded.truncated,
    captured_bytes = excluded.captured_bytes,
    created_at = excluded.created_at,
    payload_id = excluded.payload_id,
    exposure = CASE WHEN audit_blobs.exposure = 'raw' THEN 'raw' ELSE excluded.exposure END`,
		string(blob.RequestID), string(blob.Direction), blob.MediaType,
		blob.Nonce, blob.Ciphertext, boolToInt(blob.Truncated), blob.CapturedBytes,
		blob.CreatedAt.UTC().Format(time.RFC3339Nano), payloadID, string(exposure),
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
	return store.listAuditBlobs(ctx, id, false)
}

func (store *Store) GetShareableAuditBlobsByRequest(
	ctx context.Context,
	id contract.RequestID,
) ([]storagecontract.AuditBlob, error) {
	return store.listAuditBlobs(ctx, id, true)
}

func (store *Store) listAuditBlobs(
	ctx context.Context,
	id contract.RequestID,
	shareableOnly bool,
) ([]storagecontract.AuditBlob, error) {
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	// Withheld ciphertext is not even read into memory for a shareable view.
	rows, err := store.db.QueryContext(ctx, `SELECT
    b.request_id, b.direction, b.media_type,
    CASE WHEN ?2 AND b.exposure <> 'shareable' THEN x'' ELSE COALESCE(p.nonce, b.nonce) END,
    CASE WHEN ?2 AND b.exposure <> 'shareable' THEN x'' ELSE COALESCE(p.ciphertext, b.ciphertext) END,
    b.truncated, b.captured_bytes, b.created_at, b.exposure,
    CASE WHEN b.payload_id IS NULL AND length(b.ciphertext) = 0 THEN 'none' ELSE COALESCE(p.sealing, 'audit') END, p.key_id,
    CASE WHEN ?2 AND b.exposure <> 'shareable' THEN NULL ELSE p.wrapped_key END
FROM audit_blobs b LEFT JOIN audit_payloads p ON p.id = b.payload_id
WHERE b.request_id = ?1 ORDER BY b.direction ASC`, id, shareableOnly)
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

func (store *Store) UpdateAuditExposure(
	ctx context.Context,
	id contract.RequestID,
	direction storagecontract.AuditDirection,
	exposure storagecontract.AuditExposure,
) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	if !direction.Valid() || !exposure.Valid() {
		return fmt.Errorf("%w: audit exposure", storagecontract.ErrInvalidArgument)
	}
	if exposure == storagecontract.AuditExposureRaw && !store.keepsRawCaptures() {
		return store.settleRawWithoutPassword(ctx, id, direction)
	}
	// Only the label changes; the ciphertext and payload reference stay as
	// captured. The WHERE clause mirrors AuditExposure.CanBecome.
	result, err := store.db.ExecContext(ctx, `UPDATE audit_blobs SET exposure = ?1
WHERE request_id = ?2 AND direction = ?3
  AND (exposure = 'pending' OR exposure = ?1 OR ?1 = 'raw')`, string(exposure), id, string(direction))
	if err != nil {
		if exposure == storagecontract.AuditExposureRaw {
			store.deferSettle(ctx, id)
		}
		return fmt.Errorf("update audit exposure: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read audit exposure update result: %w", err)
	}
	if changed > 0 {
		if exposure == storagecontract.AuditExposureRaw {
			// A body that settles to raw moves onto the raw sealing key now.
			// The label is already right; a part a failure here leaves
			// behind, such as when the request's context ends first, goes to
			// the vault's reseal pass instead of waiting for the next start.
			if err := store.resealSettledPart(ctx, id, direction); err != nil {
				store.deferReseal()
			}
		}
		return nil
	}
	var current string
	err = store.db.QueryRowContext(ctx, `SELECT exposure FROM audit_blobs WHERE request_id = ? AND direction = ?`, id, string(direction)).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return storagecontract.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read audit exposure: %w", err)
	}
	return fmt.Errorf("%w: audit exposure %s cannot become %s", storagecontract.ErrPrecondition, current, exposure)
}

// settleRawWithoutPassword settles a part to raw while no raw capture is
// kept. The label and the drop are one statement, so the part is never raw
// with its content still under dek_audit; if it fails, the part stays
// pending and is dropped by the reseal pass its request's end asks for.
func (store *Store) settleRawWithoutPassword(ctx context.Context, id contract.RequestID, direction storagecontract.AuditDirection) error {
	var changed int64
	err := store.withSecureDelete(ctx, false, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `UPDATE audit_blobs SET `+dropRawContent+`
WHERE request_id = ? AND direction = ?`, id, string(direction))
		if err != nil {
			return fmt.Errorf("update audit exposure: %w", err)
		}
		changed, err = result.RowsAffected()
		return err
	})
	if err != nil {
		store.deferSettle(ctx, id)
		return err
	}
	if changed == 0 {
		return storagecontract.ErrNotFound
	}
	return nil
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
	rawKeyLoaded := store.HasRawSealingKey()
	var cursor int64
	var processedBytes int
	for count := 0; count < 128 && processedBytes < 16*1024*1024; count++ {
		// Read one capture at a time, including for maximum-size legacy blobs.
		// With a raw sealing key, withheld parts are left to the reseal pass,
		// which moves them straight onto that key.
		var rowID int64
		err := store.db.QueryRowContext(ctx, `SELECT rowid FROM audit_blobs
WHERE payload_id IS NULL AND length(ciphertext) > 0 AND rowid > ? AND (NOT ? OR exposure = 'shareable')
ORDER BY rowid LIMIT 1`, cursor, rawKeyLoaded).Scan(&rowID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find legacy audit blob: %w", err)
		}
		cursor = rowID
		blob, err := scanAuditBlob(store.db.QueryRowContext(ctx, `SELECT
request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at, exposure,
'audit', NULL, NULL
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
		requestID, direction, mediaType, createdAt, exposure, sealing string
		nonce, ciphertext, wrappedKey                                 []byte
		truncated, capturedBytes                                      int
		rawKeyID                                                      sql.NullInt64
	)
	if err := row.Scan(
		&requestID, &direction, &mediaType, &nonce, &ciphertext,
		&truncated, &capturedBytes, &createdAt, &exposure,
		&sealing, &rawKeyID, &wrappedKey,
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
		Exposure:      storagecontract.AuditExposure(exposure),
		Sealing:       storagecontract.AuditSealing(sealing),
		RawKeyID:      rawKeyID.Int64,
		WrappedKey:    append([]byte(nil), wrappedKey...),
	}
	if len(wrappedKey) == 0 {
		blob.WrappedKey = nil
	}
	if err := blob.RequestID.Validate(); err != nil || !blob.Direction.Valid() || !blob.Exposure.Valid() {
		return storagecontract.AuditBlob{}, fmt.Errorf("%w: audit blob identity", storagecontract.ErrInvalidRecord)
	}
	switch blob.Sealing {
	case storagecontract.AuditSealingAudit, storagecontract.AuditSealingRawV1:
	case storagecontract.AuditSealingNone:
		blob.Nonce, blob.Ciphertext = nil, nil
	default:
		return storagecontract.AuditBlob{}, fmt.Errorf("%w: audit blob sealing", storagecontract.ErrInvalidRecord)
	}
	return blob, nil
}

var (
	_ storagecontract.AuditSettingsStore  = (*Store)(nil)
	_ storagecontract.AuditKeyStore       = (*Store)(nil)
	_ storagecontract.AuditBlobStore      = (*Store)(nil)
	_ storagecontract.AuditExposureStore  = (*Store)(nil)
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
