package v1alpha1

import (
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
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
//
// LLMRoute generates a HTTPRoute resource based on the configuration basis for routing the traffic.
// The generated HTTPRoute has the owner reference set to this LLMRoute.
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
	// HTTPRoute is the base HTTPRouteSpec (https://gateway-api.sigs.k8s.io/api-types/httproute/) in
	// the Gateway API on which this LLMRoute will be implemented. AI Gateway controller will generate a HTTPRoute based
	// on the configuration given here with the additional modifications to achieve the necessary jobs,
	// notably inserting the AI Gateway external processor filter.
	//
	// In the matching rules in the HTTPRoute here, `x-envoy-ai-gateway-llm-model` header
	// can be used to describe the routing behavior.
	//
	// Currently, only the exact header matching is supported, otherwise the configuration will be rejected.
	//
	// +kubebuilder:validation:Required
	HTTPRoute gwapiv1.HTTPRouteSpec `json:"httpRoute"`
}

// +kubebuilder:object:root=true

// LLMBackend is a resource that represents a single backend for LLMRoute.
// A backend is a service that handles traffic with a concrete API specification.
//
// A LLMBackend is "attached" to a Backend which is either a k8s Service or a Backend resource of the Envoy Gateway.
//
// When a backend with an attached LLMBackend is used as a routing target in the LLMRoute (more precisely, the
// HTTPRouteSpec defined in the LLMRoute), the ai-gateway will generate the necessary configuration to do
// the backend specific logic in the final HTTPRoute.
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
	//
	// +kubebuilder:validation:Required
	APISchema LLMAPISchema `json:"outputSchema"`
	// BackendRef is the reference to the Backend resource that this LLMBackend corresponds to.
	//
	// A backend can be of either k8s Service or Backend resource of Envoy Gateway.
	//
	// This is required to be set.
	//
	// +kubebuilder:validation:Required
	BackendRef egv1a1.BackendRef `json:"backendRef"`

	// BackendSecurityPolicyRef is the name of the BackendSecurityPolicy resources this backend
	// is being attached to.
	//
	// +optional
	BackendSecurityPolicyRef *gwapiv1.LocalObjectReference `json:"backendSecurityPolicyRef,omitempty"`
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

const (
	// LLMModelHeaderKey is the header key whose value is extracted from the request by the ai-gateway.
	// This can be used to describe the routing behavior in HTTPRoute referenced by LLMRoute.
	LLMModelHeaderKey = "x-envoy-ai-gateway-llm-model"
)

// BackendSecurityPolicyType specifies the type of auth mechanism used to access a backend.
type BackendSecurityPolicyType string

const (
	BackendSecurityPolicyTypeAPIKey BackendSecurityPolicyType = "APIKey"
	BackendSecurityPolicyTypeAWSIAM BackendSecurityPolicyType = "AWS_IAM"
)

// +kubebuilder:object:root=true

// BackendSecurityPolicy specifies configuration for authentication and authorization rules on the traffic
// exiting the gateway to the backend.
type BackendSecurityPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackendSecurityPolicySpec `json:"spec,omitempty"`
}

// BackendSecurityPolicySpec specifies authentication rules on access the provider from the Gateway.
type BackendSecurityPolicySpec struct {
	// Type specifies the auth mechanism used to access the provider. Currently, only "APIKey", AND "AWS_IAM" are supported.
	//
	// +kubebuilder:validation:Enum=APIKey;AWS_IAM
	Type BackendSecurityPolicyType `json:"type"`

	// APIKey is a mechanism to access a backend(s). The API key will be injected into the Authorization header.
	//
	// +optional
	APIKey *AuthenticationAPIKey `json:"apiKey,omitempty"`

	// CloudProviderCredentials is a mechanism to access a backend(s). Cloud provider specific logic will be applied.
	//
	// +optional
	CloudProviderCredentials *AuthenticationCloudProviderCredentials `json:"cloudProviderCredentials,omitempty"`
}

// +kubebuilder:object:root=true

// BackendSecurityPolicyList contains a list of BackendSecurityPolicy
type BackendSecurityPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackendSecurityPolicy `json:"items"`
}

// AuthenticationAPIKey specifies the API key.
type AuthenticationAPIKey struct {
	// SecretRef is the reference to the secret containing the API key.
	// ai-gateway must be given the permission to read this secret.
	// The key of the secret should be "apiKey".
	//
	// +optional
	SecretRef *gwapiv1.SecretObjectReference `json:"secretRef"`
}

// AuthenticationCloudProviderCredentials specifies supported cloud provider authentication methods
type AuthenticationCloudProviderCredentials struct {
	AWSCredentials AWSCredentials `json:"awsCredentials"`
}

// AWSCredentials contains the supported authentication mechanisms to access aws
type AWSCredentials struct {
	// Region specifies the AWS region associated with the policy
	//
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// CredentialsFile specifies the credentials file to use for the AWS provider.
	//
	// +optional
	CredentialsFile *AWSCredentialsFile `json:"credentialsFile,omitempty"`

	// OIDCExchangeToken specifies the oidc configurations used to obtain an oidc token. The oidc token will be
	// used to obtain temporary credentials to access AWS
	// CredentialsFile must be defined when using OIDCFederation.
	//
	// +optional
	OIDCExchangeToken *AWSOIDCExchangeToken `json:"oidcExchangeToken,omitempty"`
}

// AWSCredentialsFile specifies the credentials file to use for the AWS provider.
// Envoy reads the credentials from the file pointed by the Path field, and the profile to use is specified by the Profile field.
type AWSCredentialsFile struct {
	// Path is the path to the credentials file.
	//
	// +kubebuilder:default=~/.aws/credentials
	Path string `json:"path,omitempty"`

	// Profile is the profile to use in the credentials file.
	//
	// +kubebuilder:default=default
	Profile string `json:"profile,omitempty"`
}

// AWSOIDCExchangeToken specifies credentials to obtain oidc token from a sso server.
// For AWS, the controller will query STS to obtain AWS AccessKeyId, SecretAccessKey, and SessionToken,
// and store them in a temporary credentials file
type AWSOIDCExchangeToken struct {
	// OIDC is used to obtain oidc tokens via an SSO server which will be used to exchange for temporary AWS credentials
	OIDC egv1a1.OIDC `json:"oidc"`

	// GrantType is the method application gets access token
	//
	// +optional
	GrantType string `json:"grantType,omitempty"`

	// Aud defines the resource the application can access
	//
	// +optional
	Aud string `json:"aud,omitempty"`

	// AwsRoleArn is the AWS IAM Role with the permission to use specific resources in AWS account which maps to the temporary AWS security credentials exchanged using the authentication token issued by OIDC provider.
	//
	// +optional
	AwsRoleArn string `json:"awsRoleArn,omitempty"`
}
