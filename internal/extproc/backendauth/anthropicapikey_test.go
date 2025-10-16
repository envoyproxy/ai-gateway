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

func TestAnthropicAPIKeyHandler(t *testing.T) {
	t.Run("sets api-key header", func(t *testing.T) {
		handler, err := newAnthropicAPIKeyHandler(&filterapi.AnthropicAPIKeyAuth{Key: "test-azure-key"})
		require.NoError(t, err)

		headers := make(map[string]string)
		headerMut := &extprocv3.HeaderMutation{}

		err = handler.Do(context.Background(), headers, headerMut, nil)
		require.NoError(t, err)

		// Verify header in map
		require.Equal(t, "test-azure-key", headers["x-api-key"])

		// Verify header in mutation
		require.Len(t, headerMut.SetHeaders, 1)
		require.Equal(t, "x-api-key", headerMut.SetHeaders[0].Header.Key)
		require.Equal(t, "test-azure-key", string(headerMut.SetHeaders[0].Header.RawValue))
	})

	t.Run("trims whitespace", func(t *testing.T) {
		handler, err := newAnthropicAPIKeyHandler(&filterapi.AnthropicAPIKeyAuth{Key: "  key-with-spaces  "})
		require.NoError(t, err)

		headers := make(map[string]string)
		headerMut := &extprocv3.HeaderMutation{}

		err = handler.Do(context.Background(), headers, headerMut, nil)
		require.NoError(t, err)

		require.Equal(t, "key-with-spaces", headers["x-api-key"])
	})
}
