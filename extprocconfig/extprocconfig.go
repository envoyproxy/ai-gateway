// Package extprocconfig provides the configuration for the external processor.
// This is a public package so that the external processor can be testable without
// depending on the Envoy Gateway as well as it can be used outside the Envoy AI Gateway.
//
// This configuration must be decoupled from the Envoy Gateway types as well as its implementation
// details. Also, the configuration must not be tied with k8s so it can be tested and iterated
// without the need for the k8s cluster.
package extprocconfig

import (
	"os"

	"k8s.io/apimachinery/pkg/util/yaml"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Config is the configuration for the external processor.
//
// The configuration is loaded from a file path specified via the command line flag -configPath to the external processor.
//
// # Example configuration:
//
//	inputSchema:
//	  schema: OpenAI
//	backendRoutingHeaderKey: x-backend-name
//	modelNameHeaderKey: x-model-name
//	tokenUsageMetadata:
//	  namespace: ai_gateway_llm_ns
//	  key: token_usage_key
//	rules:
//	- backends:
//	  - name: kserve
//	    weight: 1
//	    outputSchema:
//	      schema: OpenAI
//	  - name: awsbedrock
//	    weight: 10
//	    outputSchema:
//	      schema: AWSBedrock
//	  headers:
//	  - name: x-model-name
//	    value: llama3.3333
//	- backends:
//	  - name: openai
//	    outputSchema:
//	      schema: OpenAI
//	  headers:
//	  - name: x-model-name
//	    value: gpt4.4444
//
// where the input of the external processor is in the OpenAI schema, the model name is populated in the header x-model-name,
// The model name header `x-model-name` is used in the header matching to make the routing decision. **After** the routing decision is made,
// the selected backend name is populated in the header `x-backend-name`. For example, when the model name is `llama3.3333`,
// the request is routed to either backends `kserve` or `awsbedrock` with weights 1 and 10 respectively, and the selected
// backend, say `awsbedrock`, is populated in the header `x-backend-name`.
//
// From Envoy configuration perspective, configuring the header matching based on `x-backend-name` is enough to route the request to the selected backend.
// That is because the matching decision is made by the external processor and the selected backend is populated in the header `x-backend-name`.
type Config struct {
	// TokenUsageMetadata is the namespace and key to be used in the filter metadata to store the usage token, optional.
	// If this is provided, the external processor will populate the usage token in the filter metadata at the end of the
	// response body processing.
	TokenUsageMetadata *TokenUsageMetadata `yaml:"tokenUsageMetadata,omitempty"`
	// InputSchema specifies the API schema of the input format of requests to the external processor.
	InputSchema VersionedAPISchema `yaml:"inputSchema"`
	// ModelNameHeaderKey is the header key to be populated with the model name by the external processor.
	ModelNameHeaderKey string `yaml:"modelNameHeaderKey"`
	// BackendRoutingHeaderKey is the header key to be populated with the backend name by the external processor
	// **after** the routing decision is made by the external processor using Rules.
	BackendRoutingHeaderKey string `yaml:"backendRoutingHeaderKey"`
	// Rules is the routing rules to be used by the external processor to make the routing decision.
	// Inside the routing rules, the header ModelNameHeaderKey may be used to make the routing decision.
	Rules []RouteRule `yaml:"rules"`
}

// TokenUsageMetadata is the namespace and key to be used in the filter metadata to store the usage token.
// This can be used to subtract the usage token from the usage quota in the rate limit filter when
// the request completes combined with `apply_on_stream_done` and `hits_addend` fields of
// the rate limit configuration https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/route/v3/route_components.proto#config-route-v3-ratelimit
// which is introduced in Envoy 1.33 (to be released soon as of writing).
type TokenUsageMetadata struct {
	// Namespace is the namespace of the metadata.
	Namespace string `yaml:"namespace"`
	// Key is the key of the metadata.
	Key string `yaml:"key"`
}

// VersionedAPISchema corresponds to LLMAPISchema in api/v1alpha1/api.go.
type VersionedAPISchema struct {
	// Schema is the API schema.
	Schema APISchema `yaml:"schema"`
	// Version is the version of the API schema. Optional.
	Version string `yaml:"version,omitempty"`
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
	Headers []HeaderMatch `yaml:"headers"`
	// Backends is the list of backends to which the request should be routed to when the headers match.
	Backends []Backend `yaml:"backends"`
}

// Backend corresponds to LLMRouteRuleBackendRef in api/v1alpha1/api.go
// besides that this abstracts the concept of a backend at Envoy Gateway level to a simple name.
type Backend struct {
	// Name of the backend, which is the value in the final routing decision
	// matching the header key specified in the [Config.BackendRoutingHeaderKey].
	Name string `yaml:"name"`
	// OutputSchema specifies the API schema of the output format of requests from.
	OutputSchema VersionedAPISchema `yaml:"outputSchema"`
	// Weight is the weight of the backend in the routing decision.
	Weight int `yaml:"weight"`
	// Auth is the authn/z configuration for the backend. Optional.
	// TODO: refactor after https://github.com/envoyproxy/ai-gateway/pull/43.
	Auth *BackendAuth `yaml:"auth,omitempty"`
}

// BackendAuth ... TODO: refactor after https://github.com/envoyproxy/ai-gateway/pull/43.
type BackendAuth struct {
	AWSAuth *AWSAuth `yaml:"aws,omitempty"`
}

// AWSAuth ... TODO: refactor after https://github.com/envoyproxy/ai-gateway/pull/43.
type AWSAuth struct{}

// UnmarshalConfigYaml reads the file at the given path and unmarshals it into a Config struct.
func UnmarshalConfigYaml(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
