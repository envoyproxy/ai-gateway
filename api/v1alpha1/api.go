package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// +kubebuilder:object:root=true

// LLMRoute combines multiple LLMBackends and attaching them to Gateway(s) resources.
//
// This serves as a way to define a "unified" LLM API for a Gateway which allows downstream
// clients to use a single schema API to interact with multiple LLM backends.
//
// The InputSchema is used to determine the structure of the requests that the Gateway will
// receive. And then the Gateway will route the traffic to the appropriate LLMBackend based
// on the output schema of the LLMBackend while doing the other necessary jobs like
// upstream authentication, rate limit, etc.
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
	// APISchema specifies the API schema of the input that the target Gateway(s) will receive.
	// Based on this schema, the ai-gateway will perform the necessary transformation to the
	// output schema specified in the selected LLMBackend during the routing process.
	//
	// Currently, the only supported schema is OpenAI as the input schema.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self.schema == 'OpenAI'"
	APISchema LLMAPISchema `json:"inputSchema"`
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
	BackendRefs []LLMBackendLocalRef `json:"backendRefs,omitempty"`
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
type LLMBackendSpec struct {
	// APISchema specifies the API schema of the output format of requests from
	// Envoy that this LLMBackend can accept as incoming requests.
	// Based on this schema, the ai-gateway will perform the necessary transformation for
	// the pair of LLMRouteSpec.APISchema and LLMBackendSpec.APISchema.
	//
	// This is required to be set.
	APISchema LLMAPISchema `json:"outputSchema"`
}

// LLMAPISchema defines the API schema of either LLMRoute (the input) or LLMBackend (the output).
//
// This allows the ai-gateway to understand the input and perform the necessary transformation
// depending on the API schema pair (input, output).
//
// Note that this is vendor specific, and the stability of the API schema is not guaranteed by
// the ai-gateway, but by the vendor via proper versioning.
type LLMAPISchema struct {
	// Schema is the API schema of the LLMRoute or LLMBackend.
	//
	// +kubebuilder:validation:Enum=OpenAI;AWSBedrock
	Schema APISchema `json:"schema"`

	// Version is the version of the API schema.
	Version string `json:"version,omitempty"`
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
)

// LLMProviderType specifies the type of the LLMProviderPolicy.
type LLMProviderType string

const (
	LLMProviderTypeAPIKey LLMProviderType = "APIKey"
)

// +kubebuilder:object:root=true

// LLMProviderPolicy specifies the provider specific configuration.
//
// This is a provider specific-configuration, e.g.AWS Bedrock, Azure etc.
type LLMProviderPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// BackendRefs lists the LLMBackends that this provider policy will apply
	// The namespace is "local", i.e. the same namespace as the LLMRoute.
	//
	BackendRefs []LLMBackendLocalRef `json:"backendRef,omitempty"`

	// Type specifies the type of the provider. Currently, only "APIKey" and "AWSBedrock" are supported.
	//
	// +kubebuilder:validation:Enum=APIKey;AWSBedrock
	Type LLMProviderType `json:"type"`

	// APIKey specific configuration. The API key will be injected into the Authorization header.
	// +optional
	APIKey *LLMProviderAPIKey `json:"apiKey,omitempty"`
}

// +kubebuilder:object:root=true

// LLMProviderPolicyList contains a list of LLMProviderPolicy
type LLMProviderPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LLMProviderPolicy `json:"items"`
}

// LLMProviderAPIKey specifies the API key.
type LLMProviderAPIKey struct {
	// Type specifies the type of the API key. Currently, "SecretRef" and "Inline" are supported.
	// This defaults to "SecretRef".
	//
	// +kubebuilder:validation:Enum=SecretRef;Inline
	// +kubebuilder:default=SecretRef
	Type LLMProviderAPIKeyType `json:"type"`

	// SecretRef is the reference to the secret containing the API key.
	// ai-gateway must be given the permission to read this secret.
	// The key of the secret should be "apiKey".
	//
	// +optional
	SecretRef *gwapiv1.SecretObjectReference `json:"secretRef"`

	// Inline specifies the inline API key.
	//
	// +optional
	Inline *string `json:"inline,omitempty"`

	// BackendRefs lists the LLMBackends that this API Key will apply
	//
	BackendRefs []LLMBackendLocalRef `json:"backendRefs"`
}

// LLMProviderAPIKeyType specifies the type of LLMProviderAPIKey.
type LLMProviderAPIKeyType string
