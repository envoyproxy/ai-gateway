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

// pbkdf2AesGcm implements SessionCrypto using PBKDF2 for key derivation and AES-GCM for encryption.
type pbkdf2AesGcm struct {
	seed       string // Seed for key derivation.
	saltSize   int    // Size of the random salt.
	keyLength  int    // Length of the derived key (16, 24, or 32 bytes for AES).
	iterations int    // Number of iterations for PBKDF2.
}

// NewPBKDF2AesGcmSessionCrypto creates a SessionCrypto using PBKDF2 for key derivation and AES-GCM for encryption.
func NewPBKDF2AesGcmSessionCrypto(seed string, iterations int) SessionCrypto {
	return &pbkdf2AesGcm{
		seed:       seed,
		saltSize:   16,
		keyLength:  32,
		iterations: iterations,
	}
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

	ciphertext, err := encryptAESGCM(key, salt, []byte(plaintext))
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(ciphertext), nil
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

	plaintext, err := decryptAESGCM(key, salt, data)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// FallbackEnabledSessionCrypto tries to decrypt using the primary SessionCrypto first for decryption.
// If that fails and a fallback SessionCrypto is provided, it tries to decrypt using the fallback.
type FallbackEnabledSessionCrypto struct {
	Primary, Fallback SessionCrypto
}

// Encrypt always uses the primary SessionCrypto.
func (f FallbackEnabledSessionCrypto) Encrypt(plaintext string) (string, error) {
	return f.Primary.Encrypt(plaintext)
}

// Decrypt tries the primary SessionCrypto first, and if that fails and a fallback is provided, it tries the fallback.
func (f FallbackEnabledSessionCrypto) Decrypt(encrypted string) (string, error) {
	plaintext, err := f.Primary.Decrypt(encrypted)
	if err == nil {
		return plaintext, nil
	}
	if f.Fallback != nil {
		return f.Fallback.Decrypt(encrypted)
	}
	return "", err
}

// encryptAESGCM encrypts the plaintext using AES-GCM with the provided key.
func encryptAESGCM(key, salt, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	combined := slices.Concat(salt, nonce, ciphertext) // Final structure: salt || nonce || ciphertext.
	return combined, nil
}

// decryptAESGCM decrypts the ciphertext using AES-GCM with the provided key and nonce.
func decryptAESGCM(key, salt, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	saltSize := len(salt)
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < saltSize+nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce := ciphertext[saltSize : saltSize+nonceSize]
	ct := ciphertext[saltSize+nonceSize:]

	return gcm.Open(nil, nonce, ct, nil)
}
