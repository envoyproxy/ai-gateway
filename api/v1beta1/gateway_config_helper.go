// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1beta1

import (
	"sort"
)

// RateLimitSourceNamespaces returns the sorted, de-duplicated set of filter metadata namespaces
// (source.fromMetadata.namespace) referenced by the GatewayConfig's GlobalRateLimits. These are the
// namespaces a preceding filter writes the per-request rate-limit value into, which the router-level
// ext_proc filter must forward. Returns nil when none are configured.
func (in *GatewayConfig) RateLimitSourceNamespaces() []string {
	set := map[string]struct{}{}
	for _, rl := range in.Spec.GlobalRateLimits {
		if ns := rl.Source.FromMetadata.Namespace; ns != "" {
			set[ns] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	namespaces := make([]string, 0, len(set))
	for ns := range set {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	return namespaces
}
