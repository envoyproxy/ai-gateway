// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestGoogleAIKeyHandler(t *testing.T) {
	t.Run("sets x-goog-api-key header", func(t *testing.T) {
		handler, err := newGoogleAIKeyHandler(&filterapi.GoogleAIKeyAuth{Key: "test-google-key"})
		require.NoError(t, err)

		headers := make(map[string]string)

		hdrs, err := handler.Do(context.Background(), headers, nil)
		require.NoError(t, err)

		// Verify header in map.
		require.Equal(t, "test-google-key", headers["x-goog-api-key"])

		// Verify header in mutation.
		require.Len(t, hdrs, 1)
		require.Equal(t, "x-goog-api-key", hdrs[0][0])
		require.Equal(t, "test-google-key", hdrs[0][1])
	})

	t.Run("trims whitespace", func(t *testing.T) {
		handler, err := newGoogleAIKeyHandler(&filterapi.GoogleAIKeyAuth{Key: "  key-with-spaces  "})
		require.NoError(t, err)

		headers := make(map[string]string)

		hdrs, err := handler.Do(context.Background(), headers, nil)
		require.NoError(t, err)

		require.Equal(t, "key-with-spaces", headers["x-goog-api-key"])
		require.Len(t, hdrs, 1)
		require.Equal(t, "x-goog-api-key", hdrs[0][0])
		require.Equal(t, "key-with-spaces", hdrs[0][1])
	})
}
