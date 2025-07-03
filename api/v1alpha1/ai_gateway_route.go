// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
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
	// If this field is not set, or the timeout.requestTimeout is nil, Envoy AI Gateway defaults to
	// set 60s for the request timeout as opposed to 15s of the Envoy Gateway's default value.
	//
	// For streaming responses (like chat completions with stream=true), consider setting
	// longer timeouts as the response may take time until the completion.
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
