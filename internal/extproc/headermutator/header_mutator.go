// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package headermutator

import (
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

type HeaderMutator struct {
	// getOrignalHeaders Callback to get removed sensitive headers from the router filter.
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
func (h *HeaderMutator) Mutate(headers map[string]string, onRetry bool) *extprocv3.HeaderMutation {
	skipRemove := h.headerMutations == nil || len(h.headerMutations.Remove) == 0
	skipSet := h.headerMutations == nil || len(h.headerMutations.Set) == 0

	headerMutation := &extprocv3.HeaderMutation{}
	// Removes sensitive headers before sending to backend.
	removedHeadersSet := make(map[string]struct{})
	if !skipRemove {
		for _, h := range h.headerMutations.Remove {
			key := strings.ToLower(h)
			removedHeadersSet[key] = struct{}{}
			if _, ok := headers[key]; ok {
				// Do NOT delete from the local headers map so metrics can still read it.
				// Instead, always instruct Envoy to remove it before forwarding upstream.
				headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, h)
			}
		}
	}

	// Set the headers.
	setHeadersSet := make(map[string]struct{})
	if !skipSet {
		for _, h := range h.headerMutations.Set {
			key := strings.ToLower(h.Name)
			setHeadersSet[key] = struct{}{}
			headers[key] = h.Value
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{Key: h.Name, RawValue: []byte(h.Value)},
			})
		}
	}

	// Restore original headers on retry, only if not being removed, set or not already present.
	if onRetry && h.originalHeaders != nil {
		for h, v := range h.originalHeaders {
			key := strings.ToLower(h)
			_, isRemoved := removedHeadersSet[key]
			_, isSet := setHeadersSet[key]
			_, exists := headers[key]
			if !isRemoved && !exists && !isSet {
				headers[h] = v
				headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
					Header: &corev3.HeaderValue{Key: h, RawValue: []byte(v)},
				})
			}
		}
	}

	return headerMutation
}
