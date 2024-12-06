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

// +kubebuilder:object:root=true

// LLMBackendTrafficPolicy controls the flow of traffic to the backend.
type LLMBackendTrafficPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the details of the LLMBackend traffic policy.
	Spec LLMBackendTrafficPolicySpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// LLMBackendTrafficPolicyList contains a list of LLMBackendTrafficPolicy
type LLMBackendTrafficPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LLMBackendTrafficPolicy `json:"items"`
}

// LLMBackendTrafficPolicySpec defines the details of llm backend traffic policy
// like rateLimit, timeout etc.
type LLMBackendTrafficPolicySpec struct {
	// BackendRefs lists the LLMBackends that this traffic policy will apply
	// The namespace is "local", i.e. the same namespace as the LLMRoute.
	//
	BackendRef LLMBackendLocalRef `json:"backendRef,omitempty"`
	// RateLimit defines the rate limit policy.
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

// +kubebuilder:validation:XValidation:rule="has(self.group) ? self.group == 'gateway.networking.k8s.io' : true ", message="group must be gateway.networking.k8s.io"
type TargetSelector struct {
	// Group is the group that this selector targets. Defaults to gateway.networking.k8s.io
	//
	// +kubebuilder:default:="gateway.networking.k8s.io"
	Group *gwapiv1a2.Group `json:"group,omitempty"`

	// Kind is the resource kind that this selector targets.
	Kind gwapiv1a2.Kind `json:"kind"`

	// MatchLabels are the set of label selectors for identifying the targeted resource
	MatchLabels map[string]string `json:"matchLabels"`
}
