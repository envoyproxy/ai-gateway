// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// MCPRoute defines how to route MCP requests to the backend MCP servers.
//
// This serves as a way to define a "unified" AI API for a Gateway which allows downstream
// clients to use a single schema API to interact with multiple MCP backends.
//
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[-1:].type`
type MCPRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the details of the MCPRoute.
	Spec MCPRouteSpec `json:"spec,omitempty"`
	// Status defines the status details of the MCPRoute.
	Status MCPRouteStatus `json:"status,omitempty"`
}

// MCPRouteList contains a list of MCPRoute.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type MCPRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MCPRoute `json:"items"`
}

// MCPRouteSpec details the MCPRoute configuration.
type MCPRouteSpec struct {
	// ParentRefs are the names of the Gateway resources this MCPRoute is being attached to.
	// Cross namespace references are not supported. In other words, the Gateway resources must be in the
	// same namespace as the MCPRoute. Currently, each reference's Kind must be Gateway.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self.all(match, match.kind == 'Gateway')", message="only Gateway is supported"
	ParentRefs []gwapiv1.ParentReference `json:"parentRefs"`

	// Path is the HTTP endpoint path that serves MCP requests over the Streamable HTTP transport.
	// If not specified, the default is "/mcp".
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=/mcp
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Path *string `json:"path,omitempty"`

	// Headers are HTTP headers that must match for this route to be selected.
	// Multiple match values are ANDed together, meaning, a request must match all the specified headers to select the route.
	//
	// +listType=map
	// +listMapKey=name
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Headers []gwapiv1.HTTPHeaderMatch `json:"headers,omitempty"`

	// BackendRefs is a list of backend references to the MCP servers.
	// These MCP servers will be aggregated and exposed as a single MCP endpoint to the clients.
	// From the client's perspective, they only need to configure a single MCP server URL, e.g. "https://api.example.com/mcp",
	// and the Envoy AI Gateway will route the requests to the appropriate MCP server based on the requests.
	//
	// All names must be unique within this list to avoid potential tools, resources, etc. name collisions.
	// Also, cross-namespace references are not supported. In other words, the backend MCP servers must be in the
	// same namespace as the MCPRoute.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:XValidation:rule="self.all(i, self.exists_one(j, j.name == i.name))", message="all backendRefs names must be unique"
	BackendRefs []MCPRouteBackendRef `json:"backendRefs"`

	// SecurityPolicy defines the security policy for this MCPRoute.
	//
	// +kubebuilder:validation:Optional
	// +optional
	SecurityPolicy *MCPRouteSecurityPolicy `json:"securityPolicy,omitempty"`
}

// MCPRouteBackendRef wraps a EG's BackendObjectReference to reference an MCP server.
// TODO: move to a standalone MCPBackend CRD to avoid k8s object size limit.
type MCPRouteBackendRef struct {
	gwapiv1.BackendObjectReference `json:",inline"`

	// Path is the HTTP endpoint path of the baackend MCP server.
	// If not specified, the default is "/mcp".
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=/mcp
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Path *string `json:"path,omitempty"`

	// ToolSelector filters the tools exposed by this MCP server.
	// Supports exact matches and RE2-compatible regular expressions for both include and exclude patterns.
	// If not specified, all tools from the MCP server are exposed.
	// +kubebuilder:validation:Optional
	// +optional
	ToolSelector *MCPToolFilter `json:"toolSelector,omitempty"`

	// TODO: we can add resource and prompt selectors in the future.

	// SecurityPolicy is the security policy to apply to this MCP server.
	//
	// +kubebuilder:validation:Optional
	// +optional
	SecurityPolicy *MCPBackendSecurityPolicy `json:"securityPolicy,omitempty"`

	// TODO: add fancy per-MCP server config. For example, Rate Limit, etc.
}

// MCPToolFilter filters tools using include patterns with exact matches or regular expressions.
//
// +kubebuilder:validation:XValidation:rule="(has(self.include) && !has(self.includeRegex)) || (!has(self.include) && has(self.includeRegex))", message="exactly one of include or includeRegex must be specified"
type MCPToolFilter struct {
	// Include is a list of tool names to include. Only the specified tools will be available.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=32
	// +optional
	Include []string `json:"include,omitempty"`

	// IncludeRegex is a list of RE2-compatible regular expressions that, when matched, include the tool.
	// Only tools matching these patterns will be available.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=32
	// +optional
	IncludeRegex []string `json:"includeRegex,omitempty"`
}

// MCPBackendSecurityPolicy defines the security policy for a sp
type MCPBackendSecurityPolicy struct {
	// APIKey is a mechanism to access a backend. The API key will be injected into the request headers.
	// +optional
	APIKey *MCPBackendAPIKey `json:"apiKey,omitempty"`
}

// MCPBackendAPIKey defines the configuration for the API Key Authentication to a backend.
//
// +kubebuilder:validation:XValidation:rule="(has(self.secretRef) && !has(self.inline)) || (!has(self.secretRef) && has(self.inline))", message="exactly one of secretRef or inline must be set"
type MCPBackendAPIKey struct {
	// secretRef is the Kubernetes secret which contains the API keys.
	// The key of the secret should be "apiKey".
	// +optional
	SecretRef *gwapiv1.SecretObjectReference `json:"secretRef,omitempty"`

	// Inline contains the API key as an inline string.
	//
	// +optional
	Inline *string `json:"inline,omitempty"`

	// Header is the HTTP header to inject the API key into. If not specified,
	// defaults to "Authorization".
	// When the header is "Authorization", the injected header value will be
	// prefixed with "Bearer ".
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +optional
	Header *string `json:"header,omitempty"`
}

// MCPRouteSecurityPolicy defines the security policy for a MCPRoute.
//
// +kubebuilder:validation:XValidation:rule="!has(self.authorization) || has(self.oauth)",message="oauth must be configured when authorization is set"
type MCPRouteSecurityPolicy struct {
	// OAuth defines the configuration for the MCP spec compatible OAuth authentication.
	//
	// +optional
	OAuth *MCPRouteOAuth `json:"oauth,omitempty"`

	// APIKeyAuth defines the configuration for the API Key Authentication.
	//
	// +optional
	APIKeyAuth *egv1a1.APIKeyAuth `json:"apiKeyAuth,omitempty"`

	// ExtAuth defines the configuration for External Authorization.
	//
	// +optional
	ExtAuth *egv1a1.ExtAuth `json:"extAuth,omitempty"`

	// Authorization defines the configuration for the MCP spec compatible authorization.
	//
	// +optional
	Authorization *MCPRouteAuthorization `json:"authorization,omitempty"`
}

// MCPRouteOAuth defines a MCP spec compatible OAuth authentication configuration for a MCPRoute.
// Reference: https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization
type MCPRouteOAuth struct {
	// Issuer is the authorization server's issuer identity.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Format=uri
	Issuer string `json:"issuer"`

	// Audiences is a list of JWT audiences allowed access.
	// It is recommended to set this field for token audience validation, as it is a security best practice to prevent token misuse.
	// Reference: https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization#token-audience-binding-and-validation
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=32
	Audiences []string `json:"audiences"`

	// JWKS defines how a JSON Web Key Sets (JWKS) can be obtained to verify the access tokens presented by the clients.
	//
	// If not specified, the JWKS URI will be discovered from the OAuth 2.0 Authorization Server Metadata
	// as per RFC 8414 by querying the `/.well-known/oauth-authorization-server` endpoint on the Issuer.
	//
	// +optional
	JWKS *JWKS `json:"jwks,omitempty"`

	// ProtectedResourceMetadata defines the OAuth 2.0 Resource Server Metadata as per RFC 8414.
	// This is used to expose the metadata endpoint for mcp clients to discover the authorization servers,
	// supported scopes, and JWKS URI.
	//
	// +kubebuilder:validation:Required
	ProtectedResourceMetadata ProtectedResourceMetadata `json:"protectedResourceMetadata"`
}

// MCPRouteAuthorization defines the authorization configuration for a MCPRoute.
type MCPRouteAuthorization struct {
	// Rules defines a list of authorization rules.
	// These rules are evaluated in order, the first matching rule will be applied,
	// and the rest will be skipped.
	//
	// +optional
	Rules []MCPRouteAuthorizationRule `json:"rules,omitempty"`

	// DefaultAction defines the default action to be taken if no rules match.
	// If not specified, the default action is Deny.
	// +optional
	DefaultAction *egv1a1.AuthorizationAction `json:"defaultAction"`
}

// MCPRouteAuthorizationRule defines an authorization rule for MCPRoute based on the MCP authorization spec.
// Reference: https://modelcontextprotocol.io/specification/draft/basic/authorization#scope-challenge-handling
type MCPRouteAuthorizationRule struct {
	// Source defines the authorization source for this rule.
	//
	// +kubebuilder:validation:Required
	Source MCPAuthorizationSource `json:"source"`

	// Target defines the authorization target for this rule.
	//
	// +kubebuilder:validation:Required
	Target MCPAuthorizationTarget `json:"target"`

	// Action defines whether to allow or deny requests that match this rule.
	//
	// +kubebuilder:validation:Required
	Action egv1a1.AuthorizationAction `json:"action"`
}

type MCPAuthorizationTarget struct {
	// Tools defines the list of tools this rule applies to.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Tools []ToolCall `json:"tools"`
}

type MCPAuthorizationSource struct {
	// JWTSource defines the JWT scopes required for this rule to match.
	//
	// +kubebuilder:validation:Required
	JWTSource JWTSource `json:"jwtSource"`

	// TODO: JWTSource can be optional in the future when we support more source types.
}

type JWTSource struct {
	// Scopes defines the list of JWT scopes required for the rule.
	// If multiple scopes are specified, all scopes must be present in the JWT for the rule to match.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Scopes []egv1a1.JWTScope `json:"scopes"`

	// TODO : we can add more fields in the future, e.g., audiences, claims, etc.
}

type ToolCall struct {
	// BackendName is the name of the backend this tool belongs to.
	//
	// +kubebuilder:validation:Required
	BackendName string `json:"backendName"`

	// ToolName is the name of the tool.
	//
	// +kubebuilder:validation:Required
	ToolName string `json:"toolName"`

	// Arguments defines the arguments that must be present in the tool call for this rule to match.
	// Keys must exist and their values must match the provided RE2-compatible regular expressions.
	// If the argument is a non-string type, it will be matched against its JSON representation.
	//
	// +optional
	Arguments map[string]string `json:"arguments,omitempty"`
}

// JWKS defines how to obtain JSON Web Key Sets (JWKS) either from a remote HTTP/HTTPS endpoint or from a local source.
// +kubebuilder:validation:XValidation:rule="has(self.remoteJWKS) || has(self.localJWKS)", message="either remoteJWKS or localJWKS must be specified."
// +kubebuilder:validation:XValidation:rule="!(has(self.remoteJWKS) && has(self.localJWKS))", message="remoteJWKS and localJWKS cannot both be specified."
type JWKS struct {
	// RemoteJWKS defines how to fetch and cache JSON Web Key Sets (JWKS) from a remote
	// HTTP/HTTPS endpoint.
	//
	// +optional
	RemoteJWKS *egv1a1.RemoteJWKS `json:"remoteJWKS,omitempty"`

	// LocalJWKS defines how to get the JSON Web Key Sets (JWKS) from a local source.
	//
	// +optional
	LocalJWKS *egv1a1.LocalJWKS `json:"localJWKS,omitempty"`
}

// ProtectedResourceMetadata represents the Protected Resource Metadata	of the MCP server as per RFC 9728.
//
// References:
// * https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization#authorization-server-location
// * https://datatracker.ietf.org/doc/html/rfc9728#name-protected-resource-metadata
type ProtectedResourceMetadata struct {
	// Resource is the identifier of the protected resource.
	// This should match the MCPRoute's URL. For example, if the MCPRoute's URL is
	// "https://api.example.com/mcp", the Resource should be "https://api.example.com/mcp".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Format=uri
	Resource string `json:"resource"`

	// ResourceName is a human-readable name for the protected resource.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ResourceName *string `json:"resourceName,omitempty"`

	// ScopesSupported is a list of OAuth 2.0 scopes that the resource server supports.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=32
	// +optional
	ScopesSupported []string `json:"scopesSupported,omitempty"`

	// ResourceSigningAlgValuesSupported is a list of JWS signing algorithms supported by the resource server.
	// These algorithms are used in the "alg" field of the JOSE header in signed tokens.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +optional
	ResourceSigningAlgValuesSupported []string `json:"resourceSigningAlgValuesSupported,omitempty"`

	// ResourceDocumentation is a URL that provides human-readable documentation for the resource.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=uri
	// +optional
	ResourceDocumentation *string `json:"resourceDocumentation,omitempty"`

	// ResourcePolicyURI is a URL that points to the resource server's policy document.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=uri
	// +optional
	ResourcePolicyURI *string `json:"resourcePolicyUri,omitempty"`
}
