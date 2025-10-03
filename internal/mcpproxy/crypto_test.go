// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionEncryption(t *testing.T) {
	sc := DefaultSessionCrypto("test")

	enc, err := sc.Encrypt("plaintext")
	require.NoError(t, err)

	dec, err := sc.Decrypt(enc)
	require.NoError(t, err)
	require.Equal(t, "plaintext", dec)
}

func TestEncryptionIsSalted(t *testing.T) {
	sc := DefaultSessionCrypto("test")

	enc1, err := sc.Encrypt("plaintext")
	require.NoError(t, err)
	enc2, err := sc.Encrypt("plaintext")
	require.NoError(t, err)

	require.NotEqual(t, enc1, enc2)
}

func TestDecryptWrongSeed(t *testing.T) {
	sc1 := DefaultSessionCrypto("test1")
	sc2 := DefaultSessionCrypto("test2")

	enc, err := sc1.Encrypt("plaintext")
	require.NoError(t, err)

	dec, err := sc2.Decrypt(enc)
	require.Error(t, err)
	require.Empty(t, dec)
}

func TestDecryptDifferentInstancesSameSeed(t *testing.T) {
	sc1 := DefaultSessionCrypto("test")
	sc2 := DefaultSessionCrypto("test")

	enc, err := sc1.Encrypt("plaintext")
	require.NoError(t, err)

	dec, err := sc2.Decrypt(enc)
	require.NoError(t, err)
	require.Equal(t, "plaintext", dec)
}
