// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
	"github.com/stretchr/testify/require"
	func_e_api "github.com/tetratelabs/func-e/api"
)

func Test_downloadEnvoy(t *testing.T) {
	t.Run("ensure the version constant matches the .envoy-version file", func(t *testing.T) {
		root := internaltesting.FindProjectRoot()
		envoyVersionInRoot, err := os.ReadFile(root + "/.envoy-version")
		require.NoError(t, err)
		require.Equal(t, envoyVersion, strings.TrimSpace(string(envoyVersionInRoot)))
	})

	err := downloadEnvoy(t.Context(), func(_ context.Context, args []string, opts ...func_e_api.RunOption) error {
		require.Equal(t, []string{"--version"}, args)
		require.Len(t, opts, 9) // opts are internal so we can just count them
		return nil
	}, t.TempDir(), t.TempDir(), io.Discard, io.Discard)
	require.NoError(t, err)
}
