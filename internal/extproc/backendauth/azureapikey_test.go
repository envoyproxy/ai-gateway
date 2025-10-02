// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestAzureAPIKeyHandler(t *testing.T) {
	t.Run("sets api-key header", func(t *testing.T) {
		handler, err := newAzureAPIKeyHandler(&filterapi.AzureAPIKeyAuth{Key: "test-azure-key"})
		require.NoError(t, err)

		headers := make(map[string]string)
		headerMut := &extprocv3.HeaderMutation{}

		err = handler.Do(context.Background(), headers, headerMut, nil)
		require.NoError(t, err)

		// Verify header in map
		require.Equal(t, "test-azure-key", headers["api-key"])

		// Verify header in mutation
		require.Len(t, headerMut.SetHeaders, 1)
		require.Equal(t, "api-key", headerMut.SetHeaders[0].Header.Key)
		require.Equal(t, "test-azure-key", string(headerMut.SetHeaders[0].Header.RawValue))
	})

	t.Run("trims whitespace", func(t *testing.T) {
		handler, err := newAzureAPIKeyHandler(&filterapi.AzureAPIKeyAuth{Key: "  key-with-spaces  "})
		require.NoError(t, err)

		headers := make(map[string]string)
		headerMut := &extprocv3.HeaderMutation{}

		err = handler.Do(context.Background(), headers, headerMut, nil)
		require.NoError(t, err)

		require.Equal(t, "key-with-spaces", headers["api-key"])
	})

	t.Run("requires non-empty key", func(t *testing.T) {
		_, err := newAzureAPIKeyHandler(&filterapi.AzureAPIKeyAuth{Key: ""})
		require.Error(t, err)
		require.Contains(t, err.Error(), "azure API key is required")
	})
}
