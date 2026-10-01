package envelope

import (
	"bytes"
	"errors"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	local, dek := mustKey(t), mustKey(t)
	nonce, wrapped, err := Wrap(local, dek, KindAudit)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonce) != NonceBytes || bytes.Contains(wrapped, dek) {
		t.Fatalf("envelope layout: nonce %d bytes, contains plaintext %v", len(nonce), bytes.Contains(wrapped, dek))
	}
	opened, err := Unwrap(local, nonce, wrapped, KindAudit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, dek) {
		t.Fatal("unwrapped key differs")
	}
}

func TestUnwrapRejectsWrongKeyKindAndTampering(t *testing.T) {
	local, dek := mustKey(t), mustKey(t)
	nonce, wrapped, err := Wrap(local, dek, KindSecrets)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), wrapped...)
	tampered[0] ^= 1
	cases := map[string]func() ([]byte, error){
		"wrong local key": func() ([]byte, error) { return Unwrap(mustKey(t), nonce, wrapped, KindSecrets) },
		"other kind":      func() ([]byte, error) { return Unwrap(local, nonce, wrapped, KindAudit) },
		"tampered":        func() ([]byte, error) { return Unwrap(local, nonce, tampered, KindSecrets) },
		"short nonce":     func() ([]byte, error) { return Unwrap(local, nonce[:4], wrapped, KindSecrets) },
		"short local key": func() ([]byte, error) { return Unwrap(local[:16], nonce, wrapped, KindSecrets) },
	}
	for name, open := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := open(); !errors.Is(err, ErrUnwrap) {
				t.Fatalf("Unwrap() error = %v, want ErrUnwrap", err)
			}
		})
	}
}

func TestWrapRejectsBadInput(t *testing.T) {
	local := mustKey(t)
	for name, call := range map[string]func() error{
		"short dek":  func() error { _, _, err := Wrap(local, make([]byte, 16), KindAudit); return err },
		"short key":  func() error { _, _, err := Wrap(local[:31], local, KindAudit); return err },
		"empty kind": func() error { _, _, err := Wrap(local, local, ""); return err },
	} {
		if err := call(); !errors.Is(err, storage.ErrInvalidArgument) {
			t.Fatalf("%s: Wrap() error = %v", name, err)
		}
	}
}

func TestSealColumnBindsTableAndPrimaryKey(t *testing.T) {
	ring := &KeyRing{Secrets: mustKey(t), Audit: mustKey(t)}
	secret := []byte("sk-test-value")
	sealed, err := ring.SealColumn("service_credentials", "svc_a", secret)
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealedColumn(sealed) || bytes.Contains(sealed, secret) {
		t.Fatal("sealed column keeps the plaintext or loses its layout")
	}
	opened, err := ring.OpenColumn("service_credentials", "svc_a", sealed)
	if err != nil || !bytes.Equal(opened, secret) {
		t.Fatalf("OpenColumn() = %q, %v", opened, err)
	}
	for name, open := range map[string]func() ([]byte, error){
		"other row":   func() ([]byte, error) { return ring.OpenColumn("service_credentials", "svc_b", sealed) },
		"other table": func() ([]byte, error) { return ring.OpenColumn("service_proxy_credentials", "svc_a", sealed) },
		// The separator keeps "ab" + "c" apart from "a" + "bc".
		"shifted boundary": func() ([]byte, error) { return ring.OpenColumn("service_credentialss", "vc_a", sealed) },
		"other key": func() ([]byte, error) {
			return (&KeyRing{Secrets: mustKey(t)}).OpenColumn("service_credentials", "svc_a", sealed)
		},
		"plaintext": func() ([]byte, error) { return ring.OpenColumn("service_credentials", "svc_a", secret) },
	} {
		if _, err := open(); !errors.Is(err, ErrColumn) {
			t.Fatalf("%s: OpenColumn() error = %v, want ErrColumn", name, err)
		}
	}
}

func TestIsSealedColumnRejectsLegacyText(t *testing.T) {
	for _, value := range [][]byte{nil, []byte("sk-ant-api03-plaintext-credential-value"), make([]byte, columnOverhead-1)} {
		if IsSealedColumn(value) {
			t.Fatalf("IsSealedColumn(%q) = true", value)
		}
	}
}

func TestKeyRingClearZeroesKeys(t *testing.T) {
	secrets, audit := mustKey(t), mustKey(t)
	ring := &KeyRing{Secrets: secrets, Audit: audit}
	ring.Clear()
	if ring.Secrets != nil || ring.Audit != nil || !bytes.Equal(secrets, make([]byte, KeyBytes)) || !bytes.Equal(audit, make([]byte, KeyBytes)) {
		t.Fatal("Clear() kept key material")
	}
	if _, err := ring.SealColumn("t", "k", []byte("x")); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("SealColumn() after Clear error = %v", err)
	}
}
