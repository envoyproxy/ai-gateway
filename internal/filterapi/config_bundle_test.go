// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package filterapi

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestReassembleBundleConfig(t *testing.T) {
	cfgRaw := []byte("version: dev\nbackends:\n- name: openai\n")
	index := &ConfigBundleIndex{
		Checksum: ConfigBundleChecksum(cfgRaw),
		Parts: []ConfigBundlePart{
			{Name: "p0", Path: "parts/000"},
			{Name: "p1", Path: "parts/001"},
		},
	}
	parts := map[string][]byte{
		"parts/000": cfgRaw[:10],
		"parts/001": cfgRaw[10:],
	}

	cfg, err := ReassembleBundleConfig(index, func(part ConfigBundlePart) ([]byte, error) {
		return parts[part.Path], nil
	})
	require.NoError(t, err)
	require.Equal(t, "dev", cfg.Version)
	require.Len(t, cfg.Backends, 1)
}

func TestReassembleBundleConfig_ChecksumMismatch(t *testing.T) {
	index := &ConfigBundleIndex{
		Checksum: ConfigBundleChecksum([]byte("different")),
		Parts: []ConfigBundlePart{
			{Name: "p0", Path: "parts/000"},
		},
	}
	_, err := ReassembleBundleConfig(index, func(_ ConfigBundlePart) ([]byte, error) {
		return []byte("version: dev\n"), nil
	})
	require.ErrorContains(t, err, "bundle checksum mismatch")
	require.ErrorIs(t, err, ErrBundleChecksumMismatch)
}

func TestUnmarshalConfigBundleIndex(t *testing.T) {
	raw, err := yaml.Marshal(&ConfigBundleIndex{
		Checksum: "abc",
		Parts:    []ConfigBundlePart{{Name: "p0", Path: "parts/000"}},
	})
	require.NoError(t, err)
	_, err = UnmarshalConfigBundleIndex(raw)
	require.NoError(t, err)

	_, err = UnmarshalConfigBundleIndex([]byte("checksum: \"\"\nparts:\n- name: p0\n"))
	require.ErrorContains(t, err, "empty checksum value")

	_, err = UnmarshalConfigBundleIndex([]byte("checksum: abc\nparts: []\n"))
	require.ErrorContains(t, err, "empty parts")
}

func TestReassembleBundleConfig_ReadError(t *testing.T) {
	index := &ConfigBundleIndex{
		Checksum: ConfigBundleChecksum([]byte("v")),
		Parts:    []ConfigBundlePart{{Name: "p0", Path: "parts/000"}},
	}
	_, err := ReassembleBundleConfig(index, func(_ ConfigBundlePart) ([]byte, error) {
		return nil, errors.New("boom")
	})
	require.ErrorContains(t, err, "failed to read bundle part")
}
