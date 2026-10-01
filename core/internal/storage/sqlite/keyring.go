package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/envelope"
)

// keyRing holds the local key and the data keys it unwraps for the life of
// the store (plan §5.3).
type keyRing struct {
	mu    sync.RWMutex
	local []byte
	ring  envelope.KeyRing
	// auditOrphaned records an audit key this local key cannot open, so reads
	// of bodies sealed under it report a missing key rather than corruption.
	auditOrphaned bool
	// secretsOrphaned is the same for dek_secrets and sealed credentials.
	secretsOrphaned bool
}

func (keys *keyRing) clear() {
	if keys == nil {
		return
	}
	keys.mu.Lock()
	defer keys.mu.Unlock()
	clear(keys.local)
	keys.local = nil
	keys.ring.Clear()
}

func (keys *keyRing) audit() []byte {
	if keys == nil {
		return nil
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if len(keys.ring.Audit) != envelope.KeyBytes {
		return nil
	}
	return append([]byte(nil), keys.ring.Audit...)
}

// sealColumn seals one secret column value under dek_secrets.
func (keys *keyRing) sealColumn(table, primaryKey string, plaintext []byte) ([]byte, error) {
	if keys == nil {
		return nil, fmt.Errorf("%w: secrets key", storagecontract.ErrInvalidArgument)
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	return keys.ring.SealColumn(table, primaryKey, plaintext)
}

// openColumn opens a sealed column value. A value that does not open —
// sealed under a set-aside key, copied between rows, or damaged — reports
// the credential store as unavailable, like a missing keystore (§5.7).
func (keys *keyRing) openColumn(table, primaryKey string, sealed []byte) ([]byte, error) {
	if keys == nil {
		return nil, fmt.Errorf("%w: secrets key", secretstore.ErrUnavailable)
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	plaintext, err := keys.ring.OpenColumn(table, primaryKey, sealed)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %s does not decrypt on this device", secretstore.ErrUnavailable, table, primaryKey)
	}
	return plaintext, nil
}

type keyRingResult struct {
	// orphaned lists kinds whose envelope did not open under this local key.
	orphaned []string
	// legacyAuditTable says the database still has the audit_keys table a
	// test build created, so its file has not been scrubbed yet.
	legacyAuditTable bool
}

// keyRingMode says what ensureKeyRing may do with envelopes it cannot use.
type keyRingMode int

const (
	// keyRingRepair sets aside envelopes the local key no longer opens and
	// creates missing ones.
	keyRingRepair keyRingMode = iota
	// keyRingStrict fails with ErrLocalKeyMismatch instead of setting aside.
	keyRingStrict
	// keyRingReadOnly is strict and also fails with ErrNeedsCoreStart
	// wherever it would write.
	keyRingReadOnly
)

// ensureKeyRing opens or creates the secrets and audit envelopes in one
// transaction. The first start after a test build adopts its plaintext
// audit_keys value as dek_audit, so no body is re-encrypted, and removes it
// only in the transaction that writes its envelope. An envelope that no longer
// opens — the key file or keychain entry is gone — is renamed and kept for
// recovery, and a fresh key takes its place (§5.7). In the strict modes such
// an envelope fails with ErrLocalKeyMismatch and nothing is written.
func ensureKeyRing(ctx context.Context, database *sql.DB, localKey []byte, now time.Time, mode keyRingMode) (keys *keyRing, result keyRingResult, err error) {
	if len(localKey) != envelope.KeyBytes {
		return nil, result, fmt.Errorf("%w: local key length", storagecontract.ErrInvalidArgument)
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, result, fmt.Errorf("begin key ring: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	keys = &keyRing{local: append([]byte(nil), localKey...)}
	defer func() {
		if err != nil {
			keys.clear()
			keys = nil
		}
	}()
	stamp := now.UTC().Format(time.RFC3339Nano)

	legacy, legacyTable, err := readLegacyAuditKey(ctx, transaction)
	if err != nil {
		return nil, result, err
	}
	defer clear(legacy)
	result.legacyAuditTable = legacyTable
	if legacy != nil && mode == keyRingReadOnly {
		return nil, result, fmt.Errorf("%w: a plaintext audit key is waiting to be wrapped", ErrNeedsCoreStart)
	}
	for _, kind := range []string{envelope.KindSecrets, envelope.KindAudit} {
		dek, orphaned, err := openEnvelope(ctx, transaction, localKey, kind, stamp, mode)
		if err != nil {
			return nil, result, err
		}
		if orphaned {
			result.orphaned = append(result.orphaned, kind)
		}
		switch {
		case dek == nil:
			if kind == envelope.KindAudit && legacy != nil {
				dek = append([]byte(nil), legacy...)
			} else if dek, err = envelope.NewKey(); err != nil {
				return nil, result, err
			}
			if err := insertEnvelope(ctx, transaction, localKey, dek, kind, kind, stamp); err != nil {
				clear(dek)
				return nil, result, err
			}
		case kind == envelope.KindAudit && legacy != nil && !bytes.Equal(dek, legacy):
			// A release that ignored audit_keys already created its own
			// dek_audit and sealed bodies under it. Keep the test build's key
			// wrapped for recovery; it cannot join the ring without
			// re-encrypting bodies.
			if err := insertEnvelope(ctx, transaction, localKey, legacy, kind, kind+".legacy."+stamp, stamp); err != nil {
				clear(dek)
				return nil, result, err
			}
			result.orphaned = append(result.orphaned, kind)
		}
		if kind == envelope.KindSecrets {
			keys.ring.Secrets = dek
		} else {
			keys.ring.Audit = dek
		}
	}
	if legacy != nil {
		if _, err := transaction.ExecContext(ctx, `DELETE FROM audit_keys`); err != nil {
			return nil, result, fmt.Errorf("clear legacy audit key: %w", err)
		}
	}
	if err := transaction.QueryRowContext(ctx, `SELECT
    EXISTS(SELECT 1 FROM key_envelopes WHERE kind LIKE 'audit.%'),
    EXISTS(SELECT 1 FROM key_envelopes WHERE kind LIKE 'secrets.%')`).Scan(&keys.auditOrphaned, &keys.secretsOrphaned); err != nil {
		return nil, result, fmt.Errorf("read orphaned envelopes: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return nil, result, fmt.Errorf("commit key ring: %w", err)
	}
	return keys, result, nil
}

// readLegacyAuditKey returns the plaintext audit key a test build kept in
// audit_keys, and whether that table is still there. Databases created by a
// release never have it.
func readLegacyAuditKey(ctx context.Context, transaction *sql.Tx) (key []byte, table bool, err error) {
	if err := transaction.QueryRowContext(ctx, `SELECT EXISTS(
    SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'audit_keys')`).Scan(&table); err != nil {
		return nil, false, fmt.Errorf("read legacy audit key table: %w", err)
	}
	if !table {
		return nil, false, nil
	}
	err = transaction.QueryRowContext(ctx, `SELECT key_bytes FROM audit_keys WHERE id = 1`).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, true, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("read legacy audit key: %w", err)
	}
	if len(key) != envelope.KeyBytes {
		clear(key)
		return nil, true, fmt.Errorf("%w: audit key length", storagecontract.ErrInvalidRecord)
	}
	return key, true, nil
}

// openEnvelope returns the unwrapped key for kind, or nil when there is none.
// A row that no longer opens is renamed to <kind>.orphaned.<stamp>. If a row
// set aside earlier opens again — the original key file or keychain entry is
// back — it takes the kind back, so recovery needs no extra step. Any other
// failure aborts so a database error never discards a key.
func openEnvelope(ctx context.Context, transaction *sql.Tx, localKey []byte, kind, stamp string, mode keyRingMode) (dek []byte, orphaned bool, err error) {
	var nonce, wrapped []byte
	err = transaction.QueryRowContext(ctx, `SELECT nonce, wrapped FROM key_envelopes WHERE kind = ?`, kind).Scan(&nonce, &wrapped)
	present := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("read %s envelope: %w", kind, err)
	}
	if present {
		dek, err = envelope.Unwrap(localKey, nonce, wrapped, kind)
		if err == nil {
			return dek, false, nil
		}
		if !errors.Is(err, envelope.ErrUnwrap) {
			return nil, false, err
		}
		if mode != keyRingRepair {
			return nil, false, fmt.Errorf("%w: %s", ErrLocalKeyMismatch, kind)
		}
	}
	if mode == keyRingReadOnly {
		return nil, false, fmt.Errorf("%w: the %s data key has not been created", ErrNeedsCoreStart, kind)
	}
	restored, dek, err := restoreSetAsideEnvelope(ctx, transaction, localKey, kind)
	if err != nil {
		return nil, false, err
	}
	if present {
		if _, err := transaction.ExecContext(ctx, `UPDATE key_envelopes SET kind = ? WHERE kind = ?`,
			kind+".orphaned."+stamp, kind); err != nil {
			clear(dek)
			return nil, false, fmt.Errorf("keep orphaned %s envelope: %w", kind, err)
		}
	}
	if restored != "" {
		if _, err := transaction.ExecContext(ctx, `UPDATE key_envelopes SET kind = ? WHERE kind = ?`, kind, restored); err != nil {
			clear(dek)
			return nil, false, fmt.Errorf("restore %s envelope: %w", kind, err)
		}
	}
	return dek, present, nil
}

// restoreSetAsideEnvelope finds the newest set-aside envelope of kind that
// opens under this local key.
func restoreSetAsideEnvelope(ctx context.Context, transaction *sql.Tx, localKey []byte, kind string) (string, []byte, error) {
	rows, err := transaction.QueryContext(ctx, `SELECT kind, nonce, wrapped FROM key_envelopes
WHERE substr(kind, 1, ?) = ? ORDER BY created_at DESC, kind DESC`, len(kind)+len(".orphaned."), kind+".orphaned.")
	if err != nil {
		return "", nil, fmt.Errorf("read set-aside %s envelopes: %w", kind, err)
	}
	defer rows.Close()
	for rows.Next() {
		var rowKind string
		var nonce, wrapped []byte
		if err := rows.Scan(&rowKind, &nonce, &wrapped); err != nil {
			return "", nil, fmt.Errorf("read set-aside %s envelope: %w", kind, err)
		}
		if dek, err := envelope.Unwrap(localKey, nonce, wrapped, kind); err == nil {
			return rowKind, dek, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, fmt.Errorf("read set-aside %s envelopes: %w", kind, err)
	}
	return "", nil, nil
}

func insertEnvelope(ctx context.Context, transaction *sql.Tx, localKey, dek []byte, kind, rowKind, stamp string) error {
	nonce, wrapped, err := envelope.Wrap(localKey, dek, kind)
	if err != nil {
		return fmt.Errorf("wrap %s key: %w", kind, err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO key_envelopes (kind, nonce, wrapped, created_at) VALUES (?, ?, ?, ?)`,
		rowKind, nonce, wrapped, stamp); err != nil {
		return fmt.Errorf("write %s envelope: %w", kind, err)
	}
	return nil
}

// HasOrphanedAuditKey reports whether bodies may be sealed under an audit key
// this device can no longer open, so a failed decryption means the key is
// missing, not that the body is corrupt.
func (store *Store) HasOrphanedAuditKey() bool {
	if store == nil || store.keys == nil {
		return false
	}
	store.keys.mu.RLock()
	defer store.keys.mu.RUnlock()
	return store.keys.auditOrphaned
}
