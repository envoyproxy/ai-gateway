// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

// VersionedAPISchema defines the API schema of either AIGatewayRoute (the input) or AIServiceBackend (the output).
//
// This allows the ai-gateway to understand the input and perform the necessary transformation
// depending on the API schema pair (input, output).
//
// Note that this is vendor specific, and the stability of the API schema is not guaranteed by
// the ai-gateway, but by the vendor via proper versioning.
type VersionedAPISchema struct {
	// Name is the name of the API schema of the AIGatewayRoute or AIServiceBackend.
	//
	// +kubebuilder:validation:Enum=OpenAI;AWSBedrock;AzureOpenAI;GCPVertexAI;GCPAnthropic
	Name APISchema `json:"name"`

	// Version is the version of the API schema.
	//
	// When the name is set to "OpenAI", this equals to the prefix of the OpenAI API endpoints. This defaults to "v1"
	// if not set or empty string. For example, "chat completions" API endpoint will be "/v1/chat/completions"
	// if the version is set to "v1".
	//
	// This is especially useful when routing to the backend that has an OpenAI compatible API but has a different
	// versioning scheme. For example, Gemini OpenAI compatible API (https://ai.google.dev/gemini-api/docs/openai) uses
	// "/v1beta/openai" version prefix. Another example is that Cohere AI (https://docs.cohere.com/v2/docs/compatibility-api)
	// uses "/compatibility/v1" version prefix. On the other hand, DeepSeek (https://api-docs.deepseek.com/) doesn't
	// use version prefix, so the version can be set to an empty string.
	//
	// When the name is set to AzureOpenAI, this version maps to "API Version" in the
	// Azure OpenAI API documentation (https://learn.microsoft.com/en-us/azure/ai-services/openai/reference#rest-api-versioning).
	Version *string `json:"version,omitempty"`
}

// APISchema defines the API schema.
type APISchema string

const (
	// APISchemaOpenAI is the OpenAI schema.
	//
	// https://github.com/openai/openai-openapi
	APISchemaOpenAI APISchema = "OpenAI"
	// APISchemaAWSBedrock is the AWS Bedrock schema.
	//
	// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_Operations_Amazon_Bedrock_Runtime.html
	APISchemaAWSBedrock APISchema = "AWSBedrock"
	// APISchemaAzureOpenAI APISchemaAzure is the Azure OpenAI schema.
	//
	// https://learn.microsoft.com/en-us/azure/ai-services/openai/reference#api-specs
	APISchemaAzureOpenAI APISchema = "AzureOpenAI"
	// APISchemaGCPVertexAI is the schema followed by Gemini models hosted on GCP's Vertex AI platform.
	// Note: Using this schema requires a BackendSecurityPolicy to be configured and attached,
	// as the transformation will use the gcp-region and project-name from the BackendSecurityPolicy.
	//
	// https://cloud.google.com/vertex-ai/docs/reference/rest/v1/projects.locations.endpoints/generateContent?hl=en
	APISchemaGCPVertexAI APISchema = "GCPVertexAI"
	// APISchemaGCPAnthropic is the schema followed by Anthropic models hosted on GCP's Vertex AI platform.
	// This is majorly the Anthropic API with some GCP specific parameters as described in below URL.
	//
	// https://docs.anthropic.com/en/api/claude-on-vertex-ai
	APISchemaGCPAnthropic APISchema = "GCPAnthropic"
)

const (
	// AIModelHeaderKey is the header key whose value is extracted from the request by the ai-gateway.
	// This can be used to describe the routing behavior in HTTPRoute referenced by AIGatewayRoute.
	AIModelHeaderKey = "x-ai-eg-model"
)

// LLMRequestCost configures each request cost.
type LLMRequestCost struct {
	// MetadataKey is the key of the metadata to store this cost of the request.
	//
	// +kubebuilder:validation:Required
	MetadataKey string `json:"metadataKey"`
	// Type specifies the type of the request cost. The default is "OutputToken",
	// and it uses "output token" as the cost. The other types are "InputToken", "TotalToken",
	// and "CEL".
	//
	// +kubebuilder:validation:Enum=OutputToken;InputToken;TotalToken;CEL
	Type LLMRequestCostType `json:"type"`
	// CEL is the CEL expression to calculate the cost of the request.
	// The CEL expression must return a signed or unsigned integer. If the
	// return value is negative, it will be error.
	//
	// The expression can use the following variables:
	//
	//	* model: the model name extracted from the request content. Type: string.
	//	* backend: the backend name in the form of "name.namespace". Type: string.
	//	* input_tokens: the number of input tokens. Type: unsigned integer.
	//	* output_tokens: the number of output tokens. Type: unsigned integer.
	//	* total_tokens: the total number of tokens. Type: unsigned integer.
	//
	// For example, the following expressions are valid:
	//
	// 	* "model == 'llama' ?  input_tokens + output_token * 0.5 : total_tokens"
	//	* "backend == 'foo.default' ?  input_tokens + output_tokens : total_tokens"
	//	* "input_tokens + output_tokens + total_tokens"
	//	* "input_tokens * output_tokens"
	//
	// +optional
	CEL *string `json:"cel,omitempty"`
}

// LLMRequestCostType specifies the type of the LLMRequestCost.
type LLMRequestCostType string

const (
	// LLMRequestCostTypeInputToken is the cost type of the input token.
	LLMRequestCostTypeInputToken LLMRequestCostType = "InputToken"
	// LLMRequestCostTypeOutputToken is the cost type of the output token.
	LLMRequestCostTypeOutputToken LLMRequestCostType = "OutputToken"
	// LLMRequestCostTypeTotalToken is the cost type of the total token.
	LLMRequestCostTypeTotalToken LLMRequestCostType = "TotalToken"
	// LLMRequestCostTypeCEL is for calculating the cost using the CEL expression.
	LLMRequestCostTypeCEL LLMRequestCostType = "CEL"
)

const (
	// AIGatewayFilterMetadataNamespace is the namespace for the ai-gateway filter metadata.
	AIGatewayFilterMetadataNamespace = "io.envoy.ai_gateway"
)
