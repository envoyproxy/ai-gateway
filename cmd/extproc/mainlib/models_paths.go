// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mainlib

import (
	"path"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// modelsEndpointPaths returns the set of "/v1/models" paths to register the models
// processor under — one per distinct configured endpoint prefix, preserving
// OpenAI, Anthropic, Cohere order and deduping paths that collapse to the same value
// (e.g. when prefixes coincide). Registering under every prefix lets discovery clients
// reach the models list at whatever prefix they use for their data-plane calls
// (e.g. Claude Code's ANTHROPIC_BASE_URL carries the "/anthropic" prefix).
func modelsEndpointPaths(rootPrefix string, ep internalapi.EndpointPrefixes) []string {
	prefixes := []string{ep.OpenAI, ep.Anthropic, ep.Cohere}
	seen := make(map[string]struct{}, len(prefixes))
	paths := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		full := path.Join(rootPrefix, p, "/v1/models")
		if _, ok := seen[full]; ok {
			continue
		}
		seen[full] = struct{}{}
		paths = append(paths, full)
	}
	return paths
}
