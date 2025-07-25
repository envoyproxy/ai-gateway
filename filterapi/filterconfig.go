// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package filterapi provides the configuration for the AI Gateway-implemented filter
// which is currently an external processor (See https://github.com/envoyproxy/ai-gateway/issues/90).
//
// This is a public package so that the filter can be testable without
// depending on the Envoy Gateway as well as it can be used outside the Envoy AI Gateway.
//
// This configuration must be decoupled from the Envoy Gateway types as well as its implementation
// details. Also, the configuration must not be tied with k8s so it can be tested and iterated
// without the need for the k8s cluster.
package filterapi

import (
	"os"
	"time"

	"k8s.io/apimachinery/pkg/util/yaml"
)

// DefaultConfig is the default configuration that can be used as a
// fallback when the configuration is not explicitly provided.
const DefaultConfig = `
schema:
  name: OpenAI
modelNameHeaderKey: x-ai-eg-model
`

// Config is the configuration for the Envoy AI Gateway filter.
type Config struct {
	// UUID is the unique identifier of the filter configuration assigned by the AI Gateway when the configuration is updated.
	UUID string `json:"uuid,omitempty"`
	// MetadataNamespace is the namespace of the dynamic metadata to be used by the filter.
	MetadataNamespace string `json:"metadataNamespace"`
	// LLMRequestCost configures the cost of each LLM-related request. Optional. If this is provided, the filter will populate
	// the "calculated" cost in the filter metadata at the end of the response body processing.
	LLMRequestCosts []LLMRequestCost `json:"llmRequestCosts,omitempty"`
	// InputSchema specifies the API schema of the input format of requests to the filter.
	Schema VersionedAPISchema `json:"schema"`
	// ModelNameHeaderKey is the header key to be populated with the model name by the filter.
	ModelNameHeaderKey string `json:"modelNameHeaderKey"`
	// Backends is the list of backends that this listener can route to.
	Backends []Backend `json:"backends,omitempty"`
	// Models is the list of models that this route is aware of. Used to populate the "/models" endpoint in OpenAI-compatible APIs.
	Models []Model `json:"models,omitempty"`
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
// which is introduced in Envoy 1.33 (to be released soon as of writing).
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
)

// RouteRuleName is the name of the route rule.
type RouteRuleName string

// Backend corresponds to AIGatewayRouteRuleBackendRef in api/v1alpha1/api.go
// besides that this abstracts the concept of a backend at Envoy Gateway level to a simple name.
type Backend struct {
	// Name of the backend including the route name as well as the route rule index.
	Name string `json:"name"`
	// Name of the model in the backend. If provided this will override the name provided in the request.
	ModelNameOverride string `json:"modelNameOverride"`
	// Schema specifies the API schema of the output format of requests from.
	Schema VersionedAPISchema `json:"schema"`
	// Auth is the authn/z configuration for the backend. Optional.
	Auth *BackendAuth `json:"auth,omitempty"`
}

// BackendAuth corresponds partially to BackendSecurityPolicy in api/v1alpha1/api.go.
type BackendAuth struct {
	// APIKey is a location of the api key secret file.
	APIKey *APIKeyAuth `json:"apiKey,omitempty"`
	// AWSAuth specifies the location of the AWS credential file and region.
	AWSAuth *AWSAuth `json:"aws,omitempty"`
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

// AzureAuth defines the file containing azure access token that will be mounted to the external proc.
type AzureAuth struct {
	// AccessToken is the access token as a literal string.
	AccessToken string `json:"accessToken"`
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
