// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func testPBKDF2AesGcmSessionCrypto(seed, fallback string) SessionCrypto {
	return &fallbackEnabledSessionCrypto{
		primary:  NewPBKDF2AesGcmSessionCrypto(seed, 100),
		fallback: NewPBKDF2AesGcmSessionCrypto(fallback, 100),
	}
}

func TestEncryptSession(t *testing.T) {
	for n, factory := range map[string]func(string, string) SessionCrypto{
		"PBKDF2": testPBKDF2AesGcmSessionCrypto,
	} {
		t.Run(n+" encrypt", func(t *testing.T) {
			sc := factory("test", "fallback")

			enc, err := sc.Encrypt("plaintext")
			require.NoError(t, err)

			dec, err := sc.Decrypt(enc)
			require.NoError(t, err)
			require.Equal(t, "plaintext", dec)
		})

		t.Run(n+" encryption is salted", func(t *testing.T) {
			sc := factory("test", "fallback")

			enc1, err := sc.Encrypt("plaintext")
			require.NoError(t, err)
			enc2, err := sc.Encrypt("plaintext")
			require.NoError(t, err)
			require.NotEqual(t, enc1, enc2)
		})
	}
}

func TestDecryptSession(t *testing.T) {
	for n, factory := range map[string]func(string, string) SessionCrypto{
		"PBKDF2": testPBKDF2AesGcmSessionCrypto,
	} {
		t.Run(n+" decrypt", func(t *testing.T) {
			sc := factory("test", "fallback")

			enc, err := sc.Encrypt("plaintext")
			require.NoError(t, err)

			dec, err := sc.Decrypt(enc)
			require.NoError(t, err)
			require.Equal(t, "plaintext", dec)
		})

		t.Run(n+" wrong seed", func(t *testing.T) {
			sc1 := factory("test1", "fallback")
			sc2 := factory("test2", "fallback")

			enc, err := sc1.Encrypt("plaintext")
			require.NoError(t, err)

			dec, err := sc2.Decrypt(enc)
			require.Error(t, err)
			require.Empty(t, dec)
		})

		t.Run(n+" fallback seed", func(t *testing.T) {
			sc1 := factory("test1", "fallback")
			sc2 := factory("test2", "test1")

			// Decrypting should work with the fallback seed.
			enc, err := sc1.Encrypt("plaintext")
			require.NoError(t, err)
			dec, err := sc2.Decrypt(enc)
			require.NoError(t, err)
			require.Equal(t, "plaintext", dec)

			// Encrypting should happen with the latest seed.
			enc2, err := sc2.Encrypt("plaintext2")
			require.NoError(t, err)
			require.NotEqual(t, enc, enc2)

			dec2, err := sc1.Decrypt(enc2)
			require.Error(t, err)
			require.Empty(t, dec2)

			dec2, err = sc2.Decrypt(enc2)
			require.NoError(t, err)
			require.Equal(t, "plaintext2", dec2)
		})

		t.Run(n+" different instances same seed", func(t *testing.T) {
			sc1 := factory("test", "")
			sc2 := factory("test", "")

			enc, err := sc1.Encrypt("plaintext")
			require.NoError(t, err)

			dec, err := sc2.Decrypt(enc)
			require.NoError(t, err)
			require.Equal(t, "plaintext", dec)
		})
	}
}

func BenchmarkPBKDF2AesGcmSessionCrypto(b *testing.B) {
	for _, iterations := range []int{100, 1_000, 10_000, 50_000, 100_000, 200_000} {
		sc := NewPBKDF2AesGcmSessionCrypto("benchmark-seed", iterations)
		b.Run(fmt.Sprintf("encrypt_%d", iterations), func(b *testing.B) {
			EncryptSessionCryptoBenchmark(b, sc)
		})
		b.Run(fmt.Sprintf("decrypt_%d", iterations), func(b *testing.B) {
			DecryptSessionCryptoBenchmark(b, sc)
		})
	}
}

func EncryptSessionCryptoBenchmark(b *testing.B, sc SessionCrypto) {
	plaintext := "benchmarking plaintext data for encryption"
	for b.Loop() {
		_, err := sc.Encrypt(plaintext)
		if err != nil {
			b.Fatalf("encryption failed: %v", err)
		}
	}
}

func DecryptSessionCryptoBenchmark(b *testing.B, sc SessionCrypto) {
	plaintext := "benchmarking plaintext data for encryption"
	encrypted, err := sc.Encrypt(plaintext)
	if err != nil {
		b.Fatalf("encryption failed: %v", err)
	}
	b.ResetTimer() // reset timer to exclude encryption time
	for b.Loop() {
		_, err := sc.Decrypt(encrypted)
		if err != nil {
			b.Fatalf("decryption failed: %v", err)
		}
	}
}
