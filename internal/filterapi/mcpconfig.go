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

	// Authorization is the authorization configuration for this route.
	Authorization *MCPRouteAuthorization `json:"authorization,omitempty"`
}

// MCPBackend is the MCP backend configuration.
type MCPBackend struct {
	// Name is the fully qualified identifier of a MCP backend.
	// This name is set in [internalapi.MCPBackendHeader] header to route the request to the specific backend.
	Name MCPBackendName `json:"name"`

	// Path is the HTTP endpoint path of the backend MCP server.
	Path string `json:"path"`

	// ToolSelector filters the tools exposed by this backend. If not set, all tools are exposed.
	ToolSelector *MCPToolSelector `json:"toolSelector,omitempty"`
}

// MCPBackendName is the name of the MCP backend.
type MCPBackendName = string

// MCPToolSelector filters tools using include patterns with exact matches or regular expressions.
type MCPToolSelector struct {
	// Include is a list of tool names to include. Only the specified tools will be available.
	Include []string `json:"include,omitempty"`

	// IncludeRegex is a list of RE2-compatible regular expressions that, when matched, include the tool.
	// Only tools matching these patterns will be available.
	// TODO: regex is almost completely absent in the MCP ecosystem, consider removing this for simplicity.
	IncludeRegex []string `json:"includeRegex,omitempty"`
}

// MCPRouteName is the name of the MCP route.
type MCPRouteName = string

// MCPRouteAuthorization defines the authorization configuration for a MCPRoute.
type MCPRouteAuthorization struct {
	// Rules defines a list of authorization rules.
	// Requests that match any rule and satisfy the rule's conditions will be allowed.
	// Requests that do not match any rule or fail to satisfy the matched rule's conditions will be denied.
	// If no rules are defined, all requests will be denied.
	Rules []MCPRouteAuthorizationRule `json:"rules,omitempty"`

	// ResourceMetadataURL is the URI of the OAuth Protected Resource Metadata document for this route.
	// This is used to populate the WWW-Authenticate header when scope-based authorization fails.
	ResourceMetadataURL string `json:"resourceMetadataURL,omitempty"`
}

// MCPRouteAuthorizationRule defines an authorization rule for MCPRoute based on the MCP authorization spec.
// Reference: https://modelcontextprotocol.io/specification/draft/basic/authorization#scope-challenge-handling
type MCPRouteAuthorizationRule struct {
	// Source defines the authorization source for this rule.
	Source MCPAuthorizationSource `json:"source"`

	// Target defines the authorization target for this rule.
	Target MCPAuthorizationTarget `json:"target"`
}

type MCPAuthorizationTarget struct {
	// Tools defines the list of tools this rule applies to.
	Tools []ToolCall `json:"tools"`
}

type MCPAuthorizationSource struct {
	// JWTSource defines the JWT scopes required for this rule to match.
	JWTSource JWTSource `json:"jwtSource,omitempty"`
}

type JWTSource struct {
	// Scopes defines the list of JWT scopes required for the rule.
	// If multiple scopes are specified, all scopes must be present in the JWT for the rule to match.
	Scopes []string `json:"scopes"`
}

type ToolCall struct {
	// BackendName is the name of the backend this tool belongs to.
	BackendName string `json:"backendName"`

	// ToolName is the name of the tool.
	ToolName string `json:"toolName"`

	// Arguments defines required arguments (exact key names with regex patterns for values).
	// All patterns must match for the rule to apply.
	Arguments map[string]string `json:"arguments,omitempty"`
}
