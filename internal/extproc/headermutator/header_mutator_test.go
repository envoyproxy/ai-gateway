// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package headermutator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
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
		mutation := mutator.Mutate(headers, false)

		require.NotNil(t, mutation)
		require.ElementsMatch(t, []string{"authorization", "x-api-key"}, mutation.RemoveHeaders)
		require.Len(t, mutation.SetHeaders, 1)
		require.Equal(t, "x-new-header", mutation.SetHeaders[0].Header.Key)
		require.Equal(t, []byte("newval"), mutation.SetHeaders[0].Header.RawValue)
		// Sensitive headers remain locally for metrics, but will be stripped upstream by Envoy.
		require.Equal(t, "secret", headers["authorization"])
		require.Equal(t, "key123", headers["x-api-key"])
		require.Equal(t, "newval", headers["x-new-header"])
		require.Equal(t, "value", headers["other"])
	})

	t.Run("restore original headers on retry", func(t *testing.T) {
		originalHeaders := map[string]string{
			"authorization": "secret",
			"x-api-key":     "key123",
			"other":         "value",
		}
		headers := map[string]string{
			"other":         "value",
			"authorization": "key123",
		}
		mutations := &filterapi.HTTPHeaderMutation{
			Remove: []string{"authorization"},
			Set:    []filterapi.HTTPHeader{},
		}
		mutator := NewHeaderMutator(mutations, originalHeaders)
		mutation := mutator.Mutate(headers, true)

		require.NotNil(t, mutation)
		require.ElementsMatch(t, []string{"authorization"}, mutation.RemoveHeaders)
		require.Equal(t, "key123", headers["x-api-key"])
		require.Equal(t, "value", headers["other"])
		// authorization should not be restored since it's in Remove.
		require.NotContains(t, headers, "authorization")
	})
}
