// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// AIGatewayRoute combines multiple AIServiceBackends and attaching them to Gateway(s) resources.
//
// This serves as a way to define a "unified" AI API for a Gateway which allows downstream
// clients to use a single schema API to interact with multiple AI backends.
//
// The schema field is used to determine the structure of the requests that the Gateway will
// receive. And then the Gateway will route the traffic to the appropriate AIServiceBackend based
// on the output schema of the AIServiceBackend while doing the other necessary jobs like
// upstream authentication, rate limit, etc.
//
// Envoy AI Gateway will generate the following k8s resources corresponding to the AIGatewayRoute:
//
//   - HTTPRoute of the Gateway API as a top-level resource to bind all backends.
//     The name of the HTTPRoute is the same as the AIGatewayRoute.
//   - EnvoyExtensionPolicy of the Envoy Gateway API to attach the AI Gateway filter into the target Gateways.
//     This will be created per Gateway, and its name is `ai-eg-eep-${gateway-name}`.
//   - HTTPRouteFilter of the Envoy Gateway API per namespace for automatic hostname rewrite.
//     The name of the HTTPRouteFilter is `ai-eg-host-rewrite`.
//
// All of these resources are created in the same namespace as the AIGatewayRoute. Note that this is the implementation
// detail subject to change. If you want to customize the default behavior of the Envoy AI Gateway, you can use these
// resources as a reference and create your own resources. Alternatively, you can use EnvoyPatchPolicy API of the Envoy
// Gateway to patch the generated resources. For example, you can configure the retry fallback behavior by attaching
// BackendTrafficPolicy API of Envoy Gateway to the generated HTTPRoute.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[-1:].type`
type AIGatewayRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the details of the AIGatewayRoute.
	Spec AIGatewayRouteSpec `json:"spec,omitempty"`
	// Status defines the status details of the AIGatewayRoute.
	Status AIGatewayRouteStatus `json:"status,omitempty"`
}

// AIGatewayRouteList contains a list of AIGatewayRoute.
//
// +kubebuilder:object:root=true
type AIGatewayRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIGatewayRoute `json:"items"`
}

// AIGatewayRouteSpec details the AIGatewayRoute configuration.
type AIGatewayRouteSpec struct {
	// TargetRefs are the names of the Gateway resources this AIGatewayRoute is being attached to.
	//
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=128
	TargetRefs []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`
	// APISchema specifies the API schema of the input that the target Gateway(s) will receive.
	// Based on this schema, the ai-gateway will perform the necessary transformation to the
	// output schema specified in the selected AIServiceBackend during the routing process.
	//
	// Currently, the only supported schema is OpenAI as the input schema.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self.name == 'OpenAI'"
	APISchema VersionedAPISchema `json:"schema"`
	// Rules is the list of AIGatewayRouteRule that this AIGatewayRoute will match the traffic to.
	// Each rule is a subset of the HTTPRoute in the Gateway API (https://gateway-api.sigs.k8s.io/api-types/httproute/).
	//
	// AI Gateway controller will generate a HTTPRoute based on the configuration given here with the additional
	// modifications to achieve the necessary jobs, notably inserting the AI Gateway filter responsible for
	// the transformation of the request and response, etc.
	//
	// In the matching conditions in the AIGatewayRouteRule, `x-ai-eg-model` header is available
	// if we want to describe the routing behavior based on the model name. The model name is extracted
	// from the request content before the routing decision.
	//
	// How multiple rules are matched is the same as the Gateway API. See for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPRoute
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxItems=128
	Rules []AIGatewayRouteRule `json:"rules"`

	// FilterConfig is the configuration for the AI Gateway filter inserted in the generated HTTPRoute.
	//
	// An AI Gateway filter is responsible for the transformation of the request and response
	// as well as the routing behavior based on the model name extracted from the request content, etc.
	//
	// Currently, the filter is only implemented as an external processor filter, which might be
	// extended to other types of filters in the future. See https://github.com/envoyproxy/ai-gateway/issues/90
	FilterConfig *AIGatewayFilterConfig `json:"filterConfig,omitempty"`

	// LLMRequestCosts specifies how to capture the cost of the LLM-related request, notably the token usage.
	// The AI Gateway filter will capture each specified number and store it in the Envoy's dynamic
	// metadata per HTTP request. The namespaced key is "io.envoy.ai_gateway",
	//
	// For example, let's say we have the following LLMRequestCosts configuration:
	// ```yaml
	//	llmRequestCosts:
	//	- metadataKey: llm_input_token
	//	  type: InputToken
	//	- metadataKey: llm_output_token
	//	  type: OutputToken
	//	- metadataKey: llm_total_token
	//	  type: TotalToken
	// ```
	// Then, with the following BackendTrafficPolicy of Envoy Gateway, you can have three
	// rate limit buckets for each unique x-user-id header value. One bucket is for the input token,
	// the other is for the output token, and the last one is for the total token.
	// Each bucket will be reduced by the corresponding token usage captured by the AI Gateway filter.
	//
	// ```yaml
	//	apiVersion: gateway.envoyproxy.io/v1alpha1
	//	kind: BackendTrafficPolicy
	//	metadata:
	//	  name: some-example-token-rate-limit
	//	  namespace: default
	//	spec:
	//	  targetRefs:
	//	  - group: gateway.networking.k8s.io
	//	     kind: HTTPRoute
	//	     name: usage-rate-limit
	//	  rateLimit:
	//	    type: Global
	//	    global:
	//	      rules:
	//	        - clientSelectors:
	//	            # Do the rate limiting based on the x-user-id header.
	//	            - headers:
	//	                - name: x-user-id
	//	                  type: Distinct
	//	          limit:
	//	            # Configures the number of "tokens" allowed per hour.
	//	            requests: 10000
	//	            unit: Hour
	//	          cost:
	//	            request:
	//	              from: Number
	//	              # Setting the request cost to zero allows to only check the rate limit budget,
	//	              # and not consume the budget on the request path.
	//	              number: 0
	//	            # This specifies the cost of the response retrieved from the dynamic metadata set by the AI Gateway filter.
	//	            # The extracted value will be used to consume the rate limit budget, and subsequent requests will be rate limited
	//	            # if the budget is exhausted.
	//	            response:
	//	              from: Metadata
	//	              metadata:
	//	                namespace: io.envoy.ai_gateway
	//	                key: llm_input_token
	//	        - clientSelectors:
	//	            - headers:
	//	                - name: x-user-id
	//	                  type: Distinct
	//	          limit:
	//	            requests: 10000
	//	            unit: Hour
	//	          cost:
	//	            request:
	//	              from: Number
	//	              number: 0
	//	            response:
	//	              from: Metadata
	//	              metadata:
	//	                namespace: io.envoy.ai_gateway
	//	                key: llm_output_token
	//	        - clientSelectors:
	//	            - headers:
	//	                - name: x-user-id
	//	                  type: Distinct
	//	          limit:
	//	            requests: 10000
	//	            unit: Hour
	//	          cost:
	//	            request:
	//	              from: Number
	//	              number: 0
	//	            response:
	//	              from: Metadata
	//	              metadata:
	//	                namespace: io.envoy.ai_gateway
	//	                key: llm_total_token
	// ```
	//
	// Note that when multiple AIGatewayRoute resources are attached to the same Gateway, and
	// different costs are configured for the same metadata key, the ai-gateway will pick one of them
	// to configure the metadata key in the generated HTTPRoute, and ignore the rest.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=36
	LLMRequestCosts []LLMRequestCost `json:"llmRequestCosts,omitempty"`
}

// AIGatewayRouteRule is a rule that defines the routing behavior of the AIGatewayRoute.
type AIGatewayRouteRule struct {
	// BackendRefs is the list of AIServiceBackend that this rule will route the traffic to.
	// Each backend can have a weight that determines the traffic distribution.
	//
	// The namespace of each backend is "local", i.e. the same namespace as the AIGatewayRoute.
	//
	// By configuring multiple backends, you can achieve the fallback behavior in the case of
	// the primary backend is not available combined with the BackendTrafficPolicy of Envoy Gateway.
	// Please refer to https://gateway.envoyproxy.io/docs/tasks/traffic/failover/ as well as
	// https://gateway.envoyproxy.io/docs/tasks/traffic/retry/.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=128
	BackendRefs []AIGatewayRouteRuleBackendRef `json:"backendRefs,omitempty"`

	// Matches is the list of AIGatewayRouteMatch that this rule will match the traffic to.
	// This is a subset of the HTTPRouteMatch in the Gateway API. See for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPRouteMatch
	//
	// +optional
	// +kubebuilder:validation:MaxItems=128
	Matches []AIGatewayRouteRuleMatch `json:"matches,omitempty"`

	// Timeouts defines the timeouts that can be configured for an HTTP request.
	//
	// +optional
	Timeouts *gwapiv1.HTTPRouteTimeouts `json:"timeouts,omitempty"`

	// ModelsOwnedBy represents the owner of the running models serving by the backends,
	// which will be exported as the field of "OwnedBy" in openai-compatible API "/models".
	//
	// This is used only when this rule contains "x-ai-eg-model" in its header matching
	// where the header value will be recognized as a "model" in "/models" endpoint.
	// All the matched models will share the same owner.
	//
	// Default to "Envoy AI Gateway" if not set.
	//
	// +optional
	// +kubebuilder:default="Envoy AI Gateway"
	ModelsOwnedBy *string `json:"modelsOwnedBy,omitempty"`

	// ModelsCreatedAt represents the creation timestamp of the running models serving by the backends,
	// which will be exported as the field of "Created" in openai-compatible API "/models".
	// It follows the format of RFC 3339, for example "2024-05-21T10:00:00Z".
	//
	// This is used only when this rule contains "x-ai-eg-model" in its header matching
	// where the header value will be recognized as a "model" in "/models" endpoint.
	// All the matched models will share the same creation time.
	//
	// Default to the creation timestamp of the AIGatewayRoute if not set.
	//
	// +optional
	// +kubebuilder:validation:Format=date-time
	ModelsCreatedAt *metav1.Time `json:"modelsCreatedAt,omitempty"`
}

// AIGatewayRouteRuleBackendRef is a reference to a backend with a weight.
type AIGatewayRouteRuleBackendRef struct {
	// Name is the name of the AIServiceBackend.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Name of the model in the backend. If provided this will override the name provided in the request.
	ModelNameOverride string `json:"modelNameOverride,omitempty"`

	// Weight is the weight of the AIServiceBackend. This is exactly the same as the weight in
	// the BackendRef in the Gateway API. See for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.BackendRef
	//
	// Default is 1.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	Weight *int32 `json:"weight,omitempty"`
	// Priority is the priority of the AIServiceBackend. This sets the priority on the underlying endpoints.
	// See: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/load_balancing/priority
	// Note: This will override the `faillback` property of the underlying Envoy Gateway Backend
	//
	// Default is 0.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority *uint32 `json:"priority,omitempty"`
}

type AIGatewayRouteRuleMatch struct {
	// Headers specifies HTTP request header matchers. See HeaderMatch in the Gateway API for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPHeaderMatch
	//
	// Currently, only the exact header matching is supported.
	//
	// +listType=map
	// +listMapKey=name
	// +optional
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self.all(match, match.type != 'RegularExpression')", message="currently only exact match is supported"
	Headers []gwapiv1.HTTPHeaderMatch `json:"headers,omitempty"`
}

type AIGatewayFilterConfig struct {
	// Type specifies the type of the filter configuration.
	//
	// Currently, only ExternalProcessor is supported, and default is ExternalProcessor.
	//
	// +kubebuilder:default=ExternalProcessor
	Type AIGatewayFilterConfigType `json:"type"`

	// ExternalProcessor is the configuration for the external processor filter.
	// This is optional, and if not set, the default values of Deployment spec will be used.
	//
	// +optional
	ExternalProcessor *AIGatewayFilterConfigExternalProcessor `json:"externalProcessor,omitempty"`
}

// AIGatewayFilterConfigType specifies the type of the filter configuration.
//
// +kubebuilder:validation:Enum=ExternalProcessor;DynamicModule
type AIGatewayFilterConfigType string

const (
	AIGatewayFilterConfigTypeExternalProcessor AIGatewayFilterConfigType = "ExternalProcessor"
	AIGatewayFilterConfigTypeDynamicModule     AIGatewayFilterConfigType = "DynamicModule" // Reserved for https://github.com/envoyproxy/ai-gateway/issues/90
)

type AIGatewayFilterConfigExternalProcessor struct {
	// Resources required by the external processor container.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
	//
	// Note: when multiple AIGatewayRoute resources are attached to the same Gateway, and each
	// AIGatewayRoute has a different resource configuration, the ai-gateway will pick one of them
	// to configure the resource requirements of the external processor container.
	//
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// AIServiceBackend is a resource that represents a single backend for AIGatewayRoute.
// A backend is a service that handles traffic with a concrete API specification.
//
// A AIServiceBackend is "attached" to a Backend which is either a k8s Service or a Backend resource of the Envoy Gateway.
//
// When a backend with an attached AIServiceBackend is used as a routing target in the AIGatewayRoute (more precisely, the
// HTTPRouteSpec defined in the AIGatewayRoute), the ai-gateway will generate the necessary configuration to do
// the backend specific logic in the final HTTPRoute.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[-1:].type`
type AIServiceBackend struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the details of AIServiceBackend.
	Spec AIServiceBackendSpec `json:"spec,omitempty"`
	// Status defines the status details of the AIServiceBackend.
	Status AIServiceBackendStatus `json:"status,omitempty"`
}

// AIServiceBackendList contains a list of AIServiceBackends.
//
// +kubebuilder:object:root=true
type AIServiceBackendList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIServiceBackend `json:"items"`
}

// AIServiceBackendSpec details the AIServiceBackend configuration.
type AIServiceBackendSpec struct {
	// APISchema specifies the API schema of the output format of requests from
	// Envoy that this AIServiceBackend can accept as incoming requests.
	// Based on this schema, the ai-gateway will perform the necessary transformation for
	// the pair of AIGatewayRouteSpec.APISchema and AIServiceBackendSpec.APISchema.
	//
	// This is required to be set.
	//
	// +kubebuilder:validation:Required
	APISchema VersionedAPISchema `json:"schema"`
	// BackendRef is the reference to the Backend resource that this AIServiceBackend corresponds to.
	//
	// A backend must be a Backend resource of Envoy Gateway. Note that k8s Service will be supported
	// as a backend in the future.
	//
	// This is required to be set.
	//
	// +kubebuilder:validation:Required
	BackendRef gwapiv1.BackendObjectReference `json:"backendRef"`

	// BackendSecurityPolicyRef is the name of the BackendSecurityPolicy resources this backend
	// is being attached to.
	//
	// +optional
	BackendSecurityPolicyRef *gwapiv1.LocalObjectReference `json:"backendSecurityPolicyRef,omitempty"`

	// TODO: maybe add backend-level LLMRequestCost configuration that overrides the AIGatewayRoute-level LLMRequestCost.
	// 	That may be useful for the backend that has a different cost calculation logic.
}

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

// BackendSecurityPolicyType specifies the type of auth mechanism used to access a backend.
type BackendSecurityPolicyType string

const (
	BackendSecurityPolicyTypeAPIKey           BackendSecurityPolicyType = "APIKey"
	BackendSecurityPolicyTypeAWSCredentials   BackendSecurityPolicyType = "AWSCredentials"
	BackendSecurityPolicyTypeAzureCredentials BackendSecurityPolicyType = "AzureCredentials"
	BackendSecurityPolicyTypeGCPCredentials   BackendSecurityPolicyType = "GCPCredentials"
)

// BackendSecurityPolicy specifies configuration for authentication and authorization rules on the traffic
// exiting the gateway to the backend.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[-1:].type`
type BackendSecurityPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackendSecurityPolicySpec `json:"spec,omitempty"`
	// Status defines the status details of the BackendSecurityPolicy.
	Status BackendSecurityPolicyStatus `json:"status,omitempty"`
}

// BackendSecurityPolicySpec specifies authentication rules on access the provider from the Gateway.
// Only one mechanism to access a backend(s) can be specified.
//
// Only one type of BackendSecurityPolicy can be defined.
// +kubebuilder:validation:MaxProperties=2
// +kubebuilder:validation:XValidation:rule="self.type == 'APIKey' ? (has(self.apiKey) && !has(self.awsCredentials) && !has(self.azureCredentials) && !has(self.gcpCredentials)) : true",message="When type is APIKey, only apiKey field should be set"
// +kubebuilder:validation:XValidation:rule="self.type == 'AWSCredentials' ? (has(self.awsCredentials) && !has(self.apiKey) && !has(self.azureCredentials) && !has(self.gcpCredentials)) : true",message="When type is AWSCredentials, only awsCredentials field should be set"
// +kubebuilder:validation:XValidation:rule="self.type == 'AzureCredentials' ? (has(self.azureCredentials) && !has(self.apiKey) && !has(self.awsCredentials) && !has(self.gcpCredentials)) : true",message="When type is AzureCredentials, only azureCredentials field should be set"
// +kubebuilder:validation:XValidation:rule="self.type == 'GCPCredentials' ? (has(self.gcpCredentials) && !has(self.apiKey) && !has(self.awsCredentials) && !has(self.azureCredentials)) : true",message="When type is GCPCredentials, only gcpCredentials field should be set"
type BackendSecurityPolicySpec struct {
	// Type specifies the type of the backend security policy.
	//
	// +kubebuilder:validation:Enum=APIKey;AWSCredentials;AzureCredentials;GCPCredentials
	Type BackendSecurityPolicyType `json:"type"`

	// APIKey is a mechanism to access a backend(s). The API key will be injected into the Authorization header.
	//
	// +optional
	APIKey *BackendSecurityPolicyAPIKey `json:"apiKey,omitempty"`

	// AWSCredentials is a mechanism to access a backend(s). AWS specific logic will be applied.
	//
	// +optional
	AWSCredentials *BackendSecurityPolicyAWSCredentials `json:"awsCredentials,omitempty"`

	// AzureCredentials is a mechanism to access a backend(s). Azure OpenAI specific logic will be applied.
	//
	// +optional
	AzureCredentials *BackendSecurityPolicyAzureCredentials `json:"azureCredentials,omitempty"`
	// GCPCredentials is a mechanism to access a backend(s). GCP specific logic will be applied.
	//
	// +optional
	GCPCredentials *BackendSecurityPolicyGCPCredentials `json:"gcpCredentials,omitempty"`
}

// BackendSecurityPolicyList contains a list of BackendSecurityPolicy
//
// +kubebuilder:object:root=true
type BackendSecurityPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackendSecurityPolicy `json:"items"`
}

// BackendSecurityPolicyAPIKey specifies the API key.
type BackendSecurityPolicyAPIKey struct {
	// SecretRef is the reference to the secret containing the API key.
	// ai-gateway must be given the permission to read this secret.
	// The key of the secret should be "apiKey".
	SecretRef *gwapiv1.SecretObjectReference `json:"secretRef"`
}

// BackendSecurityPolicyOIDC specifies OIDC related fields.
type BackendSecurityPolicyOIDC struct {
	// OIDC is used to obtain oidc tokens via an SSO server which will be used to exchange for provider credentials.
	//
	// +kubebuilder:validation:Required
	OIDC egv1a1.OIDC `json:"oidc"`

	// GrantType is the method application gets access token.
	//
	// +optional
	GrantType string `json:"grantType,omitempty"`

	// Aud defines the audience that this ID Token is intended for.
	//
	// +optional
	Aud string `json:"aud,omitempty"`
}

type GCPWorkLoadIdentityFederationConfig struct {
	// ProjectID is the GCP project ID.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ProjectID string `json:"projectID"`

	// WorkloadIdentityProvider is the external auth provider to be used to authenticate against GCP.
	// https://cloud.google.com/iam/docs/workload-identity-federation?hl=en
	// Currently only OIDC is supported.
	//
	// +kubebuilder:validation:Required
	WorkloadIdentityProvider GCPWorkloadIdentityProvider `json:"workloadIdentityProvider"`

	// WorkloadIdentityPoolName is the name of the workload identity pool defined in GCP.
	// https://cloud.google.com/iam/docs/workload-identity-federation?hl=en
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	WorkloadIdentityPoolName string `json:"workloadIdentityPoolName"`

	// ServiceAccountImpersonation is the service account impersonation configuration.
	// This is used to impersonate a service account when getting access token.
	//
	// +optional
	ServiceAccountImpersonation *GCPServiceAccountImpersonationConfig `json:"serviceAccountImpersonation,omitempty"`
}

// GCPWorkloadIdentityProvider specifies the external identity provider to be used to authenticate against GCP.
// The external identity provider can be AWS, Microsoft, etc but must be pre-registered in the GCP project
//
// https://cloud.google.com/iam/docs/workload-identity-federation
type GCPWorkloadIdentityProvider struct {
	// Name of the external identity provider as registered on Google Cloud Platform.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// OIDCProvider is the generic OIDCProvider fields.
	//
	// +kubebuilder:validation:Required
	OIDCProvider BackendSecurityPolicyOIDC `json:"OIDCProvider"`
}

type GCPServiceAccountImpersonationConfig struct {
	// ServiceAccountName is the name of the service account to impersonate.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ServiceAccountName string `json:"serviceAccountName"`
	// ServiceAccountProjectName is the project name in which the service account is registered.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ServiceAccountProjectName string `json:"serviceAccountProjectName"`
}

// BackendSecurityPolicyGCPCredentials contains the supported authentication mechanisms to access GCP.
type BackendSecurityPolicyGCPCredentials struct {
	// ProjectName is the GCP project name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ProjectName string `json:"projectName"`
	// Region is the GCP region associated with the policy.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`
	// WorkLoadIdentityFederationConfig is the configuration for the GCP Workload Identity Federation.
	//
	// +kubebuilder:validation:Required
	WorkLoadIdentityFederationConfig GCPWorkLoadIdentityFederationConfig `json:"workLoadIdentityFederationConfig"`
}

// BackendSecurityPolicyAzureCredentials contains the supported authentication mechanisms to access Azure.
// Only one of ClientSecretRef or OIDCExchangeToken must be specified. Credentials will not be generated if
// neither are set.
//
// +kubebuilder:validation:XValidation:rule="(has(self.clientSecretRef) && !has(self.oidcExchangeToken)) || (!has(self.clientSecretRef) && has(self.oidcExchangeToken))",message="Exactly one of clientSecretRef or oidcExchangeToken must be specified"
type BackendSecurityPolicyAzureCredentials struct {
	// ClientID is a unique identifier for an application in Azure.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ClientID string `json:"clientID"`

	// TenantId is a unique identifier for an Azure Active Directory instance.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	TenantID string `json:"tenantID"`

	// ClientSecretRef is the reference to the secret containing the Azure client secret.
	// ai-gateway must be given the permission to read this secret.
	// The key of secret should be "client-secret".
	//
	// +optional
	ClientSecretRef *gwapiv1.SecretObjectReference `json:"clientSecretRef,omitempty"`

	// OIDCExchangeToken specifies the oidc configurations used to obtain an oidc token. The oidc token will be
	// used to obtain temporary credentials to access Azure.
	//
	// +optional
	OIDCExchangeToken *AzureOIDCExchangeToken `json:"oidcExchangeToken,omitempty"`
}

// AzureOIDCExchangeToken specifies credentials to obtain oidc token from a sso server.
// For Azure, the controller will query Azure Entra ID to get an Azure Access Token,
// and store them in a secret.
type AzureOIDCExchangeToken struct {
	// BackendSecurityPolicyOIDC is the generic OIDC fields.
	BackendSecurityPolicyOIDC `json:",inline"`
}

// BackendSecurityPolicyAWSCredentials contains the supported authentication mechanisms to access aws.
type BackendSecurityPolicyAWSCredentials struct {
	// Region specifies the AWS region associated with the policy.
	//
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// CredentialsFile specifies the credentials file to use for the AWS provider.
	//
	// +optional
	CredentialsFile *AWSCredentialsFile `json:"credentialsFile,omitempty"`

	// OIDCExchangeToken specifies the oidc configurations used to obtain an oidc token. The oidc token will be
	// used to obtain temporary credentials to access AWS.
	//
	// +optional
	OIDCExchangeToken *AWSOIDCExchangeToken `json:"oidcExchangeToken,omitempty"`
}

// AWSCredentialsFile specifies the credentials file to use for the AWS provider.
// Envoy reads the secret file, and the profile to use is specified by the Profile field.
type AWSCredentialsFile struct {
	// SecretRef is the reference to the credential file.
	//
	// The secret should contain the AWS credentials file keyed on "credentials".
	SecretRef *gwapiv1.SecretObjectReference `json:"secretRef"`

	// Profile is the profile to use in the credentials file.
	//
	// +kubebuilder:default=default
	Profile string `json:"profile,omitempty"`
}

// AWSOIDCExchangeToken specifies credentials to obtain oidc token from a sso server.
// For AWS, the controller will query STS to obtain AWS AccessKeyId, SecretAccessKey, and SessionToken,
// and store them in a temporary credentials file.
type AWSOIDCExchangeToken struct {
	// BackendSecurityPolicyOIDC is the generic OIDC fields.
	BackendSecurityPolicyOIDC `json:",inline"`

	// AwsRoleArn is the AWS IAM Role with the permission to use specific resources in AWS account
	// which maps to the temporary AWS security credentials exchanged using the authentication token issued by OIDC provider.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	AwsRoleArn string `json:"awsRoleArn"`
}

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
