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

func TestAnthropicAPIKeyHandler(t *testing.T) {
	t.Run("sets api-key header", func(t *testing.T) {
		handler, err := newAnthropicAPIKeyHandler(&filterapi.AnthropicAPIKeyAuth{Key: "test-azure-key"})
		require.NoError(t, err)

		headers := make(map[string]string)

		hders, err := handler.Do(context.Background(), headers, nil)
		require.NoError(t, err)

		// Verify header in map
		require.Equal(t, "test-azure-key", headers["x-api-key"])

		// Verify header in mutation
		require.Len(t, hders, 1)
		require.Equal(t, "x-api-key", hders[0][0])
		require.Equal(t, "test-azure-key", hders[0][1])
	})

	t.Run("trims whitespace", func(t *testing.T) {
		handler, err := newAnthropicAPIKeyHandler(&filterapi.AnthropicAPIKeyAuth{Key: "  key-with-spaces  "})
		require.NoError(t, err)

		headers := make(map[string]string)

		hdrs, err := handler.Do(context.Background(), headers, nil)
		require.NoError(t, err)

		require.Equal(t, "key-with-spaces", headers["x-api-key"])
		require.Len(t, hdrs, 1)
		require.Equal(t, "x-api-key", hdrs[0][0])
		require.Equal(t, "key-with-spaces", hdrs[0][1])
	})
}
