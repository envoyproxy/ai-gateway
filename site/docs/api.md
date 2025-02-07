---
id: api_references
title: API Reference
---

<a id="api_references"></a>
# Packages
- [aigateway.envoyproxy.io/v1alpha1](#-)


<a name="-"></a>
# aigateway.envoyproxy.io/v1alpha1

Package v1alpha1 contains API schema definitions for the aigateway.envoyproxy.io
API group.


## Resource Types
- [AIGatewayRoute](#aigatewayroute)
- [AIGatewayRouteList](#aigatewayroutelist)
- [AIServiceBackend](#aiservicebackend)
- [AIServiceBackendList](#aiservicebackendlist)
- [BackendSecurityPolicy](#backendsecuritypolicy)
- [BackendSecurityPolicyList](#backendsecuritypolicylist)



### AIGatewayFilterConfig





_Appears in:_
- [AIGatewayRouteSpec](#aigatewayroutespec)

- **type**
  - **Type:** _[AIGatewayFilterConfigType](#aigatewayfilterconfigtype)_
  - **Required:** Yes
  - **Description:** Type specifies the type of the filter configuration.<br /><br />Currently, only ExternalProcess is supported, and default is ExternalProcess.
- **externalProcess**
  - **Type:** _[AIGatewayFilterConfigExternalProcess](#aigatewayfilterconfigexternalprocess)_
  - **Required:** No
  - **Description:** ExternalProcess is the configuration for the external process filter.<br />This is optional, and if not set, the default values of Deployment spec will be used.


### AIGatewayFilterConfigExternalProcess





_Appears in:_
- [AIGatewayFilterConfig](#aigatewayfilterconfig)

- **replicas**
  - **Type:** _integer_
  - **Required:** No
  - **Description:** Replicas is the number of desired pods of the external process deployment.
- **resources**
  - **Type:** _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#resourcerequirements-v1-core)_
  - **Required:** No
  - **Description:** Resources required by the external process container.<br />More info: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/


### AIGatewayFilterConfigType

_Underlying type:_ _string_

AIGatewayFilterConfigType specifies the type of the filter configuration.

_Appears in:_
- [AIGatewayFilterConfig](#aigatewayfilterconfig)

| Value | Description |
| ----- | ----------- |
| `ExternalProcess` |  |
| `DynamicModule` |  |


### AIGatewayRoute



AIGatewayRoute combines multiple AIServiceBackends and attaching them to Gateway(s) resources.


This serves as a way to define a "unified" AI API for a Gateway which allows downstream
clients to use a single schema API to interact with multiple AI backends.


The schema field is used to determine the structure of the requests that the Gateway will
receive. And then the Gateway will route the traffic to the appropriate AIServiceBackend based
on the output schema of the AIServiceBackend while doing the other necessary jobs like
upstream authentication, rate limit, etc.


AIGatewayRoute generates a HTTPRoute resource based on the configuration basis for routing the traffic.
The generated HTTPRoute has the owner reference set to this AIGatewayRoute.

_Appears in:_
- [AIGatewayRouteList](#aigatewayroutelist)

- **apiVersion**
  - **Type:** _string_
  - **Value:** `aigateway.envoyproxy.io/v1alpha1`
- **kind**
  - **Type:** _string_
  - **Value:** `AIGatewayRoute`
- **metadata**
  - **Type:** _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#objectmeta-v1-meta)_
  - **Required:** Yes
  - **Description:** Refer to Kubernetes API documentation for fields of `metadata`.
- **spec**
  - **Type:** _[AIGatewayRouteSpec](#aigatewayroutespec)_
  - **Required:** Yes
  - **Description:** Spec defines the details of the AIGatewayRoute.


### AIGatewayRouteList



AIGatewayRouteList contains a list of AIGatewayRoute.



- **apiVersion**
  - **Type:** _string_
  - **Value:** `aigateway.envoyproxy.io/v1alpha1`
- **kind**
  - **Type:** _string_
  - **Value:** `AIGatewayRouteList`
- **metadata**
  - **Type:** _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#listmeta-v1-meta)_
  - **Required:** Yes
  - **Description:** Refer to Kubernetes API documentation for fields of `metadata`.
- **items**
  - **Type:** _[AIGatewayRoute](#aigatewayroute) array_
  - **Required:** Yes
  - **Description:** 


### AIGatewayRouteRule



AIGatewayRouteRule is a rule that defines the routing behavior of the AIGatewayRoute.

_Appears in:_
- [AIGatewayRouteSpec](#aigatewayroutespec)

- **backendRefs**
  - **Type:** _[AIGatewayRouteRuleBackendRef](#aigatewayrouterulebackendref) array_
  - **Required:** No
  - **Description:** BackendRefs is the list of AIServiceBackend that this rule will route the traffic to.<br />Each backend can have a weight that determines the traffic distribution.<br /><br />The namespace of each backend is "local", i.e. the same namespace as the AIGatewayRoute.
- **matches**
  - **Type:** _[AIGatewayRouteRuleMatch](#aigatewayrouterulematch) array_
  - **Required:** No
  - **Description:** Matches is the list of AIGatewayRouteMatch that this rule will match the traffic to.<br />This is a subset of the HTTPRouteMatch in the Gateway API. See for the details:<br />https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPRouteMatch


### AIGatewayRouteRuleBackendRef



AIGatewayRouteRuleBackendRef is a reference to a AIServiceBackend with a weight.

_Appears in:_
- [AIGatewayRouteRule](#aigatewayrouterule)

- **name**
  - **Type:** _string_
  - **Required:** Yes
  - **Description:** Name is the name of the AIServiceBackend.
- **weight**
  - **Type:** _integer_
  - **Required:** No
  - **Description:** Weight is the weight of the AIServiceBackend. This is exactly the same as the weight in<br />the BackendRef in the Gateway API. See for the details:<br />https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.BackendRef<br /><br />Default is 1.


### AIGatewayRouteRuleMatch





_Appears in:_
- [AIGatewayRouteRule](#aigatewayrouterule)

- **headers**
  - **Type:** _HTTPHeaderMatch array_
  - **Required:** No
  - **Description:** Headers specifies HTTP request header matchers. See HeaderMatch in the Gateway API for the details:<br />https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPHeaderMatch<br /><br />Currently, only the exact header matching is supported.


### AIGatewayRouteSpec



AIGatewayRouteSpec details the AIGatewayRoute configuration.

_Appears in:_
- [AIGatewayRoute](#aigatewayroute)

- **targetRefs**
  - **Type:** _[LocalPolicyTargetReferenceWithSectionName](https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io/v1alpha2.LocalPolicyTargetReferenceWithSectionName) array_
  - **Required:** Yes
  - **Description:** TargetRefs are the names of the Gateway resources this AIGatewayRoute is being attached to.
- **schema**
  - **Type:** _[VersionedAPISchema](#versionedapischema)_
  - **Required:** Yes
  - **Description:** APISchema specifies the API schema of the input that the target Gateway(s) will receive.<br />Based on this schema, the ai-gateway will perform the necessary transformation to the<br />output schema specified in the selected AIServiceBackend during the routing process.<br /><br />Currently, the only supported schema is OpenAI as the input schema.
- **rules**
  - **Type:** _[AIGatewayRouteRule](#aigatewayrouterule) array_
  - **Required:** Yes
  - **Description:** Rules is the list of AIGatewayRouteRule that this AIGatewayRoute will match the traffic to.<br />Each rule is a subset of the HTTPRoute in the Gateway API (https://gateway-api.sigs.k8s.io/api-types/httproute/).<br /><br />AI Gateway controller will generate a HTTPRoute based on the configuration given here with the additional<br />modifications to achieve the necessary jobs, notably inserting the AI Gateway filter responsible for<br />the transformation of the request and response, etc.<br /><br />In the matching conditions in the AIGatewayRouteRule, `x-ai-eg-model` header is available<br />if we want to describe the routing behavior based on the model name. The model name is extracted<br />from the request content before the routing decision.<br /><br />How multiple rules are matched is the same as the Gateway API. See for the details:<br />https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io%2fv1.HTTPRoute
- **filterConfig**
  - **Type:** _[AIGatewayFilterConfig](#aigatewayfilterconfig)_
  - **Required:** Yes
  - **Description:** FilterConfig is the configuration for the AI Gateway filter inserted in the generated HTTPRoute.<br /><br />An AI Gateway filter is responsible for the transformation of the request and response<br />as well as the routing behavior based on the model name extracted from the request content, etc.<br /><br />Currently, the filter is only implemented as an external process filter, which might be<br />extended to other types of filters in the future. See https://github.com/envoyproxy/ai-gateway/issues/90
- **llmRequestCosts**
  - **Type:** _[LLMRequestCost](#llmrequestcost) array_
  - **Required:** No
  - **Description:** LLMRequestCosts specifies how to capture the cost of the LLM-related request, notably the token usage.<br />The AI Gateway filter will capture each specified number and store it in the Envoy's dynamic<br />metadata per HTTP request. The namespaced key is "io.envoy.ai_gateway",<br /><br />For example, let's say we have the following LLMRequestCosts configuration:<br /><br />	llmRequestCosts:<br />	- metadataKey: llm_input_token<br />	  type: InputToken<br />	- metadataKey: llm_output_token<br />	  type: OutputToken<br />	- metadataKey: llm_total_token<br />	  type: TotalToken<br /><br />Then, with the following BackendTrafficPolicy of Envoy Gateway, you can have three<br />rate limit buckets for each unique x-user-id header value. One bucket is for the input token,<br />the other is for the output token, and the last one is for the total token.<br />Each bucket will be reduced by the corresponding token usage captured by the AI Gateway filter.<br /><br />	apiVersion: gateway.envoyproxy.io/v1alpha1<br />	kind: BackendTrafficPolicy<br />	metadata:<br />	  name: some-example-token-rate-limit<br />	  namespace: default<br />	spec:<br />	  targetRefs:<br />	  - group: gateway.networking.k8s.io<br />	     kind: HTTPRoute<br />	     name: usage-rate-limit<br />	  rateLimit:<br />	    type: Global<br />	    global:<br />	      rules:<br />	        - clientSelectors:<br />	            # Do the rate limiting based on the x-user-id header.<br />	            - headers:<br />	                - name: x-user-id<br />	                  type: Distinct<br />	          limit:<br />	            # Configures the number of "tokens" allowed per hour.<br />	            requests: 10000<br />	            unit: Hour<br />	          cost:<br />	            request:<br />	              from: Number<br />	              # Setting the request cost to zero allows to only check the rate limit budget,<br />	              # and not consume the budget on the request path.<br />	              number: 0<br />	            # This specifies the cost of the response retrieved from the dynamic metadata set by the AI Gateway filter.<br />	            # The extracted value will be used to consume the rate limit budget, and subsequent requests will be rate limited<br />	            # if the budget is exhausted.<br />	            response:<br />	              from: Metadata<br />	              metadata:<br />	                namespace: io.envoy.ai_gateway<br />	                key: llm_input_token<br />	        - clientSelectors:<br />	            - headers:<br />	                - name: x-user-id<br />	                  type: Distinct<br />	          limit:<br />	            requests: 10000<br />	            unit: Hour<br />	          cost:<br />	            request:<br />	              from: Number<br />	              number: 0<br />	            response:<br />	              from: Metadata<br />	              metadata:<br />	                namespace: io.envoy.ai_gateway<br />	                key: llm_output_token<br />	        - clientSelectors:<br />	            - headers:<br />	                - name: x-user-id<br />	                  type: Distinct<br />	          limit:<br />	            requests: 10000<br />	            unit: Hour<br />	          cost:<br />	            request:<br />	              from: Number<br />	              number: 0<br />	            response:<br />	              from: Metadata<br />	              metadata:<br />	                namespace: io.envoy.ai_gateway<br />	                key: llm_total_token


### AIServiceBackend



AIServiceBackend is a resource that represents a single backend for AIGatewayRoute.
A backend is a service that handles traffic with a concrete API specification.


A AIServiceBackend is "attached" to a Backend which is either a k8s Service or a Backend resource of the Envoy Gateway.


When a backend with an attached AIServiceBackend is used as a routing target in the AIGatewayRoute (more precisely, the
HTTPRouteSpec defined in the AIGatewayRoute), the ai-gateway will generate the necessary configuration to do
the backend specific logic in the final HTTPRoute.

_Appears in:_
- [AIServiceBackendList](#aiservicebackendlist)

- **apiVersion**
  - **Type:** _string_
  - **Value:** `aigateway.envoyproxy.io/v1alpha1`
- **kind**
  - **Type:** _string_
  - **Value:** `AIServiceBackend`
- **metadata**
  - **Type:** _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#objectmeta-v1-meta)_
  - **Required:** Yes
  - **Description:** Refer to Kubernetes API documentation for fields of `metadata`.
- **spec**
  - **Type:** _[AIServiceBackendSpec](#aiservicebackendspec)_
  - **Required:** Yes
  - **Description:** Spec defines the details of AIServiceBackend.


### AIServiceBackendList



AIServiceBackendList contains a list of AIServiceBackends.



- **apiVersion**
  - **Type:** _string_
  - **Value:** `aigateway.envoyproxy.io/v1alpha1`
- **kind**
  - **Type:** _string_
  - **Value:** `AIServiceBackendList`
- **metadata**
  - **Type:** _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#listmeta-v1-meta)_
  - **Required:** Yes
  - **Description:** Refer to Kubernetes API documentation for fields of `metadata`.
- **items**
  - **Type:** _[AIServiceBackend](#aiservicebackend) array_
  - **Required:** Yes
  - **Description:** 


### AIServiceBackendSpec



AIServiceBackendSpec details the AIServiceBackend configuration.

_Appears in:_
- [AIServiceBackend](#aiservicebackend)

- **schema**
  - **Type:** _[VersionedAPISchema](#versionedapischema)_
  - **Required:** Yes
  - **Description:** APISchema specifies the API schema of the output format of requests from<br />Envoy that this AIServiceBackend can accept as incoming requests.<br />Based on this schema, the ai-gateway will perform the necessary transformation for<br />the pair of AIGatewayRouteSpec.APISchema and AIServiceBackendSpec.APISchema.<br /><br />This is required to be set.
- **backendRef**
  - **Type:** _[BackendObjectReference](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1.BackendObjectReference)_
  - **Required:** Yes
  - **Description:** BackendRef is the reference to the Backend resource that this AIServiceBackend corresponds to.<br /><br />A backend can be of either k8s Service or Backend resource of Envoy Gateway.<br /><br />This is required to be set.
- **backendSecurityPolicyRef**
  - **Type:** _[LocalObjectReference](#localobjectreference)_
  - **Required:** No
  - **Description:** BackendSecurityPolicyRef is the name of the BackendSecurityPolicy resources this backend<br />is being attached to.


### APISchema

_Underlying type:_ _string_

APISchema defines the API schema.

_Appears in:_
- [VersionedAPISchema](#versionedapischema)

| Value | Description |
| ----- | ----------- |
| `OpenAI` | APISchemaOpenAI is the OpenAI schema.<br />https://github.com/openai/openai-openapi<br /> |
| `AWSBedrock` | APISchemaAWSBedrock is the AWS Bedrock schema.<br />https://docs.aws.amazon.com/bedrock/latest/APIReference/API_Operations_Amazon_Bedrock_Runtime.html<br /> |


### AWSCredentialsFile



AWSCredentialsFile specifies the credentials file to use for the AWS provider.
Envoy reads the secret file, and the profile to use is specified by the Profile field.

_Appears in:_
- [BackendSecurityPolicyAWSCredentials](#backendsecuritypolicyawscredentials)

- **secretRef**
  - **Type:** _[SecretObjectReference](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1.SecretObjectReference)_
  - **Required:** Yes
  - **Description:** SecretRef is the reference to the credential file.<br /><br />The secret should contain the AWS credentials file keyed on "credentials".
- **profile**
  - **Type:** _string_
  - **Required:** Yes
  - **Description:** Profile is the profile to use in the credentials file.


### AWSOIDCExchangeToken



AWSOIDCExchangeToken specifies credentials to obtain oidc token from a sso server.
For AWS, the controller will query STS to obtain AWS AccessKeyId, SecretAccessKey, and SessionToken,
and store them in a temporary credentials file.

_Appears in:_
- [BackendSecurityPolicyAWSCredentials](#backendsecuritypolicyawscredentials)

- **oidc**
  - **Type:** _[OIDC](#oidc)_
  - **Required:** Yes
  - **Description:** OIDC is used to obtain oidc tokens via an SSO server which will be used to exchange for temporary AWS credentials.
- **grantType**
  - **Type:** _string_
  - **Required:** No
  - **Description:** GrantType is the method application gets access token.
- **aud**
  - **Type:** _string_
  - **Required:** No
  - **Description:** Aud defines the audience that this ID Token is intended for.
- **awsRoleArn**
  - **Type:** _string_
  - **Required:** Yes
  - **Description:** AwsRoleArn is the AWS IAM Role with the permission to use specific resources in AWS account<br />which maps to the temporary AWS security credentials exchanged using the authentication token issued by OIDC provider.


### BackendSecurityPolicy



BackendSecurityPolicy specifies configuration for authentication and authorization rules on the traffic
exiting the gateway to the backend.

_Appears in:_
- [BackendSecurityPolicyList](#backendsecuritypolicylist)

- **apiVersion**
  - **Type:** _string_
  - **Value:** `aigateway.envoyproxy.io/v1alpha1`
- **kind**
  - **Type:** _string_
  - **Value:** `BackendSecurityPolicy`
- **metadata**
  - **Type:** _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#objectmeta-v1-meta)_
  - **Required:** Yes
  - **Description:** Refer to Kubernetes API documentation for fields of `metadata`.
- **spec**
  - **Type:** _[BackendSecurityPolicySpec](#backendsecuritypolicyspec)_
  - **Required:** Yes
  - **Description:** 


### BackendSecurityPolicyAPIKey



BackendSecurityPolicyAPIKey specifies the API key.

_Appears in:_
- [BackendSecurityPolicySpec](#backendsecuritypolicyspec)

- **secretRef**
  - **Type:** _[SecretObjectReference](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1.SecretObjectReference)_
  - **Required:** Yes
  - **Description:** SecretRef is the reference to the secret containing the API key.<br />ai-gateway must be given the permission to read this secret.<br />The key of the secret should be "apiKey".


### BackendSecurityPolicyAWSCredentials



BackendSecurityPolicyAWSCredentials contains the supported authentication mechanisms to access aws

_Appears in:_
- [BackendSecurityPolicySpec](#backendsecuritypolicyspec)

- **region**
  - **Type:** _string_
  - **Required:** Yes
  - **Description:** Region specifies the AWS region associated with the policy.
- **credentialsFile**
  - **Type:** _[AWSCredentialsFile](#awscredentialsfile)_
  - **Required:** No
  - **Description:** CredentialsFile specifies the credentials file to use for the AWS provider.
- **oidcExchangeToken**
  - **Type:** _[AWSOIDCExchangeToken](#awsoidcexchangetoken)_
  - **Required:** No
  - **Description:** OIDCExchangeToken specifies the oidc configurations used to obtain an oidc token. The oidc token will be<br />used to obtain temporary credentials to access AWS.


### BackendSecurityPolicyList



BackendSecurityPolicyList contains a list of BackendSecurityPolicy



- **apiVersion**
  - **Type:** _string_
  - **Value:** `aigateway.envoyproxy.io/v1alpha1`
- **kind**
  - **Type:** _string_
  - **Value:** `BackendSecurityPolicyList`
- **metadata**
  - **Type:** _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#listmeta-v1-meta)_
  - **Required:** Yes
  - **Description:** Refer to Kubernetes API documentation for fields of `metadata`.
- **items**
  - **Type:** _[BackendSecurityPolicy](#backendsecuritypolicy) array_
  - **Required:** Yes
  - **Description:** 


### BackendSecurityPolicySpec



BackendSecurityPolicySpec specifies authentication rules on access the provider from the Gateway.
Only one mechanism to access a backend(s) can be specified.


Only one type of BackendSecurityPolicy can be defined.

_Appears in:_
- [BackendSecurityPolicy](#backendsecuritypolicy)

- **type**
  - **Type:** _[BackendSecurityPolicyType](#backendsecuritypolicytype)_
  - **Required:** Yes
  - **Description:** Type specifies the auth mechanism used to access the provider. Currently, only "APIKey", AND "AWSCredentials" are supported.
- **apiKey**
  - **Type:** _[BackendSecurityPolicyAPIKey](#backendsecuritypolicyapikey)_
  - **Required:** No
  - **Description:** APIKey is a mechanism to access a backend(s). The API key will be injected into the Authorization header.
- **awsCredentials**
  - **Type:** _[BackendSecurityPolicyAWSCredentials](#backendsecuritypolicyawscredentials)_
  - **Required:** No
  - **Description:** AWSCredentials is a mechanism to access a backend(s). AWS specific logic will be applied.


### BackendSecurityPolicyType

_Underlying type:_ _string_

BackendSecurityPolicyType specifies the type of auth mechanism used to access a backend.

_Appears in:_
- [BackendSecurityPolicySpec](#backendsecuritypolicyspec)

| Value | Description |
| ----- | ----------- |
| `APIKey` |  |
| `AWSCredentials` |  |


### LLMRequestCost



LLMRequestCost configures each request cost.

_Appears in:_
- [AIGatewayRouteSpec](#aigatewayroutespec)

- **metadataKey**
  - **Type:** _string_
  - **Required:** Yes
  - **Description:** MetadataKey is the key of the metadata to store this cost of the request.
- **type**
  - **Type:** _[LLMRequestCostType](#llmrequestcosttype)_
  - **Required:** Yes
  - **Description:** Type specifies the type of the request cost. The default is "OutputToken",<br />and it uses "output token" as the cost. The other types are "InputToken", "TotalToken",<br />and "CEL".
- **celExpression**
  - **Type:** _string_
  - **Required:** No
  - **Description:** CELExpression is the CEL expression to calculate the cost of the request.<br />The CEL expression must return a signed or unsigned integer. If the<br />return value is negative, it will be error.<br /><br />The expression can use the following variables:<br /><br />	* model: the model name extracted from the request content. Type: string.<br />	* backend: the backend name in the form of "name.namespace". Type: string.<br />	* input_tokens: the number of input tokens. Type: unsigned integer.<br />	* output_tokens: the number of output tokens. Type: unsigned integer.<br />	* total_tokens: the total number of tokens. Type: unsigned integer.<br /><br />For example, the following expressions are valid:<br /><br />	* "model == 'llama' ?  input_tokens + output_token * 0.5 : total_tokens"<br />	* "backend == 'foo.default' ?  input_tokens + output_tokens : total_tokens"<br />	* "input_tokens + output_tokens + total_tokens"<br />	* "input_tokens * output_tokens"


### LLMRequestCostType

_Underlying type:_ _string_

LLMRequestCostType specifies the type of the LLMRequestCost.

_Appears in:_
- [LLMRequestCost](#llmrequestcost)

| Value | Description |
| ----- | ----------- |
| `InputToken` | LLMRequestCostTypeInputToken is the cost type of the input token.<br /> |
| `OutputToken` | LLMRequestCostTypeOutputToken is the cost type of the output token.<br /> |
| `TotalToken` | LLMRequestCostTypeTotalToken is the cost type of the total token.<br /> |
| `CEL` | LLMRequestCostTypeCEL is for calculating the cost using the CEL expression.<br /> |


### VersionedAPISchema



VersionedAPISchema defines the API schema of either AIGatewayRoute (the input) or AIServiceBackend (the output).


This allows the ai-gateway to understand the input and perform the necessary transformation
depending on the API schema pair (input, output).


Note that this is vendor specific, and the stability of the API schema is not guaranteed by
the ai-gateway, but by the vendor via proper versioning.

_Appears in:_
- [AIGatewayRouteSpec](#aigatewayroutespec)
- [AIServiceBackendSpec](#aiservicebackendspec)

- **name**
  - **Type:** _[APISchema](#apischema)_
  - **Required:** Yes
  - **Description:** Name is the name of the API schema of the AIGatewayRoute or AIServiceBackend.
- **version**
  - **Type:** _string_
  - **Required:** Yes
  - **Description:** Version is the version of the API schema.



[Back to Packages](#api_references)
