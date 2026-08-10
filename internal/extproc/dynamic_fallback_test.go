// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"log/slog"
	"strconv"
	"strings"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestDynamicFallbackHeaderMutations(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	setHeaders := func(sets []*corev3.HeaderValueOption) map[string]string {
		out := make(map[string]string, len(sets))
		for _, s := range sets {
			require.Equal(t, corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD, s.AppendAction)
			out[s.Header.Key] = string(s.Header.RawValue)
		}
		return out
	}

	t.Run("no chain and no forged headers is a no-op", func(t *testing.T) {
		headers := map[string]string{":path": "/v1/chat/completions"}
		sets, removes := dynamicFallbackHeaderMutations(headers, nil, logger)
		require.Empty(t, sets)
		require.Empty(t, removes)
		require.Equal(t, map[string]string{":path": "/v1/chat/completions"}, headers)
	})

	t.Run("forged gateway-owned headers are removed without a chain", func(t *testing.T) {
		headers := map[string]string{
			internalapi.EnvoyAttemptCountHeader:                "3",
			internalapi.DynamicFallbackSlotHeaderPrefix + "0":  "azure",
			internalapi.DynamicFallbackSlotHeaderPrefix + "15": "openai",
		}
		sets, removes := dynamicFallbackHeaderMutations(headers, nil, logger)
		require.Empty(t, sets)
		require.ElementsMatch(t, []string{
			internalapi.DynamicFallbackSlotHeaderPrefix + "0",
			internalapi.DynamicFallbackSlotHeaderPrefix + "15",
			internalapi.EnvoyAttemptCountHeader,
		}, removes)
		require.Empty(t, headers)
	})

	t.Run("chain becomes slot headers plus attempt count zero", func(t *testing.T) {
		headers := map[string]string{
			internalapi.FallbackChainHeader: " azure ,, openai ",
			// Forged inputs must be overwritten or removed even when a chain is present.
			internalapi.EnvoyAttemptCountHeader:               "7",
			internalapi.DynamicFallbackSlotHeaderPrefix + "0": "attacker",
			internalapi.DynamicFallbackSlotHeaderPrefix + "5": "attacker",
		}
		sets, removes := dynamicFallbackHeaderMutations(headers,
			map[string]struct{}{"azure": {}, "openai": {}}, logger)
		require.Equal(t, map[string]string{
			internalapi.DynamicFallbackSlotHeaderPrefix + "0": "azure",
			internalapi.DynamicFallbackSlotHeaderPrefix + "1": "openai",
			internalapi.EnvoyAttemptCountHeader:               "0",
		}, setHeaders(sets))
		require.ElementsMatch(t, []string{
			internalapi.FallbackChainHeader,
			internalapi.DynamicFallbackSlotHeaderPrefix + "5",
		}, removes)
		require.Equal(t, map[string]string{
			internalapi.DynamicFallbackSlotHeaderPrefix + "0": "azure",
			internalapi.DynamicFallbackSlotHeaderPrefix + "1": "openai",
			internalapi.EnvoyAttemptCountHeader:               "0",
		}, headers)
	})

	t.Run("chain longer than the slot cap is truncated", func(t *testing.T) {
		entries := make([]string, internalapi.DynamicFallbackMaxSlots+4)
		for i := range entries {
			entries[i] = "backend-" + strconv.Itoa(i)
		}
		headers := map[string]string{internalapi.FallbackChainHeader: strings.Join(entries, ",")}
		candidates := make(map[string]struct{}, len(entries))
		for _, e := range entries {
			candidates[e] = struct{}{}
		}
		sets, removes := dynamicFallbackHeaderMutations(headers, candidates, logger)
		// All slots plus the attempt count.
		require.Len(t, sets, internalapi.DynamicFallbackMaxSlots+1)
		got := setHeaders(sets)
		require.Equal(t, "backend-0", got[internalapi.DynamicFallbackSlotHeaderPrefix+"0"])
		require.Equal(t, "backend-15", got[internalapi.DynamicFallbackSlotHeaderPrefix+"15"])
		require.Equal(t, []string{internalapi.FallbackChainHeader}, removes)
	})

	t.Run("entries outside the candidate set are dropped", func(t *testing.T) {
		headers := map[string]string{internalapi.FallbackChainHeader: "azure,evil,openai"}
		sets, _ := dynamicFallbackHeaderMutations(headers,
			map[string]struct{}{"azure": {}, "openai": {}}, logger)
		require.Equal(t, map[string]string{
			internalapi.DynamicFallbackSlotHeaderPrefix + "0": "azure",
			internalapi.DynamicFallbackSlotHeaderPrefix + "1": "openai",
			internalapi.EnvoyAttemptCountHeader:               "0",
		}, setHeaders(sets))
	})

	t.Run("chain for a model without candidates injects nothing", func(t *testing.T) {
		headers := map[string]string{
			internalapi.FallbackChainHeader:                   "azure",
			internalapi.DynamicFallbackSlotHeaderPrefix + "1": "forged",
		}
		sets, removes := dynamicFallbackHeaderMutations(headers, nil, logger)
		require.Empty(t, sets)
		require.ElementsMatch(t, []string{
			internalapi.FallbackChainHeader,
			internalapi.DynamicFallbackSlotHeaderPrefix + "1",
		}, removes)
		require.Empty(t, headers)
	})

	t.Run("empty chain header only sanitizes", func(t *testing.T) {
		headers := map[string]string{
			internalapi.FallbackChainHeader:     " , ",
			internalapi.EnvoyAttemptCountHeader: "2",
		}
		sets, removes := dynamicFallbackHeaderMutations(headers,
			map[string]struct{}{"azure": {}}, logger)
		require.Empty(t, sets)
		require.ElementsMatch(t, []string{
			internalapi.FallbackChainHeader,
			internalapi.EnvoyAttemptCountHeader,
		}, removes)
		require.Empty(t, headers)
	})
}
