// Package extprocconfig provides the configuration for the external processor.
// This is a public package so that the external processor can be testable without
// depending on the Envoy Gateway as well as it can be used outside the Envoy AI Gateway.
//
// This configuration must be decoupled from the Envoy Gateway types as well as its implementation
// details.
package extprocconfig

import gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

// Config is the configuration for the external processor.
type Config struct {
	// InputSchema specifies the API schema of the input format of requests to the external processor.
	InputSchema VersionedAPISchema `json:"inputSchema"`
	// ModelNameHeaderKey is the header key to be populated with the model name by the external processor.
	ModelNameHeaderKey string `json:"modelNameHeaderKey"`
	// BackendRoutingHeaderKey is the header key to be populated with the backend name by the external processor
	// **after** the routing decision is made by the external processor using Rules.
	BackendRoutingHeaderKey string `json:"backendRoutingHeaderKey"`
	// Rules is the routing rules to be used by the external processor to make the routing decision.
	// Inside the routing rules, the header ModelNameHeaderKey may be used to make the routing decision.
	Rules []RouteRule `json:"rules"`
}

// VersionedAPISchema corresponds to LLMAPISchema in api/v1alpha1/api.go.
type VersionedAPISchema struct {
	// Schema is the API schema.
	Schema APISchema `json:"schema"`
	// Version is the version of the API schema. Optional.
	Version string `json:"version,omitempty"`
}

// APISchema corresponds to APISchema in api/v1alpha1/api.go.
type APISchema string

const (
	APISchemaOpenAI     APISchema = "OpenAI"
	APISchemaAWSBedrock APISchema = "AWSBedrock"
)

// HeaderMatch is an alias for HTTPHeaderMatch of the Gateway API.
type HeaderMatch = gwapiv1.HTTPHeaderMatch

// RouteRule corresponds to LLMRouteRule in api/v1alpha1/api.go
// besides the `Backends` field is modified to abstract the concept of a backend
// at Envoy Gateway level to a simple name.
type RouteRule struct {
	// Headers is the list of headers to match for the routing decision.
	// Currently, only exact match is supported.
	Headers []HeaderMatch `json:"headers"`
	// Backends is the list of backends to which the request should be routed to when the headers match.
	Backends []Backend `json:"backends"`
}

// Backend corresponds to LLMRouteRuleBackendRef in api/v1alpha1/api.go
// besides that this abstracts the concept of a backend at Envoy Gateway level to a simple name.
type Backend struct {
	// Name of the backend, which is the value in the final routing decision
	// matching the header key specified in the [Config.BackendRoutingHeaderKey].
	Name string `json:"name"`
	// OutputSchema specifies the API schema of the output format of requests from.
	OutputSchema VersionedAPISchema `json:"outputSchema"`
	// Weight is the weight of the backend in the routing decision.
	Weight int `json:"weight"`
}
