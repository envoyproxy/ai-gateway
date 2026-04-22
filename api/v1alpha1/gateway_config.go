// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GatewayConfig provides configuration for the AI Gateway external processor
// container that is deployed alongside the Gateway.
//
// A GatewayConfig is referenced by a Gateway via the annotation
// "aigateway.envoyproxy.io/gateway-config". The GatewayConfig must be in the
// same namespace as the Gateway that references it.
//
// This allows gateway-level configuration of the external processor, including
// environment variables (e.g., for tracing configuration) and resource requirements.
//
// Multiple Gateways can reference the same GatewayConfig to share configuration.
//
// Environment Variable Precedence:
// When merging environment variables, the following precedence applies (highest to lowest):
//  1. GatewayConfig.Spec.ExtProc.Kubernetes.Env (this resource)
//  2. Global controller flags (extProcExtraEnvVars)
//
// If the same environment variable name exists in both sources, the GatewayConfig
// value takes precedence.
//
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=gwconfig
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[-1:].type`
// +kubebuilder:deprecatedversion:warning="aigateway.envoyproxy.io/v1alpha1 is deprecated; use aigateway.envoyproxy.io/v1beta1 instead"
type GatewayConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the configuration for the external processor.
	Spec GatewayConfigSpec `json:"spec,omitempty"`
	// Status defines the status of the GatewayConfig.
	Status GatewayConfigStatus `json:"status,omitempty"`
}

// GatewayConfigList contains a list of GatewayConfig.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type GatewayConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GatewayConfig `json:"items"`
}

// GatewayConfigSpec defines the configuration for the AI Gateway.
type GatewayConfigSpec struct {
	// ExtProc defines the configuration for the external processor container.
	//
	// +optional
	ExtProc *GatewayConfigExtProc `json:"extProc,omitempty"`

	// GlobalLLMRequestCosts defines default LLM request costs that apply to all
	// routes referencing this GatewayConfig. These costs can be overridden on a
	// per-route basis via AIGatewayRoute.Spec.LLMRequestCosts.
	//
	// When a request matches a route, the cost calculation proceeds as follows:
	//  1. If the route defines LLMRequestCosts with a matching metadataKey, use that.
	//  2. Otherwise, fall back to the global cost with that metadataKey (if defined here).
	//  3. If neither exists, the cost is not calculated for that metadataKey.
	//
	// This allows you to define common cost formulas once at the gateway level
	// (e.g., billing_charges = input_tokens + output_tokens) and only override
	// them in specific routes when needed (e.g., premium routes with different pricing).
	//
	// +optional
	// +listType=map
	// +listMapKey=metadataKey
	GlobalLLMRequestCosts []LLMRequestCost `json:"globalLLMRequestCosts,omitempty"`
}

// GatewayConfigExtProc holds runtime-specific configuration for the external processor.
type GatewayConfigExtProc struct {
	// Kubernetes defines the configuration for running the external processor as a Kubernetes container.
	//
	// +optional
	Kubernetes *egv1a1.KubernetesContainerSpec `json:"kubernetes,omitempty"`
}

// GatewayConfigStatus defines the observed state of GatewayConfig.
type GatewayConfigStatus struct {
	// Conditions describe the current conditions of the GatewayConfig.
	//
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
