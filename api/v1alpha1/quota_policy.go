// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// QuotaPolicy specifies token quota configuration for inference services.
// Providing a list of backends in the AIGatewayRouteRule allows failover to a different service
// if token quota for a service had been exceeded.
//
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[-1:].type`
// +kubebuilder:metadata:labels="gateway.networking.k8s.io/policy=direct"
type QuotaPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              QuotaPolicySpec `json:"spec,omitempty"`
	// Status defines the status details of the QuotaPolicy.
	Status QuotaPolicyStatus `json:"status,omitempty"`
}

// QuotaPolicySpec specifies rules for computing token based costs of requests.
type QuotaPolicySpec struct {
	// TargetRefs are the names of the AIServiceBackend resources this QuotaPolicy is being attached to.
	// Attaching multiple QuotaPolicies to the same AIServiceBackend is invalid and will result in an error
	// during the reconciliation of AIServiceBackend.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self.all(ref, ref.group == 'aigateway.envoyproxy.io' && ref.kind == 'AIServiceBackend')", message="targetRefs must reference AIServiceBackend resources"
	TargetRefs []gwapiv1a2.LocalPolicyTargetReference `json:"targetRefs,omitempty"`

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
	//	- metadataKey: llm_cached_input_token
	//	  type: CachedInputToken
	// ```
	// The token costs computed using this policy are debited from the quota specificed by the BackendTrafficPolicy
	// attached to the same AIServiceBackend. In the BackendTrafficPolicy you can have multiple buckets
	// tracking quot ausage for each distinct x-user-id header value. Quota can be specified in terms of total (input + output)
	// token counts or just the output or onput token counts.
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

// QuotaPolicyList contains a list of QuotaPolicy
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type QuotaPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []QuotaPolicy `json:"items"`
}
