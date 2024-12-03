package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// +kubebuilder:object:root=true

// LLMRoute combines multiple LLMBackends and attaching them to Gateway(s) resources.
//
// This serves as a way to define a "unified" LLM API for a Gateway which allows downstream
// clients to use a single schema API to interact with multiple LLM backends.
type LLMRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the details of the LLM policy.
	Spec LLMRouteSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// LLMRouteList contains a list of LLMTrafficPolicy
type LLMRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LLMRoute `json:"items"`
}

// LLMRouteSpec details the LLMRoute configuration.
type LLMRouteSpec struct {
	// TargetRefs are the names of the Gateway resources this policy is being attached to.
	// The namespace is "local", i.e. the same namespace as the LLMRoute.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=128
	TargetRefs []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`
	// BackendRefs lists the LLMBackends that this LLMRoute will route traffic to.
	// The namespace is "local", i.e. the same namespace as the LLMRoute.
	//
	// +kubebuilder:validation:MaxItems=128
	BackendRefs []LLMBackendLocalRef `json:"backends,omitempty"`
}

// LLMBackendLocalRef is a reference to a LLMBackend resource in the "local" namespace.
type LLMBackendLocalRef struct {
	// Name is the name of the LLMBackend in the same namespace as the LLMRoute.
	Name string `json:"name"`
}

// +kubebuilder:object:root=true

// LLMBackend is a resource that represents a single backend for LLMRoute.
// A backend is a service that handles traffic with a concrete API specification.
type LLMBackend struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the details of the LLM policy.
	Spec LLMBackendSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// LLMBackendList contains a list of LLMBackends.
type LLMBackendList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LLMBackend `json:"items"`
}

// LLMBackendSpec details the LLMBackend configuration.
type LLMBackendSpec struct{}
