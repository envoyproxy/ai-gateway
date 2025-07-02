// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package internalapi provides constants and functions used across the boundary
// among controller, extension server and extproc.
package internalapi

import "fmt"

const (
	// InternalEndpointMetadataNamespace is the namespace used for the dynamic metadata for internal use.
	InternalEndpointMetadataNamespace = "aigateway.envoy.io"
	// InternalMetadataBackendNameKey is the key used to store the backend name
	InternalMetadataBackendNameKey = "per_route_rule_backend_name"
)

// PerRouteRuleRefBackendName generates a unique backend name for a per-route rule,
// i.e., the unique identifier for a backend that is associated with a specific
// route rule in a specific AIGatewayRoute.
func PerRouteRuleRefBackendName(namespace, name, routeName string, routeRuleIndex, refIndex int) string {
	return fmt.Sprintf("%s/%s/route/%s/rule/%d/ref/%d", namespace, name, routeName, routeRuleIndex, refIndex)
}
