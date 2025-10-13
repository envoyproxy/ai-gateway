// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"slices"
)

// SessionCrypto provides methods to encrypt and decrypt session data.
type SessionCrypto interface {
	// Encrypt encrypts the given plaintext string and returns ciphertext.
	Encrypt(plaintext string) (string, error)
	// Decrypt decrypts the given ciphertext string and returns plaintext bytes.
	Decrypt(encrypted string) (string, error)
}

// DefaultSessionCrypto returns a SessionCrypto implementation using PBKDF2 for key derivation and AES-GCM for encryption.
func DefaultSessionCrypto(seed, fallbackSeed string) SessionCrypto {
	primary := &pbkdf2AesGcm{
		seed:       seed,    // Seed used to derive the encryption key.
		saltSize:   16,      // Salt size for PBKDF2.
		keyLength:  32,      // Key length for AES-256.
		iterations: 100_000, // Number of PBKDF2 iterations (trade security vs performance).
	}
	if fallbackSeed == "" {
		return primary
	}
	return &fallbackEnabledSessionCrypto{
		primary: primary,
		fallback: &pbkdf2AesGcm{
			seed:       fallbackSeed,
			saltSize:   16,
			keyLength:  32,
			iterations: 100_000,
		},
	}
}

// pbkdf2AesGcm implements SessionCrypto using PBKDF2 for key derivation and AES-GCM for encryption.
type pbkdf2AesGcm struct {
	seed       string // Seed for key derivation.
	saltSize   int    // Size of the random salt.
	keyLength  int    // Length of the derived key (16, 24, or 32 bytes for AES).
	iterations int    // Number of iterations for PBKDF2.
}

// deriveKey derives a key from the seed and salt using PBKDF2.
func (p pbkdf2AesGcm) deriveKey(salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, p.seed, salt, p.iterations, p.keyLength)
}

// Encrypt the plaintext using AES-GCM with a key derived from the seed and a random salt.
func (p pbkdf2AesGcm) Encrypt(plaintext string) (string, error) {
	// Generate random salt.
	salt := make([]byte, p.saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}

	key, err := p.deriveKey(salt)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// Random nonce.
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	combined := slices.Concat(salt, nonce, ciphertext) // Final structure: salt || nonce || ciphertext.
	return base64.StdEncoding.EncodeToString(combined), nil
}

// Decrypt the base64-encoded encrypted string using AES-GCM with a key derived from the seed and the extracted salt.
func (p pbkdf2AesGcm) Decrypt(encrypted string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	if len(data) < p.saltSize {
		return "", fmt.Errorf("data too short")
	}

	salt := data[:p.saltSize]
	key, err := p.deriveKey(salt)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	ns := gcm.NonceSize()
	if len(data) < p.saltSize+ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce := data[p.saltSize : p.saltSize+ns]
	ct := data[p.saltSize+ns:]

	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// fallbackEnabledSessionCrypto tries to decrypt using the primary SessionCrypto first for decryption.
// If that fails and a fallback SessionCrypto is provided, it tries to decrypt using the fallback.
type fallbackEnabledSessionCrypto struct {
	primary, fallback SessionCrypto
}

// Encrypt always uses the primary SessionCrypto.
func (f fallbackEnabledSessionCrypto) Encrypt(plaintext string) (string, error) {
	return f.primary.Encrypt(plaintext)
}

// Decrypt tries the primary SessionCrypto first, and if that fails and a fallback is provided, it tries the fallback.
func (f fallbackEnabledSessionCrypto) Decrypt(encrypted string) (string, error) {
	plaintext, err := f.primary.Decrypt(encrypted)
	if err == nil {
		return plaintext, nil
	}
	if f.fallback != nil {
		return f.fallback.Decrypt(encrypted)
	}
	return "", err
}
