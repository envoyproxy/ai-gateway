// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package headermutator

import (
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

type HeaderMutator struct {
	// originalHeaders is a map of original headers inherited from the router processor.
	originalHeaders map[string]string

	// headerMutations is a list of header mutations to apply.
	headerMutations *filterapi.HTTPHeaderMutation
}

func NewHeaderMutator(headerMutations *filterapi.HTTPHeaderMutation, originalHeaders map[string]string) *HeaderMutator {
	return &HeaderMutator{
		originalHeaders: originalHeaders,
		headerMutations: headerMutations,
	}
}

// Mutate mutates the headers based on the header mutations and restores original headers if mutated previously.
func (h *HeaderMutator) Mutate(headers map[string]string, onRetry bool) (sets []internalapi.Header, removes []string) {
	skipRemove := h.headerMutations == nil || len(h.headerMutations.Remove) == 0
	skipSet := h.headerMutations == nil || len(h.headerMutations.Set) == 0

	// Removes sensitive headers before sending to backend.
	removedHeadersSet := make(map[string]struct{})
	if !skipRemove {
		for _, key := range h.headerMutations.Remove {
			if shouldIgnoreHeader(key) {
				continue
			}
			removedHeadersSet[key] = struct{}{}
			if _, ok := headers[key]; ok {
				// Do NOT delete from the local headers map so metrics can still read it.
				// Instead, always instruct Envoy to remove it before forwarding upstream.
				removes = append(removes, key)
			}
		}
	}

	// Set the headers.
	setHeadersSet := make(map[string]struct{})
	if !skipSet {
		for _, h := range h.headerMutations.Set {
			key := h.Name
			if shouldIgnoreHeader(key) {
				continue
			}
			setHeadersSet[key] = struct{}{}
			headers[key] = h.Value
			sets = append(sets, [2]string{key, h.Value})
		}
	}

	if onRetry {
		// Restore original headers on retry, only if not being removed, set or not already present.
		for key, v := range h.originalHeaders {
			if shouldIgnoreHeader(key) {
				continue
			}
			_, isRemoved := removedHeadersSet[key]
			_, isSet := setHeadersSet[key]
			_, exists := headers[key]
			if !exists && !isSet {
				// Restore the original header value irrespective of whether it was removed or not in the headers map if it doesn't exist in the set headers
				// so that metrics can still read it.
				headers[key] = v
				if !isRemoved {
					setHeadersSet[key] = struct{}{}
					sets = append(sets, [2]string{key, v})
				}
			}
		}
		// 1. Remove any headers that were added in the previous attempt (not part of original headers and not being set now).
		// 2. Restore any original headers that were modified in the previous attempt (and not being set now).
		for key := range headers {
			if shouldIgnoreHeader(key) {
				continue
			}
			_, isSet := setHeadersSet[key]
			_, isRemoved := removedHeadersSet[key]
			if isRemoved || isSet {
				continue
			}
			originalValue, exists := h.originalHeaders[key]
			if !exists {
				delete(headers, key)
				removes = append(removes, key)
			} else {
				// Restore original value.
				headers[key] = originalValue
				sets = append(sets, [2]string{key, originalValue})
			}
		}
	}
	return
}

// shouldIgnoreHeader returns true if the header key should be ignored for mutation.
//
// Skip Envoy AI Gateway headers since some of them are populated after the originalHeaders are captured.
// This should be safe since these headers are managed by Envoy AI Gateway itself, not expected to be
// modified by users via header mutation API.
//
// Also, skip Envoy pseudo-headers beginning with ':', such as ":method", ":path", etc.
// This is important because these headers are not only sensitive to our implementation detail as well as
// it can cause unexpected behavior if they are modified unexpectedly. User shouldn't need to
// modify these headers via header mutation API.
func shouldIgnoreHeader(key string) bool {
	return strings.HasPrefix(key, ":") || strings.HasPrefix(key, internalapi.EnvoyAIGatewayHeaderPrefix)
}
