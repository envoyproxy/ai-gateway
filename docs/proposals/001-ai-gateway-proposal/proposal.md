# Envoy AI Gateway

## Proposal Status
 ***Draft***

## Table of Contents

<!-- toc -->

-   [Summary](#summary)
-   [Goals](#goals)
-   [Non-Goals](#non-goals)
-   [Proposal](#proposal)
    -   [Personas](#personas)
    -   [Axioms](#axioms)
    -   [LLMRoute](#llmroute)
    -   [LLMBackend](#llmbackend)
    -   [Token Usage based Rate Limiting](#llmsecuritypolicy)
    -   [Diagrams](#diagrams)
- [FAQ](#faq)
- [Open Questions](#open-questions)

<!-- /toc -->

## Summary
The AI Gateway project is to act as a centralized access point for managing and controlling access to various AI models within an organization.
It provides a single interface for developers to interact with different AI providers while ensuring security, governance and observability over AI traffic.

This proposal introduces four new Custom Resource Definitions(CRD) to support the requirements of the Envoy AI Gateway: **LLMRoute**, **LLMBackend**, **LLMSecurityPolicy**.

* The `LLMRoute` specifies the schema for the user requests and routing rules associated with a list of `LLMBackend`.
* The `LLMBackend` defines the request schema and security policy for various AI providers. This resource is managed by the Inference Platform Admin persona.
* The `LLMSecurityPolicy` defines the authentication policy for AI provider using the API token or OIDC federation.
* For Token Rate Limiting we plan to extend envoy gateway to support generic usage based rate limiting.

## Goals

- Drive the consensus on the Envoy AI Gateway API for the MVP features
  - Model Upstream Access: Support accessing models from an initial list of AI Providers: AWS Bedrock, Google AI Studio, Azure OpenAI.
  - Unified Client Access: Support a unified AI gateway API across AI providers.
  - Traffic Management: Monitor and regulate AI usage, including rate limiting and cost optimization by tracking API calls and model usage.
  - Observability: Provide detailed insights into usage patterns, performance and potential issues through logging and metrics collection.
  - Policy Enforcement: Allow organizations to set specific rules and guidelines for how AI models can be accessed and used.
- Documentation of API decisions for posterity

## Non-Goals

- non-MVP features
- Routing for LLM serving instances in a Kubernetes cluster

## Proposal

### Personas

Before diving into the details of the API, descriptions of the personas will help shape the thought process of the API design.

#### Inference Platform Admin

The Inference Platform Admin manages the gateway infrastructure necessary to route inference requests to a variety of LLM providers. Including handling Ops for:
  - A list of LLM providers and supported models
  - LLM provider API schema conversion and centralized upstream authentication configurations.
  - Traffic policy including rate limiting, fallback resilience between providers.

#### Payment Team

- Reports the per user/tenant LLM token usage for billing purpose.

#### Security Team

- Security team to control the ACL for accessing the models from AI providers.

### Axioms

The API design is based on these axioms:

- This solution should be composable with other Gateway solutions.
- Gateway architecture should be extensible when customization is required.
- The MVP heavily assumes that the requests are done using the OpenAI spec, but open to the extension in the future.


### LLMRoute

`LLMRoute` defines the unified user request schema and the routing rules to a list of supported `LLMBackend`s such as AWS Bedrock, GCP AI Studio, Azure OpenAI and KServe for self-hosted LLMs.

- `LLMRoute` serves as a way to define the unified AI Gateway API which allows downstream clients to use a single schema API to interact with multiple `LLMBackend`s.
- The `LLMRouteRule`s are defined to route to the `LLMBackend`s based on the HTTP header matching. For some features like traffic splitting, the rules are matched in the external proc as the backend needs to be determined before
the request body transformation is backend dependent.
-`LLMTrafficPolicy` is referenced to perform other necessary jobs for upstream authentication and rate limiting.


```golang
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
// Rules is the list of LLMRouteRule that this LLMRoute will match the traffic to.
// Each rule is a subset of the HTTPRoute in the Gateway API (https://gateway-api.sigs.k8s.io/api-types/httproute/).
//
// AI Gateway controller will generate a HTTPRoute based on the configuration given here with the additional
// modifications to achieve the necessary jobs, notably inserting the AI Gateway external processor filter.
//
// In the matching conditions in the LLMRouteRule, `x-envoy-ai-gateway-llm-model` header
// can be used to describe the routing behavior in the HTTPRoute.
//
// +kubebuilder:validation:Required
// +kubebuilder:validation:MaxItems=128
Rules []LLMRouteRule `json:"rules"`
}

// LLMRouteRule is a rule that defines the routing behavior of the LLMRoute.
type LLMRouteRule struct {
// BackendRefs is the list of LLMBackend that this rule will route the traffic to.
// Each backend can have a weight that determines the traffic distribution.
//
// The namespace of each backend is "local", i.e. the same namespace as the LLMRoute.
//
// +optional
// +kubebuilder:validation:MaxItems=128
BackendRefs []LLMRouteRuleBackendRef `json:"backendRefs,omitempty"`

// Matches is the list of LLMRouteMatch that this rule will match the traffic to.
// This is a subset of the HTTPRouteMatch in the Gateway API. See for the details:
// https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPRouteMatch
//
// +optional
// +kubebuilder:validation:MaxItems=128
Matches []LLMRouteRuleMatch `json:"matches,omitempty"`
}
```


### LLMBackend

`LLMBackend` defines the LLM provider API schema and a reference to the envoy gateway backend

- The Gateway routes the traffic to the appropriate `LLMBackend` by converting the unified API schema to the LLM provider API schema.
- The LLMBackend is attached with the `BackendSecurityPolicy` to perform the upstream authentication.

```golang
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
}
```

### LLMSecurityPolicy

```golang
// LLMSecurityPolicySpec specifies a provider (e.g.AWS Bedrock, Azure etc.) specific-configuration like auth
type LLMSecurityPolicySpec struct {
// Type specifies the type of the provider. Currently, only "APIKey" and "AWS_IAM" are supported.
//
// +kubebuilder:validation:Enum=APIKey;AWS_IAM
Type AuthenticationType `json:"type"`

// APIKey specific configuration. The API key will be injected into the Authorization header.
// +optional
APIKey *LLMProviderAPIKey `json:"apiKey,omitempty"`
}
```

### Token Usage Rate Limiting

AI Gateway project plan to extend the envoy gateway `BackendTrafficPolicy` with a generic usage based rate limiting in [#4957](https://github.com/envoyproxy/gateway/pull/4957).
For supporting token usage based rate limiting, we configure `hits_addend` in the response path to allow reducing the counter based on the response content that affects the subsequent requests.
The token usages are extracted from the standard token usage fields according to then OpenAI schema in the ext proc `processResponseBody` handler.

The AI gateway ext proc includes an envoy rate limiting service client to reduce the counter based on the LLM inference responses. The rate limiting server configuration is updated dynamically via xDS
whenever the rate limiting rules are changed.

```go
/// RateLimitRule defines the semantics for matching attributes
// from the incoming requests, and setting limits for them.
type RateLimitRule struct {
// ClientSelectors holds the list of select conditions to select
// specific clients using attributes from the traffic flow.
// All individual select conditions must hold True for this rule
// and its limit to be applied.
//
// If no client selectors are specified, the rule applies to all traffic of
// the targeted Route.
//
// If the policy targets a Gateway, the rule applies to each Route of the Gateway.
// Please note that each Route has its own rate limit counters. For example,
// if a Gateway has two Routes, and the policy has a rule with limit 10rps,
// each Route will have its own 10rps limit.
//
// +optional
// +kubebuilder:validation:MaxItems=8
ClientSelectors []RateLimitSelectCondition `json:"clientSelectors,omitempty"`
// Limit holds the rate limit values.
// This limit is applied for traffic flows when the selectors
// compute to True, causing the request to be counted towards the limit.
// The limit is enforced and the request is ratelimited, i.e. a response with
// 429 HTTP status code is sent back to the client when
// the selected requests have reached the limit.
Limit RateLimitValue `json:"limit"`
// RequestHitsAddend specifies the number to reduce the rate limit counters
// on the request path. If the addend is not specified, the default behavior
// is to reduce the rate limit counters by 1.
//
// When Envoy receives a request that matches the rule, it tries to reduce the
// rate limit counters by the specified number. If the counter doesn't have
// enough capacity, the request is rate limited.
//
// +optional
RequestHitsAddend *RateLimitHitsAddend `json:"requestHitsAddend,omitempty"`
// ResponseHitsAddend specifies the number to reduce the rate limit counters
// after the response is sent back to the client or the request stream is closed.
//
// The addend is used to reduce the rate limit counters for the matching requests.
// Since the reduction happens after the request stream is complete, the rate limit
// won't be enforced for the current request, but for the subsequent matching requests.
//
// This is optional and if not specified, the rate limit counters are not reduced
// on the response path.
//
// Currently, this is only supported for HTTP Global Rate Limits.
//
// +optional
ResponseHitsAddend *RateLimitHitsAddend `json:"responseHitsAddend,omitempty"`
```

### Yaml Examples

#### LLMRoute
The routing calculation in done in the `ExtProc` by analyzing the match rules on `LLMRoute` spec to emulate the behavior in order to perform the provider specific request/response transformation,
because the routing decision is made at the very end of the filter chain.

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: LLMRoute
metadata:
  name: gateway-route
spec:
  inputSchema:
    schema: OpenAI
  rules:
    matches:
      - headers:
          key: x-envoy-ai-gateway-llm-model
          value: llama3-70b
        backendRefs:
        - name: kserve-backend
          weight: 20
        - name: aws-bedrock-backend
          weight: 80
```

#### LLMBackend
```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: LLMBackend
metadata:
  name: kserve
spec:
  outputSchema: OpenAI
  backendRef: kserve-backend
  backendSecurityPolicyName: jwt
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: LLMBackend
metadata:
  name: aws-bedrock-llama-3
spec:
  outputSchema: AWSBedrock
  backendRef: aws-bedrock-backend
  backendSecurityPolicyName: aws-oidc
```

#### BackendTrafficPolicy

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: llama-ratelimit
spec:
  rateLimit:
    type: Global
    global:
      rules:
      - clientSelectors:
          - name: x-ai-gateway-llm-model-name
            type: exact
            value: llama-3.3-70b-instruction
          - name: x-user-id
            type: Distinct
        limit:
          requests: 1000
          unit: Minute
        responseHitsAdded:
          format: "%DYNAMIC_METADATA(llm.ratelimit.ai_gateway_filter:token_usage)%"

```

## Diagrams
### Control Plane
Envoy AI Gateway extends Envoy Gateway using an Extension Server. Envoy Gateway can be configured to call an external server over gRPC with
the xDS configuration before it is sent to Envoy Proxy. The Envoy Gateway extension Server provides a mechanism where Envoy Gateway tracks
custom resources and then calls a set of hooks that allow the generated xDS configuration to be modified before it is sent to Envoy Proxy.

![Data Plane](./control_plane.png)

AI Gateway ExtProc controller watches the `LLMRoute` resource and perform the follow steps:
- Reconciles the envoy gateway ext proc deployment and creates the extension policy.
- Reconciles the envoy proxy deployment and attach the AWS credential if the provider is AWS.
- Reconciles `LLMRoute` to calculate the routing rules and generates the `HTTPRoute` resource applying the extension filter.

AI Gateway extension server also watches the `LLMRoute`, `LLMSecurityPolicy` and `BackendTrafficPolicy` to dynamically update the xDS
configuration for the rate limiting filter and aws signing filter.

### Data Plane

Much of this is better explained visually:

Below is a detailed view how an inference request works on envoy AI gateway

![Data Plane](./data_plane.png)

This diagram lightly follows the example request for routing to Anthropic claude 3.5 sonnet model on AWS Bedrock.
The flow can be described as:
- The request comes in to envoy AI gateway(Ext-Proc).
- Ext Authorization filter is applied for checking if the user or account is authorized to access the model.
- ExtProc looks up the model name claude-3.5-sonnet from the request and inject the request header `x-ai-gateway-llm-model-name`.
- ExtProc extracts the request header `x-ai-gateway-llm-backend` or calculate the rules to determine the backend.
- ExtProc translates the user inference request (OpenAI) to the data schema according to the AI provider.
- Rate limiting is applied for request based usage tracking.
- Provider authentication policy is applied based on the AI provider
  - API key is injected to the request headers for the provider supporting API keys.
  - AWS signing filter is applied for authenticating with AWS Bedrock service if the backend is targeted to AWS
- Routing rule is applied to route the request to the specified or calculated destination.
- Upon receiving the response from AI provider, the token usage is reduced by extracting the usage fields according to OpenAI schema.
  - the rate limit is enforced on the subsequent request.


## FAQ



## Open Questions

