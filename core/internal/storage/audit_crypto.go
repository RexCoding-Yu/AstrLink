package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

const AuditKeyBytes = 32
const AuditNonceBytes = 12

// SealAuditBlob encrypts plaintext with AES-256-GCM using a fresh 96-bit nonce.
func SealAuditBlob(key, plaintext []byte) (nonce, ciphertext []byte, err error) {
	return SealAES256GCM(key, plaintext, nil)
}

// OpenAuditBlob decrypts an AES-256-GCM audit blob.
func OpenAuditBlob(key, nonce, ciphertext []byte) ([]byte, error) {
	return OpenAES256GCM(key, nonce, ciphertext, nil)
}

// SealAES256GCM encrypts plaintext under a 32-byte key with a fresh 96-bit
// nonce. The additional data is authenticated but not stored.
func SealAES256GCM(key, plaintext, additionalData []byte) (nonce, ciphertext []byte, err error) {
	if len(key) != AuditKeyBytes {
		return nil, nil, fmt.Errorf("%w: audit key length", ErrInvalidArgument)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, AuditNonceBytes)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, additionalData)
	return nonce, ciphertext, nil
}

// OpenAES256GCM reverses SealAES256GCM. Every failure wraps ErrAuditDecrypt,
// including a wrong key, nonce or additional data.
func OpenAES256GCM(key, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	if len(key) != AuditKeyBytes {
		return nil, fmt.Errorf("%w: audit key length", ErrAuditDecrypt)
	}
	if len(nonce) != AuditNonceBytes {
		return nil, fmt.Errorf("%w: audit nonce length", ErrAuditDecrypt)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuditDecrypt, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuditDecrypt, err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuditDecrypt, err)
	}
	return plaintext, nil
}
