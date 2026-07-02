// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mainlib

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestModelsEndpointPaths(t *testing.T) {
	t.Run("default prefixes produce one path per distinct prefix", func(t *testing.T) {
		ep := internalapi.EndpointPrefixes{OpenAI: "/", Anthropic: "/anthropic", Cohere: "/cohere"}
		got := modelsEndpointPaths("/", ep)
		require.Equal(t, []string{"/v1/models", "/anthropic/v1/models", "/cohere/v1/models"}, got)
	})

	t.Run("coinciding prefixes are deduped", func(t *testing.T) {
		ep := internalapi.EndpointPrefixes{OpenAI: "/", Anthropic: "/", Cohere: "/"}
		got := modelsEndpointPaths("/", ep)
		require.Equal(t, []string{"/v1/models"}, got)
	})

	t.Run("non-root rootPrefix is honored", func(t *testing.T) {
		ep := internalapi.EndpointPrefixes{OpenAI: "/", Anthropic: "/anthropic", Cohere: "/cohere"}
		got := modelsEndpointPaths("/base", ep)
		require.Equal(t, []string{"/base/v1/models", "/base/anthropic/v1/models", "/base/cohere/v1/models"}, got)
	})
}
