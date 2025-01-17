package v1alpha1

import (
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// +kubebuilder:object:root=true

// AIGatewayRoute combines multiple AIServiceBackends and attaching them to Gateway(s) resources.
//
// This serves as a way to define a "unified" AI API for a Gateway which allows downstream
// clients to use a single schema API to interact with multiple AI backends.
//
// The inputSchema field is used to determine the structure of the requests that the Gateway will
// receive. And then the Gateway will route the traffic to the appropriate AIServiceBackend based
// on the output schema of the AIServiceBackend while doing the other necessary jobs like
// upstream authentication, rate limit, etc.
//
// AIGatewayRoute generates a HTTPRoute resource based on the configuration basis for routing the traffic.
// The generated HTTPRoute has the owner reference set to this AIGatewayRoute.
type AIGatewayRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the details of the AIGatewayRoute.
	Spec AIGatewayRouteSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// AIGatewayRouteList contains a list of AIGatewayRoute.
type AIGatewayRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIGatewayRoute `json:"items"`
}

// AIGatewayRouteSpec details the AIGatewayRoute configuration.
type AIGatewayRouteSpec struct {
	// TargetRefs are the names of the Gateway resources this AIGatewayRoute is being attached to.
	//
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=128
	TargetRefs []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`
	// APISchema specifies the API schema of the input that the target Gateway(s) will receive.
	// Based on this schema, the ai-gateway will perform the necessary transformation to the
	// output schema specified in the selected AIServiceBackend during the routing process.
	//
	// Currently, the only supported schema is OpenAI as the input schema.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self.schema == 'OpenAI'"
	APISchema VersionedAPISchema `json:"inputSchema"`
	// Rules is the list of AIGatewayRouteRule that this AIGatewayRoute will match the traffic to.
	// Each rule is a subset of the HTTPRoute in the Gateway API (https://gateway-api.sigs.k8s.io/api-types/httproute/).
	//
	// AI Gateway controller will generate a HTTPRoute based on the configuration given here with the additional
	// modifications to achieve the necessary jobs, notably inserting the AI Gateway filter responsible for
	// the transformation of the request and response, etc.
	//
	// In the matching conditions in the AIGatewayRouteRule, `x-envoy-ai-gateway-model` header is available
	// if we want to describe the routing behavior based on the model name. The model name is extracted
	// from the request content before the routing decision.
	//
	// How multiple rules are matched is the same as the Gateway API. See for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPRoute
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxItems=128
	Rules []AIGatewayRouteRule `json:"rules"`

	// FilterConfig is the configuration for the AI Gateway filter inserted in the generated HTTPRoute.
	//
	// An AI Gateway filter is responsible for the transformation of the request and response
	// as well as the routing behavior based on the model name extracted from the request content, etc.
	//
	// Currently, the filter is only implemented as an external process filter, which might be
	// extended to other types of filters in the future. See https://github.com/envoyproxy/ai-gateway/issues/90
	FilterConfig *AIGatewayFilterConfig `json:"filterConfig,omitempty"`

	// RequestCost specifies the cost of the request, notably the token usage.
	// The AI Gateway filter will capture this information and store it in the Envoy's dynamic
	// metadata per HTTP request. The namespaced key is "io.envoy.ai_gateway",
	// and the key is "ai_gateway_route_request_cost".
	//
	// For example, with the following BackendTrafficPolicy of Envoy Gateway,
	// the captured value is used as the "token usage rate limiting":
	//
	// apiVersion: gateway.envoyproxy.io/v1alpha1
	// kind: BackendTrafficPolicy
	// metadata:
	//	name: some-example-token-rate-limit
	//	namespace: default
	// spec:
	//	targetRefs:
	//	  - group: gateway.networking.k8s.io
	//	    kind: HTTPRoute
	//	    name: usage-rate-limit
	//	rateLimit:
	//	  type: Global
	//	  global:
	//	    rules:
	//	      - clientSelectors:
	//	          # Do the rate limiting based on the x-user-id header.
	//	          - headers:
	//	              - name: x-user-id
	//	                type: Exact
	//	                value: one
	//	        limit:
	//	          # Configures the number of "tokens" allowed per hour.
	//	          requests: 10000
	//	          unit: Hour
	//	        cost:
	//	          request:
	//	            from: Number
	//	            # Setting the request cost to zero allows to only check the rate limit budget,
	//	            # and not consume the budget on the request path.
	//	            number: 0
	//	          # This specifies the cost of the response retrieved from the dynamic metadata set by the AI Gateway filter.
	//	          # The extracted value will be used to consume the rate limit budget, and subsequent requests will be rate limited
	//	          # if the budget is exhausted.
	//	          response:
	//	            from: Metadata
	//	            metadata:
	//	              namespace: io.envoy.ai_gateway
	//	              key: ai_gateway_route_request_cost
	RequestCost *RequestCost `json:"requestCost,omitempty"`
}

// AIGatewayRouteRule is a rule that defines the routing behavior of the AIGatewayRoute.
type AIGatewayRouteRule struct {
	// BackendRefs is the list of AIServiceBackend that this rule will route the traffic to.
	// Each backend can have a weight that determines the traffic distribution.
	//
	// The namespace of each backend is "local", i.e. the same namespace as the AIGatewayRoute.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=128
	BackendRefs []AIGatewayRouteRuleBackendRef `json:"backendRefs,omitempty"`

	// Matches is the list of AIGatewayRouteMatch that this rule will match the traffic to.
	// This is a subset of the HTTPRouteMatch in the Gateway API. See for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPRouteMatch
	//
	// +optional
	// +kubebuilder:validation:MaxItems=128
	Matches []AIGatewayRouteRuleMatch `json:"matches,omitempty"`
}

// AIGatewayRouteRuleBackendRef is a reference to a AIServiceBackend with a weight.
type AIGatewayRouteRuleBackendRef struct {
	// Name is the name of the AIServiceBackend.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Weight is the weight of the AIServiceBackend. This is exactly the same as the weight in
	// the BackendRef in the Gateway API. See for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.BackendRef
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Weight int `json:"weight"`
}

type AIGatewayRouteRuleMatch struct {
	// Headers specifies HTTP request header matchers. See HeaderMatch in the Gateway API for the details:
	// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPHeaderMatch
	//
	// Currently, only the exact header matching is supported.
	//
	// +listType=map
	// +listMapKey=name
	// +optional
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self.all(match, match.type != 'RegularExpression')", message="currently only exact match is supported"
	Headers []gwapiv1.HTTPHeaderMatch `json:"headers,omitempty"`
}

type AIGatewayFilterConfig struct {
	// Type specifies the type of the filter configuration.
	//
	// Currently, only ExternalProcess is supported, and default is ExternalProcess.
	//
	// +kubebuilder:default=ExternalProcess
	Type AIGatewayFilterConfigType `json:"type"`

	// ExternalProcess is the configuration for the external process filter.
	// This is optional, and if not set, the default values of Deployment spec will be used.
	//
	// +optional
	ExternalProcess *AIGatewayFilterConfigExternalProcess `json:"externalProcess,omitempty"`
}

// AIGatewayFilterConfigType specifies the type of the filter configuration.
//
// +kubebuilder:validation:Enum=ExternalProcess;DynamicModule
type AIGatewayFilterConfigType string

const (
	AIGatewayFilterConfigTypeExternalProcess AIGatewayFilterConfigType = "ExternalProcess"
	AIGatewayFilterConfigTypeDynamicModule   AIGatewayFilterConfigType = "DynamicModule" // Reserved for https://github.com/envoyproxy/ai-gateway/issues/90
)

type AIGatewayFilterConfigExternalProcess struct {
	// Replicas is the number of desired pods of the external process deployment.
	//
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`
	// Resources required by the external process container.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
	//
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
	// Image is the image of the external process container.
	//
	// This defaults to the ghcr.io/envoyproxy/ai-gateway/extproc:${VERSION} image where
	// ${VERSION} is the version of the Envoy AI Gateway controller.
	//
	// +optional
	Image string `json:"image,omitempty"`
	// TODO: maybe adding the option not to deploy the external process filter and let the user deploy it manually?
	// 	Not sure if it is worth it as we are migrating to dynamic modules.
}

// +kubebuilder:object:root=true

// AIServiceBackend is a resource that represents a single backend for AIGatewayRoute.
// A backend is a service that handles traffic with a concrete API specification.
//
// A AIServiceBackend is "attached" to a Backend which is either a k8s Service or a Backend resource of the Envoy Gateway.
//
// When a backend with an attached AIServiceBackend is used as a routing target in the AIGatewayRoute (more precisely, the
// HTTPRouteSpec defined in the AIGatewayRoute), the ai-gateway will generate the necessary configuration to do
// the backend specific logic in the final HTTPRoute.
type AIServiceBackend struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the details of AIServiceBackend.
	Spec AIServiceBackendSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// AIServiceBackendList contains a list of AIServiceBackends.
type AIServiceBackendList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AIServiceBackend `json:"items"`
}

// AIServiceBackendSpec details the AIServiceBackend configuration.
type AIServiceBackendSpec struct {
	// APISchema specifies the API schema of the output format of requests from
	// Envoy that this AIServiceBackend can accept as incoming requests.
	// Based on this schema, the ai-gateway will perform the necessary transformation for
	// the pair of AIGatewayRouteSpec.APISchema and AIServiceBackendSpec.APISchema.
	//
	// This is required to be set.
	//
	// +kubebuilder:validation:Required
	APISchema VersionedAPISchema `json:"outputSchema"`
	// BackendRef is the reference to the Backend resource that this AIServiceBackend corresponds to.
	//
	// A backend can be of either k8s Service or Backend resource of Envoy Gateway.
	//
	// This is required to be set.
	//
	// +kubebuilder:validation:Required
	BackendRef gwapiv1.BackendObjectReference `json:"backendRef"`

	// BackendSecurityPolicyRef is the name of the BackendSecurityPolicy resources this backend
	// is being attached to.
	//
	// +optional
	BackendSecurityPolicyRef *gwapiv1.LocalObjectReference `json:"backendSecurityPolicyRef,omitempty"`

	// TODO: maybe add backend-level RequestCost configuration that overrides the AIGatewayRoute-level RequestCost.
	// 	That may be useful for the backend that has a different cost calculation logic.
}

// VersionedAPISchema defines the API schema of either AIGatewayRoute (the input) or AIServiceBackend (the output).
//
// This allows the ai-gateway to understand the input and perform the necessary transformation
// depending on the API schema pair (input, output).
//
// Note that this is vendor specific, and the stability of the API schema is not guaranteed by
// the ai-gateway, but by the vendor via proper versioning.
type VersionedAPISchema struct {
	// Schema is the API schema of the AIGatewayRoute or AIServiceBackend.
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
	// AIModelHeaderKey is the header key whose value is extracted from the request by the ai-gateway.
	// This can be used to describe the routing behavior in HTTPRoute referenced by AIGatewayRoute.
	AIModelHeaderKey = "x-envoy-ai-gateway-model"
)

// BackendSecurityPolicyType specifies the type of auth mechanism used to access a backend.
type BackendSecurityPolicyType string

const (
	BackendSecurityPolicyTypeAPIKey         BackendSecurityPolicyType = "APIKey"
	BackendSecurityPolicyTypeAWSCredentials BackendSecurityPolicyType = "AWSCredentials"
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
// Only one mechanism to access a backend(s) can be specified.
//
// Only one type of BackendSecurityPolicy can be defined.
// +kubebuilder:validation:MaxProperties=2
type BackendSecurityPolicySpec struct {
	// Type specifies the auth mechanism used to access the provider. Currently, only "APIKey", AND "AWSCredentials" are supported.
	//
	// +kubebuilder:validation:Enum=APIKey;AWSCredentials
	Type BackendSecurityPolicyType `json:"type"`

	// APIKey is a mechanism to access a backend(s). The API key will be injected into the Authorization header.
	//
	// +optional
	APIKey *BackendSecurityPolicyAPIKey `json:"apiKey,omitempty"`

	// AWSCredentials is a mechanism to access a backend(s). AWS specific logic will be applied.
	//
	// +optional
	AWSCredentials *BackendSecurityPolicyAWSCredentials `json:"awsCredentials,omitempty"`
}

// +kubebuilder:object:root=true

// BackendSecurityPolicyList contains a list of BackendSecurityPolicy
type BackendSecurityPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackendSecurityPolicy `json:"items"`
}

// BackendSecurityPolicyAPIKey specifies the API key.
type BackendSecurityPolicyAPIKey struct {
	// SecretRef is the reference to the secret containing the API key.
	// ai-gateway must be given the permission to read this secret.
	// The key of the secret should be "apiKey".
	SecretRef *gwapiv1.SecretObjectReference `json:"secretRef"`
}

// BackendSecurityPolicyAWSCredentials contains the supported authentication mechanisms to access aws
type BackendSecurityPolicyAWSCredentials struct {
	// Region specifies the AWS region associated with the policy.
	//
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// CredentialsFile specifies the credentials file to use for the AWS provider.
	//
	// +optional
	CredentialsFile *AWSCredentialsFile `json:"credentialsFile,omitempty"`

	// OIDCExchangeToken specifies the oidc configurations used to obtain an oidc token. The oidc token will be
	// used to obtain temporary credentials to access AWS.
	//
	// +optional
	OIDCExchangeToken *AWSOIDCExchangeToken `json:"oidcExchangeToken,omitempty"`
}

// AWSCredentialsFile specifies the credentials file to use for the AWS provider.
// Envoy reads the secret file, and the profile to use is specified by the Profile field.
type AWSCredentialsFile struct {
	// SecretRef is the reference to the credential file
	SecretRef *gwapiv1.SecretObjectReference `json:"secretRef"`

	// Profile is the profile to use in the credentials file.
	//
	// +kubebuilder:default=default
	Profile string `json:"profile,omitempty"`
}

// AWSOIDCExchangeToken specifies credentials to obtain oidc token from a sso server.
// For AWS, the controller will query STS to obtain AWS AccessKeyId, SecretAccessKey, and SessionToken,
// and store them in a temporary credentials file.
type AWSOIDCExchangeToken struct {
	// OIDC is used to obtain oidc tokens via an SSO server which will be used to exchange for temporary AWS credentials.
	OIDC egv1a1.OIDC `json:"oidc"`

	// GrantType is the method application gets access token.
	//
	// +optional
	GrantType string `json:"grantType,omitempty"`

	// Aud defines the audience that this ID Token is intended for.
	//
	// +optional
	Aud string `json:"aud,omitempty"`

	// AwsRoleArn is the AWS IAM Role with the permission to use specific resources in AWS account
	// which maps to the temporary AWS security credentials exchanged using the authentication token issued by OIDC provider.
	AwsRoleArn string `json:"awsRoleArn"`
}

// RequestCost configures the request cost.
type RequestCost struct {
	// Type specifies the type of the request cost. The default is "OutputToken",
	// and it uses "output token" as the cost. The other types are "InputToken" and "TotalToken".
	//
	// +kubebuilder:validation:Enum=OutputToken;InputToken;TotalToken
	Type RequestCostType `json:"type"`

	// CELExpression is the CEL expression to calculate the cost of the request.
	// The CEL expression must return an integer value. The CEL expression should be
	// able to access the request headers, model name, backend name, input/output tokens etc.
	//
	// +optional
	// +notImplementedHide https://github.com/envoyproxy/ai-gateway/issues/97
	CELExpression *string `json:"celExpression"`
}

// RequestCostType specifies the type of the RequestCost.
type RequestCostType string

const (
	RequestCostTypeInputToken  RequestCostType = "InputToken"
	RequestCostTypeOutputToken RequestCostType = "OutputToken"
	RequestCostTypeTotalToken  RequestCostType = "TotalToken"
	RequestCostTypeCEL         RequestCostType = "CEL"
)

const (
	// AIGatewayFilterMetadataNamespace is the namespace for the ai-gateway filter metadata.
	AIGatewayFilterMetadataNamespace              = "io.envoy.ai_gateway"
	AIGatewayFilterMetadataRequestCostMetadataKey = "ai_gateway_route_request_cost"
)
