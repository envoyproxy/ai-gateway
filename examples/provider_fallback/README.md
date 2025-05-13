This example demonstrates how to configure the "provider" fallback per routing rule.
Specifically, this configures AIGatewayRoute to route requests to an always failing backend and then fallback to a healthy AWS Bedrock backend.
The fallback behavior is achieved with [`BackendTrafficPolicy` API](https://gateway.envoyproxy.io/contributions/design/backend-traffic-policy/) of Envoy Gateway which is attached to a generated HTTPRoute.
