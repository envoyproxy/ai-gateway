// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"log/slog"
	"strconv"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// dynamicFallbackHeaderMutations enforces the dynamic fallback header contract. It runs on
// every request of a gateway with at least one opted-in route
// (RuntimeConfig.DynamicFallbackEnabled).
//
// The x-aigw-try-<k> slot headers and x-envoy-attempt-count are trusted matcher inputs, so any
// client-supplied value is removed or overwritten (a forged attempt count would steer the first
// attempt to an arbitrary slot). When the chain header is present AND the requested model has
// published candidates, the validated entries become the slot headers — slot k serves attempt
// k+1 — and x-envoy-attempt-count is injected as "0" per the ordering contract. Unknown entries
// are dropped with a log; models without candidates get no injected headers; the chain header
// itself never leaves the router.
//
// requestHeaders is updated in place to mirror the returned mutations, keeping later header
// consumers (CEL attributes, tracing) consistent with what Envoy sees.
func dynamicFallbackHeaderMutations(requestHeaders map[string]string, candidates map[string]struct{}, logger *slog.Logger) (sets []*corev3.HeaderValueOption, removes []string) {
	var entries []string
	if chain, ok := requestHeaders[internalapi.FallbackChainHeader]; ok {
		if len(candidates) == 0 {
			logger.Debug("fallback chain supplied for a model without fallback candidates; ignoring")
		} else {
			for e := range strings.SplitSeq(chain, ",") {
				e = strings.TrimSpace(e)
				if e == "" {
					continue
				}
				if _, ok := candidates[e]; !ok {
					logger.Warn("fallback chain entry is not a published candidate of the model; dropping it",
						slog.String("entry", e))
					continue
				}
				if len(entries) == internalapi.DynamicFallbackMaxSlots {
					logger.Warn("fallback chain exceeds the supported number of slots; ignoring the tail",
						slog.Int("max_slots", internalapi.DynamicFallbackMaxSlots))
					break
				}
				entries = append(entries, e)
			}
		}
		removes = append(removes, internalapi.FallbackChainHeader)
		delete(requestHeaders, internalapi.FallbackChainHeader)
	}

	overwrite := func(key, value string) {
		sets = append(sets, &corev3.HeaderValueOption{
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			Header:       &corev3.HeaderValue{Key: key, RawValue: []byte(value)},
		})
		requestHeaders[key] = value
	}
	removeIfPresent := func(key string) {
		if _, ok := requestHeaders[key]; ok {
			removes = append(removes, key)
			delete(requestHeaders, key)
		}
	}

	for k := range internalapi.DynamicFallbackMaxSlots {
		slotHeader := internalapi.DynamicFallbackSlotHeaderPrefix + strconv.Itoa(k)
		if k < len(entries) {
			overwrite(slotHeader, entries[k])
		} else {
			removeIfPresent(slotHeader)
		}
	}
	if len(entries) > 0 {
		overwrite(internalapi.EnvoyAttemptCountHeader, "0")
	} else {
		removeIfPresent(internalapi.EnvoyAttemptCountHeader)
	}
	return sets, removes
}
