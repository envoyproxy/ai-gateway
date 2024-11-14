package v1alpha1

import (
	"encoding/json"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// +kubebuilder:object:root=true

// LLMRoute is the Schema for controlling the LLM provider routing.
// This corresponds to HTTPRoute in the Gateway API, and is an abstraction layer on top of it.
//
// All resources created by a LLMRoute have the same namespace as LLMRoute as well as owner references set to the LLMRoute.
//
// This corresponds to one gateway.networking.k8s.io/v1.HTTPRoute resource with the exact header
// match rules where "x-ai-gateway-llm-backend" header is set to the name of each LLMBackend.
//
// "x-ai-gateway-llm-backend" is a reserved header and should be directly set by the client or the earlier filters,
// e.g. additional external filters like the ExtProc field in the LLMRoute.
//
// For example, if [LLMRouteSpec.Backends] has two backends named "openai" and "ollama", the controller will create an HTTPRoute
// with the following rules:
//
//	apiVersion: gateway.networking.k8s.io/v1
//	kind: HTTPRoute
//	metadata:
//	  name: llmroute-${LLMRoute.name}
//	  namespace: ${LLMRoute.namespace}
//	spec:
//	  ..... # Omitting other fields for brevity.
//	  rules:
//	  - backendRefs:
//	    - kind: Service
//	      name: ollama
//	      port: 80
//	    matches:
//	    - headers:
//	      - name: x-ai-gateway-llm-backend
//	        exact: ollama
//	  - backendRefs:
//	    - group: gateway.envoyproxy.io
//	      kind: Backend
//	      name: openai
//	    matches:
//	    - headers:
//	      - name: x-ai-gateway-llm-backend
//	        exact: openai
//
// Optionally, this might also create or update an EnvoyProxy resource for each target Gateway.
//
// Implementation note: The controller generates a corresponding Deployment and Service for the external processor.
// These resources are created in the same namespace as the LLMRoute.
// The name of the Deployment and Service is "ai-gateway-extproc-${LLMRoute.name}-${LLMRoute.namespace}".
// The corresponding external processor is started with the LLMRoute configuration to control the behavior.
// For all Backends, requests are sent to that external processor for processing.
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

// String returns the string representation of the LLM traffic policy for debugging and logging.
func (p *LLMRoute) String() string {
	bytes, _ := json.Marshal(p) // For now, just marshal the policy to JSON.
	return string(bytes)
}

// LLMRouteSpec details the LLMRoute configuration.
type LLMRouteSpec struct {
	// ExtProcConfig contains the configuration for the Deployment of the ai-gateway's external processor.
	//
	// +optional
	ExtProcConfig *LLMRouteExtProcConfig `json:"extProcConfig,omitempty"`
	// TargetRefs are the names of the Gateway resources this policy is being attached to.
	// The namespace is "local", i.e. the same namespace as the LLMRoute.
	// A Gateway cannot have more than one LLMRoute attached to it. TODO: enforce this rule in the controller.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=16
	TargetRefs []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`
	// Backends lists the details of the LLM backends available to the Gateway.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	Backends []LLMBackend `json:"backends,omitempty"`
	// ExtProc an ordered list of external processing filters that should be added to the envoy filter chain.
	// This is non-backend specific and applies to all backends. In other words, these filters are applied to all
	// requests before backend specific filters are applied (the ones specified in the LLMBackend).
	//
	// This is especially useful for selecting the backend based on the custom logic. Since routing decisions are
	// made via the "x-ai-gateway-llm-backend" header, the external filters can set this header based on the custom logic.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=32
	ExtProc []egv1a1.ExtProc `json:"extProc,omitempty"`

	// Schema specifies the API schema of the LLM route. In other words, this specifies the API schema that clients
	// should use to interact with the LLM route.
	//
	// Currently, only "OpenAI" is supported, and it defaults to "OpenAI".
	//
	// +kubebuilder:validation:Enum=OpenAI
	// +kubebuilder:default=OpenAI
	// +optional
	Schema LLMRouteAPISchema `json:"schema,omitempty"`
}

// LLMRouteAPISchema defines the API schema of the LLM route. This specifies either client or backend API schema.
type LLMRouteAPISchema string

const (
	// LLMRouteAPISchemaOpenAI is the OpenAI schema.
	//
	// https://platform.openai.com/docs/overview
	LLMRouteAPISchemaOpenAI LLMRouteAPISchema = "OpenAI"
	// LLMRouteAPISchemaAWSBedrock is the AWS Bedrock schema.
	//
	// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_Operations_Amazon_Bedrock_Runtime.html
	LLMRouteAPISchemaAWSBedrock LLMRouteAPISchema = "AWSBedrock"
)

// LLMRouteExtProcConfig contains the configuration for the Deployment of the ai-gateway's external processor.
type LLMRouteExtProcConfig struct {
	// Replicas is the number of replicas of the external processor. Defaults to 1.
	//
	// +optional
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas,omitempty"`
	// Resources is the resource requirements for the external processor.
	//
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// LLMBackend describes the details of a LLM backend, e.g. OpenAI, AWS Bedrock, etc.
// Each backend can have its own rate limit policy as well as request/response transformation.
// This configures how to perform the LLM specific operations on the requests and responses to and from
// that referenced backend.
type LLMBackend struct {
	// BackendRef is the reference to the Backend resource that this LLMBackend corresponds to.
	//
	// The backend can be of either k8s Service or [egv1a1.Backend] kind plus the resource must be in the same namespace as the LLMRoute.
	// For the sake of simplicity, currently the same backend cannot be referenced by multiple LLMBackend(s).
	BackendRef egv1a1.BackendRef `json:"backendRef"`
	// Schema specifies the schema of the LLM backend.
	// Currently, only "OpenAI" and "AWSBedrock" are supported. This defaults to "OpenAI".
	//
	// +kubebuilder:validation:Enum=OpenAI;AWSBedrock
	// +kubebuilder:default=OpenAI
	Schema LLMRouteAPISchema `json:"schema,omitempty"`
	// ProviderPolicy specifies the provider specific configuration such as.
	ProviderPolicy *LLMProviderPolicy `json:"providerPolicy,omitempty"`
	// TrafficPolicy controls the flow of traffic to the backend.
	TrafficPolicy *LLMTrafficPolicy `json:"trafficPolicy,omitempty"`
}

// LLMProviderPolicy specifies the provider specific configuration.
//
// This is a provider specific-configuration, e.g.AWS Bedrock, Azure etc.
type LLMProviderPolicy struct {
	// Type specifies the type of the provider. Currently, only "APIKey" and "AWSBedrock" are supported.
	//
	// +kubebuilder:validation:Enum=APIKey;AWSBedrock
	Type LLMProviderType `json:"type"`

	// APIKey specific configuration. The API key will be injected into the Authorization header.
	// +optional
	APIKey *LLMProviderAPIKey `json:"apiKey,omitempty"`

	// AWS Bedrock specific configuration. Note that at most one AWS Bedrock policy can be specified.
	// Otherwise, the Envoy cannot determine which credentials to use.
	//
	// AWS Bedrock provider utilizes the EnvoyProxy resource to attach the environment variables for the AWS credentials
	// to the Envoy container. The controller checks the existence of the EnvoyProxy resource attached to each
	// target Gateway and creates a new one if it doesn't exist. If it exists, it updates the existing one.
	// If the new EnvoyProxy resource is created, the controller sets the owner reference to the LLMRoute.
	// The created EnvoyProxy resource will have the same namespace and name as in the form of "${LLMRoute's name}-${Each target Gateway's name}".
	// It is user's responsibility to attach the Gateway resource to the EnvoyProxy resource via "infrastructure" field. See
	// https://github.com/envoyproxy/gateway/blob/a6590bf81463d5cfeadc817f5238a01507ab1a9b/test/e2e/testdata/tracing-zipkin.yaml#L15-L19
	// for an example.
	//
	// This EnvoyProxy resource manipulation is necessary because the AWS signing filter doesn't support
	// dynamic configuration, but it can be configured only by the environment variables.
	// https://github.com/envoyproxy/envoy/blob/0ad67a1d7f8f6352e8c2b7abcce627d8f212c081/source/extensions/common/aws/credentials_provider_impl.cc#L775
	// See the upstream issue: https://github.com/envoyproxy/envoy/issues/36109 for more details. Once this issue is resolved,
	// we can remove this EnvoyProxy resource manipulation.
	//
	//
	// +optional
	AWSBedrock *LLMProviderAWSBedrock `json:"awsBedrock,omitempty"`

	// TODO: Azure, etc?
}

// LLMProviderType specifies the type of the LLMProviderPolicy.
type LLMProviderType string

const (
	LLMProviderTypeAPIKey     LLMProviderType = "APIKey"
	LLMProviderTypeAWSBedrock LLMProviderType = "AWSBedrock"
)

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
}

// LLMProviderAPIKeyType specifies the type of LLMProviderAPIKey.
type LLMProviderAPIKeyType string

const (
	LLMProviderAPIKeyTypeSecretRef LLMProviderAPIKeyType = "SecretRef"
	LLMProviderAPIKeyTypeInline    LLMProviderAPIKeyType = "Inline"
)

// LLMProviderAWSBedrock specifies the AWS Bedrock specific configuration.
type LLMProviderAWSBedrock struct {
	// Region is the AWS region to use.
	Region string `json:"region"`
	// Type specifies the type of the AWS Bedrock provider. Currently, "InlineCredential" and "CredentialsFile" are supported.
	// This defaults to "InlineCredential".
	//
	// +kubebuilder:validation:Enum=InlineCredential;CredentialsFile
	// +kubebuilder:default=InlineCredential
	Type LLMProviderAWSBedrockType `json:"type"`

	// SigningAlgorithm specifies the algorithm to use for signing the request.
	// AWS_SIGV4 or AWS_SIGV4A are supported. This is optional and defaults to AWS_SIGV4.
	SigningAlgorithm *string `json:"signingAlgorithm,omitempty"`

	// HostRewrite specifies the real AWE Bedrock host (e.g. bedrock-runtime.us-east-1.amazonaws.com)
	// to rewrite in the request before the AWS request signing.
	// Usually, this matches the hostname of the backend, but it can be different in case of a proxy.
	//
	// This defaults to "bedrock-runtime.${Region}.amazonaws.com".
	// +optional
	HostRewrite *string `json:"hostRewrite"`

	// InlineCredential specifies the inline credential to use for the AWS Bedrock provider.
	// +optional
	InlineCredential *LLMProviderAWSBedrockInlineCredential `json:"inlineCredential,omitempty"`

	// CredentialsFile specifies the credentials file to use for the AWS Bedrock provider.
	// +optional
	CredentialsFile *LLMProviderAWSBedrockCredentialsFile `json:"credentialsFile,omitempty"`
}

// LLMProviderAWSBedrockType specifies the type of the AWS Bedrock provider.
type LLMProviderAWSBedrockType string

const (
	// LLMProviderAWSBedrockTypeInlineCredential specifies the inline credentials.
	LLMProviderAWSBedrockTypeInlineCredential LLMProviderAWSBedrockType = "InlineCredential" // nolint: gosec
	LLMProviderAWSBedrockTypeCredentialsFIle  LLMProviderAWSBedrockType = "CredentialsFile"  // nolint: gosec
)

// LLMProviderAWSBedrockInlineCredential specifies the inline credentials.
type LLMProviderAWSBedrockInlineCredential struct {
	// AccessKeyID is the AWS access key ID.
	// +kubebuilder:validation:MinLength=1
	AccessKeyID string `json:"accessKeyID"`
	// SecretAccessKey is the AWS secret access key.
	// +kubebuilder:validation:MinLength=1
	SecretAccessKey string `json:"secretAccessKey"`
	// SessionToken is the AWS session token.
	// +optional
	SessionToken string `json:"sessionToken,omitempty"`
}

// LLMProviderAWSBedrockCredentialsFile specifies the credentials file to use for the AWS Bedrock provider.
// Envoy reads the credentials from the file pointed by the Path field, and the profile to use is specified by the Profile field.
type LLMProviderAWSBedrockCredentialsFile struct {
	// Path is the path to the credentials file.
	// This defaults to "~/.aws/credentials".
	//
	// +optional
	Path string `json:"path,omitempty"`

	// Profile is the profile to use in the credentials file.
	// This defaults to "default".
	//
	// +optional
	Profile string `json:"profile,omitempty"`
}

type LLMTrafficPolicy struct {
	// RateLimit defines the usual rate limit policy for this backend.
	RateLimit *LLMTrafficPolicyRateLimit `json:"rateLimit,omitempty"`
}

type LLMTrafficPolicyRateLimit struct {
	// Rules defines the rate limit rules.
	Rules []LLMTrafficPolicyRateLimitRule `json:"rules,omitempty"`
}

// LLMTrafficPolicyRateLimitRule defines the details of the rate limit policy.
type LLMTrafficPolicyRateLimitRule struct {
	// Headers is a list of request headers to match. Multiple header values are ANDed together,
	// meaning, a request MUST match all the specified headers.
	// At least one of headers or sourceCIDR condition must be specified.
	Headers []LLMPolicyRateLimitHeaderMatch `json:"headers,omitempty"`
	// Metadata is a list of metadata to match. Multiple metadata values are ANDed together,
	Metadata []LLMPolicyRateLimitMetadataMatch `json:"metadata,omitempty"`
	// Limits holds the rate limit values.
	// This limit is applied for traffic flows when the selectors
	// compute to True, causing the request to be counted towards the limit.
	// The limit is enforced and the request is ratelimited, i.e. a response with
	// 429 HTTP status code is sent back to the client when
	// the selected requests have reached the limit.
	//
	// +kubebuilder:validation:MinItems=1
	Limits []LLMPolicyRateLimitValue `json:"limits"`
}

type LLMPolicyRateLimitModelNameMatch struct {
	// Type specifies how to match against the value of the model name.
	// Only "Exact" and "Distinct" are supported.
	// +kubebuilder:validation:Enum=Exact;Distinct
	Type LLMPolicyRateLimitStringMatchType `json:"type"`
	// Value specifies the value of the model name base on the match Type.
	// It is ignored if the match Type is "Distinct".
	//
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Value *string `json:"value"`
}

// LLMPolicyRateLimitHeaderMatch defines the match attributes within the HTTP Headers of the request.
type LLMPolicyRateLimitHeaderMatch struct {
	// Type specifies how to match against the value of the header.
	Type LLMPolicyRateLimitStringMatchType `json:"type"`

	// Name of the HTTP header.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`

	// Value within the HTTP header. Due to the
	// case-insensitivity of header names, "foo" and "Foo" are considered equivalent.
	// Do not set this field when Type="Distinct", implying matching on any/all unique
	// values within the header.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Value *string `json:"value,omitempty"`
}

// LLMPolicyRateLimitStringMatchType specifies the semantics of how string values should be compared.
// Valid LLMPolicyRateLimitStringMatchType values are "Exact", "RegularExpression", and "Distinct".
//
// +kubebuilder:validation:Enum=Exact;RegularExpression;Distinct
type LLMPolicyRateLimitStringMatchType string

// HeaderMatchType constants.
const (
	// HeaderMatchExact matches the exact value of the Value field against the value of
	// the specified HTTP Header.
	HeaderMatchExact LLMPolicyRateLimitStringMatchType = "Exact"
	// HeaderMatchRegularExpression matches a regular expression against the value of the
	// specified HTTP Header. The regex string must adhere to the syntax documented in
	// https://github.com/google/re2/wiki/Syntax.
	HeaderMatchRegularExpression LLMPolicyRateLimitStringMatchType = "RegularExpression"
	// HeaderMatchDistinct matches any and all possible unique values encountered in the
	// specified HTTP Header. Note that each unique value will receive its own rate limit
	// bucket.
	// Note: This is only supported for Global Rate Limits.
	HeaderMatchDistinct LLMPolicyRateLimitStringMatchType = "Distinct"
)

// LLMPolicyRateLimitMetadataMatch defines the match attributes within the metadata from dynamic or route entry.
// The match will be ignored if the metadata is not present.
type LLMPolicyRateLimitMetadataMatch struct {
	// Type specifies the type of metadata to match.
	//
	// +kubebuilder:default=Dynamic
	Type LLMPolicyRateLimitMetadataMatchMetadataType `json:"type"`
	// Name specifies the key of the metadata to match.
	Name string `json:"name"`
	// Paths specifies the value of the metadata to match.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	Paths []string `json:"paths,omitempty"`
	// DefaultValue specifies an optional value to use if “metadata“ is empty.
	// Default value is "unknown".
	//
	// +optional
	DefaultValue *string `json:"defaultValue,omitempty"`
}

// LLMPolicyRateLimitMetadataMatchMetadataType specifies the type of metadata to match.
//
// +kubebuilder:validation:Enum=Dynamic;RouteEntry
type LLMPolicyRateLimitMetadataMatchMetadataType string

const (
	// MetadataTypeDynamic specifies that the source of metadata is dynamic.
	MetadataTypeDynamic LLMPolicyRateLimitMetadataMatchMetadataType = "Dynamic"
)

// LLMPolicyRateLimitValue defines the limits for rate limiting.
type LLMPolicyRateLimitValue struct {
	// Type specifies the type of rate limit.
	//
	// +kubebuilder:default=Request
	Type LLMPolicyRateLimitType `json:"type"`
	// Quantity specifies the number of requests or tokens allowed in the given interval.
	Quantity uint `json:"quantity"`
	// Unit specifies the interval for the rate limit.
	//
	// +kubebuilder:default=Minute
	Unit LLMPolicyRateLimitUnit `json:"unit"`
}

// LLMPolicyRateLimitType specifies the type of rate limit.
// Valid RateLimitType values are "Request" and "Token".
//
// +kubebuilder:validation:Enum=Request;Token
type LLMPolicyRateLimitType string

const (
	// RateLimitTypeRequest specifies the rate limit to be based on the number of requests.
	RateLimitTypeRequest LLMPolicyRateLimitType = "Request"
	// RateLimitTypeToken specifies the rate limit to be based on the number of tokens.
	RateLimitTypeToken LLMPolicyRateLimitType = "Token"
)

// LLMPolicyRateLimitUnit specifies the intervals for setting rate limits.
// Valid RateLimitUnit values are "Second", "Minute", "Hour", and "Day".
//
// +kubebuilder:validation:Enum=Second;Minute;Hour;Day
type LLMPolicyRateLimitUnit string

// RateLimitUnit constants.
const (
	// RateLimitUnitSecond specifies the rate limit interval to be 1 second.
	RateLimitUnitSecond LLMPolicyRateLimitUnit = "Second"

	// RateLimitUnitMinute specifies the rate limit interval to be 1 minute.
	RateLimitUnitMinute LLMPolicyRateLimitUnit = "Minute"

	// RateLimitUnitHour specifies the rate limit interval to be 1 hour.
	RateLimitUnitHour LLMPolicyRateLimitUnit = "Hour"

	// RateLimitUnitDay specifies the rate limit interval to be 1 day.
	RateLimitUnitDay LLMPolicyRateLimitUnit = "Day"
)

const (
	// LLMRoutingHeaderKey is the header key used to route requests to the selected backend.
	LLMRoutingHeaderKey = "x-ai-gateway-llm-backend"
	// LLMModelNameHeaderKey is the header key used to inject the model name into the request.
	LLMModelNameHeaderKey = "x-ai-gateway-llm-model-name"
)
