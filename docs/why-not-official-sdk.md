## Why not use the "official SDKs"?

In [`internal/apischema`](../internal/apischema), we have defined our own data structures for various providers like OpenAI, Azure OpenAI, Anthropic, ete., for our translation logic as well as for observability purposes.

> Note that there might be some official SDK usage remaining in some places, which is not consistent with this explanation. We are planning to remove them gradually.

It is a FAQ to ask why we are not using the "official SDKs" provided by the providers themselves. The reasons why we are not using the "official SDKs" are:

- Cross provider compatibility we want is not a provider's concern. In other words, they do not care about edge cases we want to handle.
  - E.g. https://github.com/openai/openai-go/issues/484 explains even "official SDKs" might not be compatible with the real responses.
- Maintaining a few struct for only supported endpoints in this project is not that hard. We are not trying to cover all endpoints provided by the providers.
- Usually AI providers auto generate SDKs from an ambiguous OpenAPI spec (at least openai/anthropic), which has weird performance cost.
  - For example, Go's json marshal/unmarshal performance is not that good compared to other languages. Hence, we are looking for more optimized way to serialize/deserialize payloads.
    However, if the definition is auto generated from OpenAPI spec, it is hard to optimize the serialization/deserialization logic as well as sometimes makes it impossible to use the highly optimized libraries like `goccy/go-json`.
- Using them making it hard for us to add vendor specific fields. Sometimes we want to add vendor specific fields in a nested data structure, which is not possible if we rely on the external packages.

On the other hand, we use official SDKs for testing purposes to make sure our code works as expected.

Previous discussions:

- https://github.com/envoyproxy/ai-gateway/issues/995
- https://github.com/envoyproxy/ai-gateway/pull/1147
