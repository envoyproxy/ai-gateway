// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package filterapi provides the configuration for the AI Gateway-implemented filter
// which is currently an external processor (See https://github.com/envoyproxy/ai-gateway/issues/90).
//
// This configuration must be decoupled from the Envoy Gateway types as well as its implementation
// details. Also, the configuration must not be tied with k8s so it can be tested and iterated
// without the need for the k8s cluster.
package filterapi

import (
	"os"
	"time"

	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// DefaultConfig is the default configuration that can be used as a
// fallback when the configuration is not explicitly provided.
var DefaultConfig = ``

// Config is the configuration for the Envoy AI Gateway filter.
type Config struct {
	// UUID is the unique identifier of the filter configuration assigned by the AI Gateway when the configuration is updated.
	UUID string `json:"uuid,omitempty"`
	// LLMRequestCost configures the cost of each LLM-related request. Optional. If this is provided, the filter will populate
	// the "calculated" cost in the filter metadata at the end of the response body processing.
	LLMRequestCosts []LLMRequestCost `json:"llmRequestCosts,omitempty"`
	// Backends is the list of backends that this listener can route to.
	Backends []Backend `json:"backends,omitempty"`
	// Models is the list of models that this route is aware of. Used to populate the "/models" endpoint in OpenAI-compatible APIs.
	Models []Model `json:"models,omitempty"`
	// MCPConfig is the configuration for the MCPRoute implementations.
	MCPConfig *MCPConfig `json:"mcpConfig,omitempty"`
}

// Model corresponds to the OpenAI model object in the OpenAI-compatible APIs
// and is used to populate the "/models" endpoint in OpenAI-compatible APIs.
type Model struct {
	// Name will be exported as the field of "ID" in OpenAI-compatible APIs.
	Name string
	// ownedBy will be exported as the field of "OwnedBy" in OpenAI-compatible API "/models".
	OwnedBy string
	// createdAt will be exported as the field of "Created" in OpenAI-compatible API "/models".
	CreatedAt time.Time
}

// LLMRequestCost specifies "where" the request cost is stored in the filter metadata as well as
// "how" the cost is calculated. By default, the cost is retrieved from "output token" in the response body.
//
// This can be used to subtract the usage token from the usage quota in the rate limit filter when
// the request completes combined with `apply_on_stream_done` and `hits_addend` fields of
// the rate limit configuration https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/route/v3/route_components.proto#config-route-v3-ratelimit
// introduced in Envoy 1.33.
type LLMRequestCost struct {
	// MetadataKey is the key of the metadata storing the request cost.
	MetadataKey string `json:"metadataKey"`
	// Type is the kind of the request cost calculation.
	Type LLMRequestCostType `json:"type"`
	// CEL is the CEL expression to calculate the cost of the request.
	// This is not empty when the Type is LLMRequestCostTypeCEL.
	CEL string `json:"cel,omitempty"`
}

// LLMRequestCostType specifies the kind of the request cost calculation.
type LLMRequestCostType string

const (
	// LLMRequestCostTypeOutputToken specifies that the request cost is calculated from the output token.
	LLMRequestCostTypeOutputToken LLMRequestCostType = "OutputToken"
	// LLMRequestCostTypeInputToken specifies that the request cost is calculated from the input token.
	LLMRequestCostTypeInputToken LLMRequestCostType = "InputToken"
	// LLMRequestCostTypeCachedInputToken specifies that the request cost is calculated from the cached input token.
	LLMRequestCostTypeCachedInputToken LLMRequestCostType = "CachedInputToken"
	// LLMRequestCostTypeTotalToken specifies that the request cost is calculated from the total token.
	LLMRequestCostTypeTotalToken LLMRequestCostType = "TotalToken"
	// LLMRequestCostTypeCEL specifies that the request cost is calculated from the CEL expression.
	LLMRequestCostTypeCEL LLMRequestCostType = "CEL"
)

// VersionedAPISchema corresponds to VersionedAPISchema in api/v1alpha1/api.go.
type VersionedAPISchema struct {
	// Name is the name of the API schema.
	Name APISchemaName `json:"name"`
	// Version is the version of the API schema. Optional.
	Version string `json:"version,omitempty"`
}

// APISchemaName corresponds to APISchemaName in api/v1alpha1/api.go.
type APISchemaName string

const (
	// APISchemaOpenAI represents the standard OpenAI API schema.
	APISchemaOpenAI APISchemaName = "OpenAI"
	// APISchemaCohere represents the Cohere API schema.
	APISchemaCohere APISchemaName = "Cohere"
	// APISchemaAWSBedrock represents the AWS Bedrock API schema.
	APISchemaAWSBedrock APISchemaName = "AWSBedrock"
	// APISchemaAzureOpenAI represents the Azure OpenAI API schema.
	APISchemaAzureOpenAI APISchemaName = "AzureOpenAI"
	// APISchemaGCPVertexAI represents the Google Cloud Gemini API schema.
	// Used for Gemini models hosted on Google Cloud Vertex AI.
	APISchemaGCPVertexAI APISchemaName = "GCPVertexAI"
	// APISchemaGCPAnthropic represents the Google Cloud Anthropic API schema.
	// Used for Claude models hosted on Google Cloud Vertex AI.
	APISchemaGCPAnthropic APISchemaName = "GCPAnthropic"
	// APISchemaAnthropic represents the standard Anthropic API schema.
	APISchemaAnthropic APISchemaName = "Anthropic"
	// APISchemaAWSAnthropic represents the AWS Bedrock Anthropic API schema.
	// Used for Claude models hosted on AWS Bedrock using the native Anthropic Messages API.
	APISchemaAWSAnthropic APISchemaName = "AWSAnthropic"
)

// RouteRuleName is the name of the route rule.
type RouteRuleName string

// Backend corresponds to AIGatewayRouteRuleBackendRef in api/v1alpha1/api.go
// besides that this abstracts the concept of a backend at Envoy Gateway level to a simple name.
type Backend struct {
	// Name of the backend including the route name as well as the route rule index.
	Name              string                        `json:"name"`
	ModelNameOverride internalapi.ModelNameOverride `json:"modelNameOverride"`
	// Schema specifies the API schema of the output format of requests from.
	Schema VersionedAPISchema `json:"schema"`
	// Auth is the authn/z configuration for the backend. Optional.
	Auth *BackendAuth `json:"auth,omitempty"`
	// Sensitive Headers to be removed from the request before sending to the backend. Optional.
	HeaderMutation *HTTPHeaderMutation `json:"httpHeaderMutation,omitempty"`
	// Body mutations to be applied to the request before sending to the backend. Optional.
	BodyMutation *HTTPBodyMutation `json:"httpBodyMutation,omitempty"`
}

// BackendAuth corresponds partially to BackendSecurityPolicy in api/v1alpha1/api.go.
type BackendAuth struct {
	// APIKey is a location of the api key secret file.
	APIKey *APIKeyAuth `json:"apiKey,omitempty"`
	// AWSAuth specifies the location of the AWS credential file and region.
	AWSAuth *AWSAuth `json:"aws,omitempty"`
	// AzureAPIKey is the Azure OpenAI API key.
	AzureAPIKey *AzureAPIKeyAuth `json:"azureAPIKey,omitempty"`
	// AnthropicAPIKey is the Anthropic API key.
	AnthropicAPIKey *AnthropicAPIKeyAuth `json:"anthropicAPIKey,omitempty"`
	// AzureAuth specifies the location of Azure access token file.
	AzureAuth *AzureAuth `json:"azure,omitempty"`
	// GCPAuth specifies the location of GCP credential file.
	GCPAuth *GCPAuth `json:"gcp,omitempty"`
}

// AWSAuth defines the credentials needed to access AWS.
type AWSAuth struct {
	// CredentialFileLiteral is the literal string of the AWS credential file. E.g.
	// [default]\naws_access_key_id = <access-key-id>\naws_secret_access_key = <secret-access-key>\naws_session_token = <session-token>.
	CredentialFileLiteral string `json:"credentialFileLiteral,omitempty"`
	Region                string `json:"region"`
}

// APIKeyAuth defines the file that will be mounted to the external proc.
type APIKeyAuth struct {
	// Key is the API key as a literal string.
	Key string `json:"key"`
}

// AzureAPIKeyAuth defines the Azure OpenAI API key.
type AzureAPIKeyAuth struct {
	// Key is the Azure API key as a literal string.
	Key string `json:"key"`
}

// AnthropicAPIKeyAuth defines the Anthropic API key.
type AnthropicAPIKeyAuth struct {
	// Key is the Anthropic API key as a literal string.
	Key string `json:"key"`
}

// AzureAuth defines the file containing azure access token that will be mounted to the external proc.
type AzureAuth struct {
	// AccessToken is the access token as a literal string.
	// ignored if UseManagedIdentity is true.
	AccessToken string `json:"accessToken,omitempty"`
	// UseManagedIdentity enables Azure Managed Identity authentication.
	// Extproc will get token dynamically
	UseManagedIdentity bool `json:"useManagedIdentity,omitempty"`
	// ClientID is the Azure client ID for user-assigned managed identity.
	// Optional when using system-assigned managed identity or when provided via environment variables.
	ClientID string `json:"clientID,omitempty"`
	// TenantID is the Azure tenant ID.
	// Optional when provided via environment variables (e.g., AKS Workload Identity).
	TenantID string `json:"tenantID,omitempty"`
}

// GCPAuth defines the GCP authentication configuration used to access Google Cloud AI services.
type GCPAuth struct {
	// AccessToken is the access token as a literal string.
	// This token is obtained through GCP Workload Identity Federation and service account impersonation.
	// The token is automatically rotated by the BackendSecurityPolicy controller before expiration.
	AccessToken string `json:"accessToken"`
	// Region is the GCP region to use for the request.
	// This is used in URL path templates when making requests to GCP Vertex AI endpoints.
	// Examples: "us-central1", "europe-west4".
	Region string `json:"region"`
	// ProjectName is the GCP project name to use for the request.
	// This is used in URL path templates when making requests to GCP Vertex AI endpoints.
	// This should be the project where Vertex AI APIs are enabled.
	ProjectName string `json:"projectName"`
}

// HTTPHeaderMutation defines the mutation of HTTP headers that will be applied to the request
type HTTPHeaderMutation struct {
	// Set overwrites the request with the given header (name, value)
	// before the action.
	Set []HTTPHeader `json:"set,omitempty"`
	// Remove the given header(s) from the HTTP request before the action. The
	// value of Remove is a list of HTTP header names.
	// This is always ensured to be lower-cased like Envoy does internally.
	Remove []string `json:"remove,omitempty"`
}

// HTTPHeader represents an HTTP Header name and value as defined by RFC 7230.
type HTTPHeader struct {
	// Name is the name of the HTTP Header to be matched.
	// This is always ensured to be lower-cased like Envoy does internally.
	Name string `json:"name"`
	// Value is the value of HTTP Header to be matched.
	Value string `json:"value"`
}

// HTTPBodyMutation defines the mutation of HTTP request body JSON fields that will be applied to the request
type HTTPBodyMutation struct {
	// Set overwrites/adds the request body with the given JSON field (name, value)
	// before sending to the backend. Only top-level fields are currently supported.
	Set []HTTPBodyField `json:"set,omitempty"`
	// Remove the given JSON field(s) from the HTTP request body before sending to the backend.
	// The value of Remove is a list of top-level field names to remove.
	Remove []string `json:"remove,omitempty"`
}

// HTTPBodyField represents a JSON field name and value for body mutation
type HTTPBodyField struct {
	// Path is the top-level field name to set in the request body.
	// Examples: "service_tier", "max_tokens", "temperature"
	Path string `json:"path"`
	// Value is the JSON value to set at the specified field. This can be any valid JSON value:
	// string, number, boolean, object, array, or null.
	// The value will be parsed as JSON and inserted at the specified field.
	Value string `json:"value"`
}

// UnmarshalConfigYaml reads the file at the given path and unmarshals it into a Config struct.
func UnmarshalConfigYaml(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// MustLoadDefaultConfig loads the default configuration.
// This panics if the configuration fails to be loaded.
func MustLoadDefaultConfig() *Config {
	var cfg Config
	if err := yaml.Unmarshal([]byte(DefaultConfig), &cfg); err != nil {
		panic(err)
	}
	return &cfg
}
