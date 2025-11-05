// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package headermutator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestHeaderMutator_Mutate(t *testing.T) {
	t.Run("remove and set headers", func(t *testing.T) {
		headers := map[string]string{
			"authorization": "secret",
			"x-api-key":     "key123",
			"other":         "value",
		}
		mutations := &filterapi.HTTPHeaderMutation{
			Remove: []string{"authorization", "x-api-key"},
			Set:    []filterapi.HTTPHeader{{Name: "x-new-header", Value: "newval"}},
		}
		mutator := NewHeaderMutator(mutations, nil)
		sets, removes := mutator.Mutate(headers, false)

		require.ElementsMatch(t, []string{"authorization", "x-api-key"}, removes)
		require.Len(t, sets, 1)
		require.Equal(t, "x-new-header", sets[0][0])
		require.Equal(t, "newval", sets[0][1])
		// Sensitive headers remain locally for metrics, but will be stripped upstream by Envoy.
		require.Equal(t, "secret", headers["authorization"])
		require.Equal(t, "key123", headers["x-api-key"])
		require.Equal(t, "newval", headers["x-new-header"])
		require.Equal(t, "value", headers["other"])
	})

	t.Run("restore original headers on retry", func(t *testing.T) {
		originalHeaders := map[string]string{
			"authorization":    "secret",
			"x-api-key":        "key123",
			"other":            "value",
			"only-in-original": "original",
			"in-original-too-but-previous-attempt-set": "pikachu",
			// Envoy pseudo-header should be ignored.
			":path": "/v1/endpoint",
			// Internal headers should be ignored.
			internalapi.EnvoyAIGatewayHeaderPrefix + "-foo-bar": "should-not-be-included",
		}
		headers := map[string]string{
			"other":         "value",
			"authorization": "secret",
			"in-original-too-but-previous-attempt-set": "charmander",
			"only-set-previously":                      "bulbasaur",
			// Internal headers should be ignored.
			internalapi.EnvoyAIGatewayHeaderPrefix + "-dog-cat": "should-not-be-included",
		}
		mutations := &filterapi.HTTPHeaderMutation{
			Remove: []string{"authorization"},
			Set:    []filterapi.HTTPHeader{},
		}
		mutator := NewHeaderMutator(mutations, originalHeaders)
		sets, removes := mutator.Mutate(headers, true)

		require.ElementsMatch(t, []string{"authorization", "only-set-previously"}, removes)
		require.Len(t, sets, 4)
		setHeadersMap := make(map[string]string)
		for _, h := range sets {
			key, value := h[0], h[1]
			setHeadersMap[key] = value
		}
		require.Equal(t, "key123", setHeadersMap["x-api-key"])
		require.Equal(t, "value", setHeadersMap["other"])
		// Removed header should not be added back via SetHeaders on retry
		_, ok := setHeadersMap["authorization"]
		require.False(t, ok)
		require.Equal(t, "original", setHeadersMap["only-in-original"])
		require.Equal(t, "pikachu", setHeadersMap["in-original-too-but-previous-attempt-set"])
		// Check the final headers map too.
		require.Equal(t, "key123", headers["x-api-key"])
		require.Equal(t, "value", headers["other"])
		require.Equal(t, "secret", headers["authorization"])
		require.Equal(t, "original", headers["only-in-original"])
		require.Equal(t, "pikachu", headers["in-original-too-but-previous-attempt-set"])
	})
}
