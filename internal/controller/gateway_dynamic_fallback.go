// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
)

// dynamicFallbackRuleCandidates returns the rule's published fallback vocabulary (alias when
// set, resource name otherwise, in declaration order), or nil when the rule is ineligible. The
// eligibility conditions MUST mirror activeDynamicFallbackRefs in
// internal/extensionserver/dynamic_fallback.go, so candidates (and composed backend entries)
// are only published for rules the extension server actually rewrites. Envoy Gateway may still
// split a rule into weighted per-ref clusters for reasons only visible at translation time;
// such rules' candidates are inert (documented in the proposal's limitations).
func dynamicFallbackRuleCandidates(rule *aigv1b1.AIGatewayRouteRule, routeNamespace string) []string {
	candidates := make([]string, 0, len(rule.BackendRefs))
	seenBackends := make(map[string]struct{}, len(rule.BackendRefs))
	seenNames := make(map[string]struct{}, len(rule.BackendRefs))
	seenPriorities := make(map[uint32]struct{}, len(rule.BackendRefs))
	for i := range rule.BackendRefs {
		ref := &rule.BackendRefs[i]
		if ref.IsInferencePool() {
			return nil
		}
		if ref.Weight != nil && *ref.Weight == 0 {
			continue
		}
		var priority uint32
		if ref.Priority != nil {
			priority = *ref.Priority
		}
		name := ref.Name
		if ref.Alias != "" {
			name = ref.Alias
		}
		backendKey := string(ref.GetNamespace(routeNamespace)) + "/" + ref.Name
		if _, dup := seenBackends[backendKey]; dup {
			return nil
		}
		if _, dup := seenNames[name]; dup {
			return nil
		}
		if _, dup := seenPriorities[priority]; dup {
			return nil
		}
		seenBackends[backendKey] = struct{}{}
		seenNames[name] = struct{}{}
		seenPriorities[priority] = struct{}{}
		candidates = append(candidates, name)
	}
	if len(candidates) < 2 {
		return nil
	}
	return candidates
}
