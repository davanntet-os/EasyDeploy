// Package secret encrypts sensitive values (registry passwords) at rest
// using AES-256-GCM. The 32-byte key is derived from the operator-supplied
// EASYDEPLOY_SECRET_KEY via SHA-256, so any passphrase length is accepted.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// Box seals and opens secrets with a fixed key.
type Box struct {
	gcm cipher.AEAD
}

// New derives an AES-256-GCM box from the master key.
func New(masterKey string) (*Box, error) {
	if masterKey == "" {
		return nil, fmt.Errorf("secret: empty master key")
	}
	sum := sha256.Sum256([]byte(masterKey))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{gcm: gcm}, nil
}

// Encrypt seals plaintext and returns a base64 string (nonce prepended).
// Empty input returns an empty string so blank passwords stay blank.
func (b *Box) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := b.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Empty input returns an empty string.
func (b *Box) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	ns := b.gcm.NonceSize()
	if len(raw) < ns {
		return "", fmt.Errorf("secret: ciphertext too short")
	}
	nonce, body := raw[:ns], raw[ns:]
	plain, err := b.gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("secret: decrypt failed: %w", err)
	}
	return string(plain), nil
}
