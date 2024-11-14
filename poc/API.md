+++
title = "API Reference"
+++


## Packages
- [aigateway.envoyproxy.io/v1alpha1](#aigatewayenvoyproxyiov1alpha1)


## aigateway.envoyproxy.io/v1alpha1

Package v1alpha1 contains API schema definitions for the aigateway.envoyproxy.io
API group.


### Resource Types
- [LLMRoute](#llmroute)
- [LLMRouteList](#llmroutelist)



#### LLMBackend



LLMBackend describes the details of a LLM backend, e.g. OpenAI, AWS Bedrock, etc.
Each backend can have its own rate limit policy as well as request/response transformation.
This configures how to perform the LLM specific operations on the requests and responses to and from
that referenced backend.

_Appears in:_
- [LLMRouteSpec](#llmroutespec)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `backendRef` | _[BackendRef](#backendref)_ |  true  | BackendRef is the reference to the Backend resource that this LLMBackend corresponds to.<br /><br />The backend can be of either k8s Service or [egv1a1.Backend] kind plus the resource must be in the same namespace as the LLMRoute.<br />For the sake of simplicity, currently the same backend cannot be referenced by multiple LLMBackend(s). |
| `schema` | _[LLMRouteAPISchema](#llmrouteapischema)_ |  true  | Schema specifies the schema of the LLM backend.<br />Currently, only "OpenAI" and "AWSBedrock" are supported. This defaults to "OpenAI". |
| `providerPolicy` | _[LLMProviderPolicy](#llmproviderpolicy)_ |  true  | ProviderPolicy specifies the provider specific configuration such as. |
| `trafficPolicy` | _[LLMTrafficPolicy](#llmtrafficpolicy)_ |  true  | TrafficPolicy controls the flow of traffic to the backend. |


#### LLMPolicyRateLimitHeaderMatch



LLMPolicyRateLimitHeaderMatch defines the match attributes within the HTTP Headers of the request.

_Appears in:_
- [LLMTrafficPolicyRateLimitRule](#llmtrafficpolicyratelimitrule)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `type` | _[LLMPolicyRateLimitStringMatchType](#llmpolicyratelimitstringmatchtype)_ |  true  | Type specifies how to match against the value of the header. |
| `name` | _string_ |  true  | Name of the HTTP header. |
| `value` | _string_ |  false  | Value within the HTTP header. Due to the<br />case-insensitivity of header names, "foo" and "Foo" are considered equivalent.<br />Do not set this field when Type="Distinct", implying matching on any/all unique<br />values within the header. |


#### LLMPolicyRateLimitMetadataMatch



LLMPolicyRateLimitMetadataMatch defines the match attributes within the metadata from dynamic or route entry.
The match will be ignored if the metadata is not present.

_Appears in:_
- [LLMTrafficPolicyRateLimitRule](#llmtrafficpolicyratelimitrule)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `type` | _[LLMPolicyRateLimitMetadataMatchMetadataType](#llmpolicyratelimitmetadatamatchmetadatatype)_ |  true  | Type specifies the type of metadata to match. |
| `name` | _string_ |  true  | Name specifies the key of the metadata to match. |
| `paths` | _string array_ |  false  | Paths specifies the value of the metadata to match. |
| `defaultValue` | _string_ |  false  | DefaultValue specifies an optional value to use if “metadata“ is empty.<br />Default value is "unknown". |


#### LLMPolicyRateLimitMetadataMatchMetadataType

_Underlying type:_ _string_

LLMPolicyRateLimitMetadataMatchMetadataType specifies the type of metadata to match.

_Appears in:_
- [LLMPolicyRateLimitMetadataMatch](#llmpolicyratelimitmetadatamatch)

| Value | Description |
| ----- | ----------- |
| `Dynamic` | MetadataTypeDynamic specifies that the source of metadata is dynamic.<br /> | 




#### LLMPolicyRateLimitStringMatchType

_Underlying type:_ _string_

LLMPolicyRateLimitStringMatchType specifies the semantics of how string values should be compared.
Valid LLMPolicyRateLimitStringMatchType values are "Exact", "RegularExpression", and "Distinct".

_Appears in:_
- [LLMPolicyRateLimitHeaderMatch](#llmpolicyratelimitheadermatch)
- [LLMPolicyRateLimitModelNameMatch](#llmpolicyratelimitmodelnamematch)

| Value | Description |
| ----- | ----------- |
| `Exact` | HeaderMatchExact matches the exact value of the Value field against the value of<br />the specified HTTP Header.<br /> | 
| `RegularExpression` | HeaderMatchRegularExpression matches a regular expression against the value of the<br />specified HTTP Header. The regex string must adhere to the syntax documented in<br />https://github.com/google/re2/wiki/Syntax.<br /> | 
| `Distinct` | HeaderMatchDistinct matches any and all possible unique values encountered in the<br />specified HTTP Header. Note that each unique value will receive its own rate limit<br />bucket.<br />Note: This is only supported for Global Rate Limits.<br /> | 


#### LLMPolicyRateLimitType

_Underlying type:_ _string_

LLMPolicyRateLimitType specifies the type of rate limit.
Valid RateLimitType values are "Request" and "Token".

_Appears in:_
- [LLMPolicyRateLimitValue](#llmpolicyratelimitvalue)

| Value | Description |
| ----- | ----------- |
| `Request` | RateLimitTypeRequest specifies the rate limit to be based on the number of requests.<br /> | 
| `Token` | RateLimitTypeToken specifies the rate limit to be based on the number of tokens.<br /> | 


#### LLMPolicyRateLimitUnit

_Underlying type:_ _string_

LLMPolicyRateLimitUnit specifies the intervals for setting rate limits.
Valid RateLimitUnit values are "Second", "Minute", "Hour", and "Day".

_Appears in:_
- [LLMPolicyRateLimitValue](#llmpolicyratelimitvalue)

| Value | Description |
| ----- | ----------- |
| `Second` | RateLimitUnitSecond specifies the rate limit interval to be 1 second.<br /> | 
| `Minute` | RateLimitUnitMinute specifies the rate limit interval to be 1 minute.<br /> | 
| `Hour` | RateLimitUnitHour specifies the rate limit interval to be 1 hour.<br /> | 
| `Day` | RateLimitUnitDay specifies the rate limit interval to be 1 day.<br /> | 


#### LLMPolicyRateLimitValue



LLMPolicyRateLimitValue defines the limits for rate limiting.

_Appears in:_
- [LLMTrafficPolicyRateLimitRule](#llmtrafficpolicyratelimitrule)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `type` | _[LLMPolicyRateLimitType](#llmpolicyratelimittype)_ |  true  | Type specifies the type of rate limit. |
| `quantity` | _integer_ |  true  | Quantity specifies the number of requests or tokens allowed in the given interval. |
| `unit` | _[LLMPolicyRateLimitUnit](#llmpolicyratelimitunit)_ |  true  | Unit specifies the interval for the rate limit. |


#### LLMProviderAPIKey



LLMProviderAPIKey specifies the API key.

_Appears in:_
- [LLMProviderPolicy](#llmproviderpolicy)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `type` | _[LLMProviderAPIKeyType](#llmproviderapikeytype)_ |  true  | Type specifies the type of the API key. Currently, "SecretRef" and "Inline" are supported.<br />This defaults to "SecretRef". |
| `secretRef` | _[SecretObjectReference](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1.SecretObjectReference)_ |  false  | SecretRef is the reference to the secret containing the API key.<br />ai-gateway must be given the permission to read this secret.<br />The key of the secret should be "apiKey". |
| `inline` | _string_ |  false  | Inline specifies the inline API key. |


#### LLMProviderAPIKeyType

_Underlying type:_ _string_

LLMProviderAPIKeyType specifies the type of LLMProviderAPIKey.

_Appears in:_
- [LLMProviderAPIKey](#llmproviderapikey)

| Value | Description |
| ----- | ----------- |
| `SecretRef` |  | 
| `Inline` |  | 


#### LLMProviderAWSBedrock



LLMProviderAWSBedrock specifies the AWS Bedrock specific configuration.

_Appears in:_
- [LLMProviderPolicy](#llmproviderpolicy)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `region` | _string_ |  true  | Region is the AWS region to use. |
| `type` | _[LLMProviderAWSBedrockType](#llmproviderawsbedrocktype)_ |  true  | Type specifies the type of the AWS Bedrock provider. Currently, "InlineCredential" and "CredentialsFile" are supported.<br />This defaults to "InlineCredential". |
| `signingAlgorithm` | _string_ |  true  | SigningAlgorithm specifies the algorithm to use for signing the request.<br />AWS_SIGV4 or AWS_SIGV4A are supported. This is optional and defaults to AWS_SIGV4. |
| `hostRewrite` | _string_ |  false  | HostRewrite specifies the real AWE Bedrock host (e.g. bedrock-runtime.us-east-1.amazonaws.com)<br />to rewrite in the request before the AWS request signing.<br />Usually, this matches the hostname of the backend, but it can be different in case of a proxy.<br /><br />This defaults to "bedrock-runtime.$\{Region\}.amazonaws.com". |
| `inlineCredential` | _[LLMProviderAWSBedrockInlineCredential](#llmproviderawsbedrockinlinecredential)_ |  false  | InlineCredential specifies the inline credential to use for the AWS Bedrock provider. |
| `credentialsFile` | _[LLMProviderAWSBedrockCredentialsFile](#llmproviderawsbedrockcredentialsfile)_ |  false  | CredentialsFile specifies the credentials file to use for the AWS Bedrock provider. |


#### LLMProviderAWSBedrockCredentialsFile



LLMProviderAWSBedrockCredentialsFile specifies the credentials file to use for the AWS Bedrock provider.
Envoy reads the credentials from the file pointed by the Path field, and the profile to use is specified by the Profile field.

_Appears in:_
- [LLMProviderAWSBedrock](#llmproviderawsbedrock)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `path` | _string_ |  false  | Path is the path to the credentials file.<br />This defaults to "~/.aws/credentials". |
| `profile` | _string_ |  false  | Profile is the profile to use in the credentials file.<br />This defaults to "default". |


#### LLMProviderAWSBedrockInlineCredential



LLMProviderAWSBedrockInlineCredential specifies the inline credentials.

_Appears in:_
- [LLMProviderAWSBedrock](#llmproviderawsbedrock)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `accessKeyID` | _string_ |  true  | AccessKeyID is the AWS access key ID. |
| `secretAccessKey` | _string_ |  true  | SecretAccessKey is the AWS secret access key. |
| `sessionToken` | _string_ |  false  | SessionToken is the AWS session token. |


#### LLMProviderAWSBedrockType

_Underlying type:_ _string_

LLMProviderAWSBedrockType specifies the type of the AWS Bedrock provider.

_Appears in:_
- [LLMProviderAWSBedrock](#llmproviderawsbedrock)

| Value | Description |
| ----- | ----------- |
| `InlineCredential` | LLMProviderAWSBedrockTypeInlineCredential specifies the inline credentials.<br /> | 
| `CredentialsFile` |  | 


#### LLMProviderPolicy



LLMProviderPolicy specifies the provider specific configuration.


This is a provider specific-configuration, e.g.AWS Bedrock, Azure etc.

_Appears in:_
- [LLMBackend](#llmbackend)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `type` | _[LLMProviderType](#llmprovidertype)_ |  true  | Type specifies the type of the provider. Currently, only "APIKey" and "AWSBedrock" are supported. |
| `apiKey` | _[LLMProviderAPIKey](#llmproviderapikey)_ |  false  | APIKey specific configuration. The API key will be injected into the Authorization header. |
| `awsBedrock` | _[LLMProviderAWSBedrock](#llmproviderawsbedrock)_ |  false  | AWS Bedrock specific configuration. Note that at most one AWS Bedrock policy can be specified.<br />Otherwise, the Envoy cannot determine which credentials to use.<br /><br />AWS Bedrock provider utilizes the EnvoyProxy resource to attach the environment variables for the AWS credentials<br />to the Envoy container. The controller checks the existence of the EnvoyProxy resource attached to each<br />target Gateway and creates a new one if it doesn't exist. If it exists, it updates the existing one.<br />If the new EnvoyProxy resource is created, the controller sets the owner reference to the LLMRoute.<br />The created EnvoyProxy resource will have the same namespace and name as in the form of "$\{LLMRoute's name\}-$\{Each target Gateway's name\}".<br />It is user's responsibility to attach the Gateway resource to the EnvoyProxy resource via "infrastructure" field. See<br />https://github.com/envoyproxy/gateway/blob/a6590bf81463d5cfeadc817f5238a01507ab1a9b/test/e2e/testdata/tracing-zipkin.yaml#L15-L19<br />for an example.<br /><br />This EnvoyProxy resource manipulation is necessary because the AWS signing filter doesn't support<br />dynamic configuration, but it can be configured only by the environment variables.<br />https://github.com/envoyproxy/envoy/blob/0ad67a1d7f8f6352e8c2b7abcce627d8f212c081/source/extensions/common/aws/credentials_provider_impl.cc#L775<br />See the upstream issue: https://github.com/envoyproxy/envoy/issues/36109 for more details. Once this issue is resolved,<br />we can remove this EnvoyProxy resource manipulation. |


#### LLMProviderType

_Underlying type:_ _string_

LLMProviderType specifies the type of the LLMProviderPolicy.

_Appears in:_
- [LLMProviderPolicy](#llmproviderpolicy)

| Value | Description |
| ----- | ----------- |
| `APIKey` |  | 
| `AWSBedrock` |  | 


#### LLMRoute



LLMRoute is the Schema for controlling the LLM provider routing.
This corresponds to HTTPRoute in the Gateway API, and is an abstraction layer on top of it.


All resources created by a LLMRoute have the same namespace as LLMRoute as well as owner references set to the LLMRoute.


This corresponds to one gateway.networking.k8s.io/v1.HTTPRoute resource with the exact header
match rules where "x-ai-gateway-llm-backend" header is set to the name of each LLMBackend.


"x-ai-gateway-llm-backend" is a reserved header and should be directly set by the client or the earlier filters,
e.g. additional external filters like the ExtProc field in the LLMRoute.


For example, if [LLMRouteSpec.Backends] has two backends named "openai" and "ollama", the controller will create an HTTPRoute
with the following rules:


	apiVersion: gateway.networking.k8s.io/v1
	kind: HTTPRoute
	metadata:
	  name: llmroute-${LLMRoute.name}
	  namespace: ${LLMRoute.namespace}
	spec:
	  ..... # Omitting other fields for brevity.
	  rules:
	  - backendRefs:
	    - kind: Service
	      name: ollama
	      port: 80
	    matches:
	    - headers:
	      - name: x-ai-gateway-llm-backend
	        exact: ollama
	  - backendRefs:
	    - group: gateway.envoyproxy.io
	      kind: Backend
	      name: openai
	    matches:
	    - headers:
	      - name: x-ai-gateway-llm-backend
	        exact: openai


Optionally, this might also create or update an EnvoyProxy resource for each target Gateway.


Implementation note: The controller generates a corresponding Deployment and Service for the external processor.
These resources are created in the same namespace as the LLMRoute.
The name of the Deployment and Service is "ai-gateway-extproc-${LLMRoute.name}-${LLMRoute.namespace}".
The corresponding external processor is started with the LLMRoute configuration to control the behavior.
For all Backends, requests are sent to that external processor for processing.

_Appears in:_
- [LLMRouteList](#llmroutelist)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `apiVersion` | _string_ | |`aigateway.envoyproxy.io/v1alpha1`
| `kind` | _string_ | |`LLMRoute`
| `metadata` | _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#objectmeta-v1-meta)_ |  true  | Refer to Kubernetes API documentation for fields of `metadata`. |
| `spec` | _[LLMRouteSpec](#llmroutespec)_ |  true  | Spec defines the details of the LLM policy. |


#### LLMRouteAPISchema

_Underlying type:_ _string_

LLMRouteAPISchema defines the API schema of the LLM route. This specifies either client or backend API schema.

_Appears in:_
- [LLMBackend](#llmbackend)
- [LLMRouteSpec](#llmroutespec)

| Value | Description |
| ----- | ----------- |
| `OpenAI` | LLMRouteAPISchemaOpenAI is the OpenAI schema.<br />https://platform.openai.com/docs/overview<br /> | 
| `AWSBedrock` | LLMRouteAPISchemaAWSBedrock is the AWS Bedrock schema.<br />https://docs.aws.amazon.com/bedrock/latest/APIReference/API_Operations_Amazon_Bedrock_Runtime.html<br /> | 


#### LLMRouteExtProcConfig



LLMRouteExtProcConfig contains the configuration for the Deployment of the ai-gateway's external processor.

_Appears in:_
- [LLMRouteSpec](#llmroutespec)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `replicas` | _integer_ |  false  | Replicas is the number of replicas of the external processor. Defaults to 1. |
| `resources` | _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#resourcerequirements-v1-core)_ |  false  | Resources is the resource requirements for the external processor. |


#### LLMRouteList



LLMRouteList contains a list of LLMTrafficPolicy



| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `apiVersion` | _string_ | |`aigateway.envoyproxy.io/v1alpha1`
| `kind` | _string_ | |`LLMRouteList`
| `metadata` | _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.29/#listmeta-v1-meta)_ |  true  | Refer to Kubernetes API documentation for fields of `metadata`. |
| `items` | _[LLMRoute](#llmroute) array_ |  true  |  |


#### LLMRouteSpec



LLMRouteSpec details the LLMRoute configuration.

_Appears in:_
- [LLMRoute](#llmroute)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `extProcConfig` | _[LLMRouteExtProcConfig](#llmrouteextprocconfig)_ |  false  | ExtProcConfig contains the configuration for the Deployment of the ai-gateway's external processor. |
| `targetRefs` | _[LocalPolicyTargetReferenceWithSectionName](https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io/v1alpha2.LocalPolicyTargetReferenceWithSectionName) array_ |  false  | TargetRefs are the names of the Gateway resources this policy is being attached to.<br />The namespace is "local", i.e. the same namespace as the LLMRoute.<br />A Gateway cannot have more than one LLMRoute attached to it. TODO: enforce this rule in the controller. |
| `backends` | _[LLMBackend](#llmbackend) array_ |  false  | Backends lists the details of the LLM backends available to the Gateway. |
| `extProc` | _[ExtProc](https://gateway.envoyproxy.io/docs/api/extension_types/#extproc) array_ |  false  | ExtProc an ordered list of external processing filters that should be added to the envoy filter chain.<br />This is non-backend specific and applies to all backends. In other words, these filters are applied to all<br />requests before backend specific filters are applied (the ones specified in the LLMBackend).<br /><br />This is especially useful for selecting the backend based on the custom logic. Since routing decisions are<br />made via the "x-ai-gateway-llm-backend" header, the external filters can set this header based on the custom logic. |
| `schema` | _[LLMRouteAPISchema](#llmrouteapischema)_ |  false  | Schema specifies the API schema of the LLM route. In other words, this specifies the API schema that clients<br />should use to interact with the LLM route.<br /><br />Currently, only "OpenAI" is supported, and it defaults to "OpenAI". |


#### LLMTrafficPolicy





_Appears in:_
- [LLMBackend](#llmbackend)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `rateLimit` | _[LLMTrafficPolicyRateLimit](#llmtrafficpolicyratelimit)_ |  true  | RateLimit defines the usual rate limit policy for this backend. |


#### LLMTrafficPolicyRateLimit





_Appears in:_
- [LLMTrafficPolicy](#llmtrafficpolicy)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `rules` | _[LLMTrafficPolicyRateLimitRule](#llmtrafficpolicyratelimitrule) array_ |  true  | Rules defines the rate limit rules. |


#### LLMTrafficPolicyRateLimitRule



LLMTrafficPolicyRateLimitRule defines the details of the rate limit policy.

_Appears in:_
- [LLMTrafficPolicyRateLimit](#llmtrafficpolicyratelimit)

| Field | Type | Required | Description |
| ---   | ---  | ---      | ---         |
| `headers` | _[LLMPolicyRateLimitHeaderMatch](#llmpolicyratelimitheadermatch) array_ |  true  | Headers is a list of request headers to match. Multiple header values are ANDed together,<br />meaning, a request MUST match all the specified headers.<br />At least one of headers or sourceCIDR condition must be specified. |
| `metadata` | _[LLMPolicyRateLimitMetadataMatch](#llmpolicyratelimitmetadatamatch) array_ |  true  | Refer to Kubernetes API documentation for fields of `metadata`. |
| `limits` | _[LLMPolicyRateLimitValue](#llmpolicyratelimitvalue) array_ |  true  | Limits holds the rate limit values.<br />This limit is applied for traffic flows when the selectors<br />compute to True, causing the request to be counted towards the limit.<br />The limit is enforced and the request is ratelimited, i.e. a response with<br />429 HTTP status code is sent back to the client when<br />the selected requests have reached the limit. |


