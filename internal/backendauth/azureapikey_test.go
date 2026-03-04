// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestAzureAPIKeyHandler(t *testing.T) {
	t.Run("sets api-key header", func(t *testing.T) {
		handler, err := newAzureAPIKeyHandler(&filterapi.AzureAPIKeyAuth{Key: "test-azure-key"})
		require.NoError(t, err)

		headers := make(map[string]string)

		hdrs, err := handler.Do(t.Context(), headers, nil)
		require.NoError(t, err)

		// Verify header in map
		require.Equal(t, "test-azure-key", headers["api-key"])

		// Verify header in mutation
		require.Len(t, hdrs, 1)
		require.Equal(t, "api-key", hdrs[0][0])
		require.Equal(t, "test-azure-key", hdrs[0][1])
	})

	t.Run("trims whitespace", func(t *testing.T) {
		handler, err := newAzureAPIKeyHandler(&filterapi.AzureAPIKeyAuth{Key: "  key-with-spaces  "})
		require.NoError(t, err)

		headers := make(map[string]string)
		hdrs, err := handler.Do(t.Context(), headers, nil)
		require.NoError(t, err)

		require.Equal(t, "key-with-spaces", headers["api-key"])
		require.Len(t, hdrs, 1)
		require.Equal(t, "api-key", hdrs[0][0])
		require.Equal(t, "key-with-spaces", hdrs[0][1])
	})

	t.Run("requires non-empty key", func(t *testing.T) {
		_, err := newAzureAPIKeyHandler(&filterapi.AzureAPIKeyAuth{Key: ""})
		require.Error(t, err)
		require.Contains(t, err.Error(), "azure API key is required")
	})
}
