// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	func_e_api "github.com/tetratelabs/func-e/api"
)

func Test_downloadEnvoy(t *testing.T) {
	err := downloadEnvoy(t.Context(), func(_ context.Context, args []string, opts ...func_e_api.RunOption) error {
		require.Equal(t, []string{"--version"}, args)
		require.Len(t, opts, 9) // opts are internal so we can just count them
		return nil
	}, t.TempDir(), t.TempDir(), io.Discard, io.Discard)
	require.NoError(t, err)
}

func Test_getEnvoyVersion(t *testing.T) {
	tests := []struct {
		name            string
		envVersion      string
		egVersion       string
		expectedVersion string
	}{
		{
			name:            "env override wins",
			envVersion:      "1.37.0",
			egVersion:       "1.36.2",
			expectedVersion: "1.37.0",
		},
		{
			name:            "fallback to default",
			envVersion:      "",
			egVersion:       "1.36.2",
			expectedVersion: "1.36.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ENVOY_VERSION", tt.envVersion)
			version, err := getEnvoyVersion("docker.io/envoyproxy/envoy:distroless-v" + tt.egVersion)
			require.NoError(t, err)
			require.Equal(t, tt.expectedVersion, version)
		})
	}
	t.Run("invalid image tag", func(t *testing.T) {
		_, err := getEnvoyVersion("docker.io/envoyproxy/envoy")
		require.Error(t, err)
	})
}
