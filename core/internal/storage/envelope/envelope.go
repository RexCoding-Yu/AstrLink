// Package envelope wraps AstrLink's data keys under the local key and seals
// single secret columns under the secrets key (plan §5.3).
//
//	local key ─AES-256-GCM(AAD=kind)─▶ dek_secrets, dek_audit
//	dek_secrets ─AES-256-GCM(AAD=table‖primary key)─▶ secret columns
//
// dek_audit keys audit bodies, content keys and session fingerprints. A test
// build's plaintext audit_keys value becomes dek_audit, so its bodies keep
// working without re-encryption.
package envelope

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	// KindSecrets wraps the key for credentials and local token secrets.
	KindSecrets = "secrets"
	// KindAudit wraps the key for captured bodies.
	KindAudit = "audit"
	// KeyBytes is the size of the local key and of every data key.
	KeyBytes = storage.AuditKeyBytes
	// NonceBytes is the GCM nonce size of envelopes and sealed columns.
	NonceBytes = storage.AuditNonceBytes

	columnVersion byte = 1
	// columnOverhead is the version byte, nonce and GCM tag.
	columnOverhead = 1 + NonceBytes + 16
)

var (
	// ErrUnwrap means an envelope does not open under this local key: the key
	// changed, the row was altered, or it belongs to another kind.
	ErrUnwrap = errors.New("key envelope cannot be opened")
	// ErrColumn means a sealed column does not open under the secrets key.
	ErrColumn = errors.New("sealed column cannot be opened")
)

// NewKey returns a fresh random 32-byte key.
func NewKey() ([]byte, error) {
	key := make([]byte, KeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return key, nil
}

// Wrap seals a data key under the local key, bound to its kind.
func Wrap(localKey, dek []byte, kind string) (nonce, wrapped []byte, err error) {
	if len(localKey) != KeyBytes || len(dek) != KeyBytes || kind == "" {
		return nil, nil, fmt.Errorf("%w: key envelope fields", storage.ErrInvalidArgument)
	}
	return storage.SealAES256GCM(localKey, dek, []byte(kind))
}

// Unwrap opens an envelope written by Wrap for the same kind.
func Unwrap(localKey, nonce, wrapped []byte, kind string) ([]byte, error) {
	if len(localKey) != KeyBytes || kind == "" {
		return nil, fmt.Errorf("%w: key envelope fields", ErrUnwrap)
	}
	dek, err := storage.OpenAES256GCM(localKey, nonce, wrapped, []byte(kind))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnwrap, kind)
	}
	if len(dek) != KeyBytes {
		clear(dek)
		return nil, fmt.Errorf("%w: %s length", ErrUnwrap, kind)
	}
	return dek, nil
}

// KeyRing holds the unwrapped data keys. Callers must not keep the slices
// after Clear.
type KeyRing struct {
	Secrets []byte
	Audit   []byte
}

// Clear zeroes both keys.
func (ring *KeyRing) Clear() {
	if ring == nil {
		return
	}
	clear(ring.Secrets)
	clear(ring.Audit)
	ring.Secrets, ring.Audit = nil, nil
}

// SealColumn encrypts one secret column value as version ‖ nonce ‖ ciphertext.
// The table and primary key are authenticated, so a value copied to another
// row or table does not open.
func (ring *KeyRing) SealColumn(table, primaryKey string, plaintext []byte) ([]byte, error) {
	if ring == nil || len(ring.Secrets) != KeyBytes {
		return nil, fmt.Errorf("%w: secrets key", storage.ErrInvalidArgument)
	}
	nonce, ciphertext, err := storage.SealAES256GCM(ring.Secrets, plaintext, columnAAD(table, primaryKey))
	if err != nil {
		return nil, err
	}
	sealed := make([]byte, 0, 1+len(nonce)+len(ciphertext))
	sealed = append(sealed, columnVersion)
	sealed = append(sealed, nonce...)
	return append(sealed, ciphertext...), nil
}

// OpenColumn reverses SealColumn for the same table and primary key.
func (ring *KeyRing) OpenColumn(table, primaryKey string, sealed []byte) ([]byte, error) {
	if ring == nil || len(ring.Secrets) != KeyBytes {
		return nil, fmt.Errorf("%w: secrets key", ErrColumn)
	}
	if !IsSealedColumn(sealed) {
		return nil, fmt.Errorf("%w: format", ErrColumn)
	}
	plaintext, err := storage.OpenAES256GCM(
		ring.Secrets,
		sealed[1:1+NonceBytes],
		sealed[1+NonceBytes:],
		columnAAD(table, primaryKey),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrColumn, table)
	}
	return plaintext, nil
}

// IsSealedColumn reports whether a stored value has the sealed layout. Legacy
// plaintext credentials are UTF-8 text and never start with byte 0x01.
func IsSealedColumn(value []byte) bool {
	return len(value) >= columnOverhead && value[0] == columnVersion
}

func columnAAD(table, primaryKey string) []byte {
	var aad bytes.Buffer
	aad.WriteByte(columnVersion)
	aad.WriteString(table)
	aad.WriteByte(0)
	aad.WriteString(primaryKey)
	return aad.Bytes()
}
