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

const (
	// EndpointPickerHeaderKey is the header key used to specify the target backend endpoint.
	// This is the default header name in the reference implementation:
	// https://github.com/kubernetes-sigs/gateway-api-inference-extension/blob/2b5b337b45c3289e5f9367b2c19deef021722fcd/pkg/epp/server/runserver.go#L63
	EndpointPickerHeaderKey = "x-gateway-destination-endpoint"
)

const (
	// XDSClusterMetadataKey is the key used to access cluster metadata in xDS attributes
	XDSClusterMetadataKey = "xds.cluster_metadata"
	// XDSUpstreamHostMetadataKey is the key used to access upstream host metadata in xDS attributes
	XDSUpstreamHostMetadataKey = "xds.upstream_host_metadata"
)

// PerRouteRuleRefBackendName generates a unique backend name for a per-route rule,
// i.e., the unique identifier for a backend that is associated with a specific
// route rule in a specific AIGatewayRoute.
func PerRouteRuleRefBackendName(namespace, name, routeName string, routeRuleIndex, refIndex int) string {
	return fmt.Sprintf("%s/%s/route/%s/rule/%d/ref/%d", namespace, name, routeName, routeRuleIndex, refIndex)
}
