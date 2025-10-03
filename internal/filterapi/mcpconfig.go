// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package filterapi

// MCPConfig is the configuration for the MCP listener and routing.
type MCPConfig struct {
	// BackendListenerAddr is the address that speaks plain HTTP and can be used to
	// route to each backend directly without interruption.
	//
	// The listener should only listen on the local interface, and equipped with
	// the HCM filter with the plain header-based routing for each backend based
	// on the [internalapi.MCPBackendHeader] header.
	BackendListenerAddr string `json:"backendListenerAddr"`

	// Routes is the list of routes that this listener can route to.
	Routes []MCPRoute `json:"routes,omitempty"`
}

// MCPRoute is the route configuration for routing to each MCP backend based on the tool name.
type MCPRoute struct {
	// Name is the fully qualified identifier of a MCPRoute.
	// This name is set in [internalapi.MCPRouteHeader] header to identify the route.
	Name MCPRouteName `json:"name"`

	// Backends is the list of backends that this route can route to.
	Backends []MCPBackend `json:"backends"`
}

// MCPBackend is the MCP backend configuration.
type MCPBackend struct {
	// Name is the fully qualified identifier of a MCP backend.
	// This name is set in [internalapi.MCPBackendHeader] header to route the request to the specific backend.
	Name MCPBackendName `json:"name"`

	// Path is the HTTP endpoint path of the backend MCP server.
	Path string `json:"path"`

	// ToolSelector filters the tools exposed by this backend. If not set, all tools are exposed.
	ToolSelector *MCPNameSelector `json:"toolSelector,omitempty"`
}

// MCPBackendName is the name of the MCP backend.
type MCPBackendName = string

// MCPNameSelector is a filter that selects MCP resources by their names.
// Only one of Include or IncludeRegex can be specified.
type MCPNameSelector struct {
	// Include is a list of strings to include. If specified, only the strings in this list are included.
	Include []string `json:"include,omitempty"`

	// IncludeRegex is a list of RE2-compatible regular expressions that, when matched, include the string.
	IncludeRegex []string `json:"includeRegex,omitempty"`
}

// MCPRouteName is the name of the MCP route.
type MCPRouteName = string
