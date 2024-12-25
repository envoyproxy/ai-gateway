// Package extprocconfig provides the configuration for the external processor.
// This is a public package so that the external processor can be testable without
// depending on the Envoy Gateway as well as it can be used outside the Envoy AI Gateway.
//
// This configuration must be decoupled from the Envoy Gateway types as well as its implementation
// details.
package extprocconfig

import gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

// VersionedAPISchema corresponds to LLMAPISchema in api/v1alpha1/api.go.
type VersionedAPISchema struct {
	Schema  APISchema
	Version string
}

// APISchema corresponds to APISchema in api/v1alpha1/api.go.
type APISchema string

const (
	APISchemaOpenAI     APISchema = "OpenAI"
	APISchemaAWSBedrock APISchema = "AWSBedrock"
)

// Config is the configuration for the external processor.
type Config struct {
	InputSchema             VersionedAPISchema
	ModelNameHeaderKey      string
	BackendRoutingHeaderKey string
	Rules                   []RouteRule
}

// HeaderMatch is an alias for HTTPHeaderMatch of the Gateway API.
type HeaderMatch = gwapiv1.HTTPHeaderMatch

// RouteRule corresponds to LLMRouteRule in api/v1alpha1/api.go
// besides the `Backends` field is modified to abstract the concept of a backend
// at Envoy Gateway level to a simple name.
type RouteRule struct {
	Headers  []HeaderMatch
	Backends []Backend
}

// Backend corresponds to LLMRouteRuleBackendRef in api/v1alpha1/api.go
// besides this abstracts the concept of a backend at Envoy Gateway level to a simple name.
type Backend struct {
	// Name of the backend, which is the value in the final routing decision
	// matching the header key specified in the [Config.BackendRoutingHeaderKey].
	Name string
	// OutputSchema specifies the API schema of the output format of requests from.
	OutputSchema VersionedAPISchema
	// Weight is the weight of the backend in the routing decision.
	Weight int
}
