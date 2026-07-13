# LLM Arbitrary Routing

Enables arbitrary per-request backend selection and fallback for LLM requests via the `x-ai-eg-routing-plan` header.

## Overview

A custom ext_proc can set a routing plan header to control which backends are tried and in what order. Useful for:
- **Provider fallback** — Try Azure OpenAI, fall back to GCP VertexAI, then AWS Bedrock
- **Cost optimization** — Route to cheaper providers first
- **Capacity management** — Direct traffic based on backend availability

## Architecture

```
Client Request
    │
    ▼
┌─────────────────────────────────────────────────────┐
│  Your ext_proc (runs first in filter chain)          │
│  Sets: x-ai-eg-routing-plan: <base64 JSON>          │
│  {"backends":["azure-primary","gcp-ptu"],            │
│   "fallbackEnabled":true}                            │
└──────────────┬──────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────┐
│  AI Gateway ext_proc (router filter)                 │
│  • Parses body, extracts model                       │
│  • Reads + stores routing plan                       │
└──────────────┬──────────────────────────────────────┘
               │
               ▼
           Envoy routes to single DFP cluster
               │
               ▼
┌─────────────────────────────────────────────────────┐
│  AI Gateway ext_proc (upstream filter) — Attempt 1   │
│  • Picks backends[0] from plan                       │
│  • Translates body + applies auth for that backend   │
│  • Sets :authority + :path for DFP                   │
└──────────────┬──────────────────────────────────────┘
               │
               ▼
        DFP resolves hostname → sends request
               │
               ▼ (on failure, Envoy retry triggers)
┌─────────────────────────────────────────────────────┐
│  AI Gateway ext_proc (upstream filter) — Attempt 2   │
│  • Picks backends[1] from plan                       │
│  • Re-translates original body for new backend       │
│  • Sets new :authority + :path                       │
└──────────────┬──────────────────────────────────────┘
               │
               ▼
        DFP resolves new hostname → sends request → response to client
```

## Routing Plan Header

```
x-ai-eg-routing-plan: <base64-encoded JSON>
```

### Format

```json
{
  "backends": ["azure-primary", "gcp-ptu", "aws-bedrock"],
  "fallbackEnabled": true
}
```

- **`backends`** — Ordered list of backend names matching backend config
- **`fallbackEnabled`** — When `false`, only `backends[0]` is used (default: `true`)
- Requests without this header use standard Envoy routing (fully backward compatible)

## Quick Start

### 1. Configure Backends

Edit `filter-config.yaml` with your backends:

```yaml
backends:
  - name: "azure-primary"
    schema: {name: "AzureOpenAI", prefix: "v1"}
    host: "snc-oai-llmproxy-dev-eastus2.openai.azure.com"
    backendPath: "/openai/deployments/gpt-4o/chat/completions?api-version=2024-10-21"
    auth:
      azureAPIKey:
        key: "your-azure-key"

  - name: "gcp-ptu"
    schema: {name: "GCPVertexAI", prefix: "v1beta1"}
    host: "us-central1-aiplatform.googleapis.com"
    backendPath: "/v1beta1/projects/my-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent"
    auth:
      gcp:
        credentialFilePath: "/etc/ai-gateway/secrets/gcp-sa.json"
```

Each backend must have:
- **name** — Unique identifier
- **schema** — API schema (AzureOpenAI, GCPVertexAI, AWSBedrock, etc.)
- **host** — Hostname for DFP routing
- **backendPath** — API path on the backend
- **auth** — Authentication config

### 2. Configure Envoy

Use single DFP cluster with router + upstream ext_proc filters. See [Envoy Config Template](#envoy-config-template) below.

### 3. Send Requests with Routing Plan

Use any client example:

```bash
python clients/example_python.py
node clients/example_javascript.js
go run clients/example_go.go
```

## Examples

### Python (OpenAI SDK)

```python
import base64
import json
from openai import OpenAI

plan = {
    "backends": ["azure-primary", "gcp-ptu"],
    "fallbackEnabled": True
}
encoded_plan = base64.b64encode(json.dumps(plan).encode()).decode()

client = OpenAI(api_key="...", base_url="http://localhost:8080/v1")
response = client.chat.completions.create(
    model="gpt-4",
    messages=[{"role": "user", "content": "Hello"}],
    extra_headers={"x-ai-eg-routing-plan": encoded_plan}
)
```

### JavaScript (fetch)

```javascript
const plan = {
  backends: ["azure-primary", "gcp-ptu"],
  fallbackEnabled: true
};
const encodedPlan = btoa(JSON.stringify(plan));

fetch("http://localhost:8080/v1/chat/completions", {
  method: "POST",
  headers: {
    "x-ai-eg-routing-plan": encodedPlan,
    "Content-Type": "application/json"
  },
  body: JSON.stringify({
    model: "gpt-4",
    messages: [{ role: "user", content: "Hello" }]
  })
});
```

### Go (net/http)

```go
plan := map[string]interface{}{
	"backends": []string{"azure-primary", "gcp-ptu"},
	"fallbackEnabled": true,
}
planJSON, _ := json.Marshal(plan)
encodedPlan := base64.StdEncoding.EncodeToString(planJSON)

req, _ := http.NewRequest("POST", "http://localhost:8080/v1/chat/completions", body)
req.Header.Set("x-ai-eg-routing-plan", encodedPlan)
http.DefaultClient.Do(req)
```

## Retry and Fallback

Cross-provider fallback works because the single DFP cluster handles all backends.
Each retry attempt re-enters the upstream ext_proc, which picks the next backend from the routing plan.

```
Attempt 1: :authority=azure.openai.com     → DFP → Azure (503)
Attempt 2: :authority=aiplatform.google.com → DFP → GCP   (200) ✓
```

### Requirements

- Envoy `retry_policy.num_retries` must be ≥ `len(backends) - 1`
- `retry_on` must include the status codes that should trigger fallback

## Envoy Config Template

Single DFP cluster with router + upstream ext_proc filters:

```yaml
static_resources:
  listeners:
    - name: listener_0
      address:
        socket_address: {address: 0.0.0.0, port_value: 8080}
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: ingress
                codec_type: AUTO
                scheme_header_transformation:
                  match_upstream: true
                route_config:
                  name: local_route
                  virtual_hosts:
                    - name: ai_gateway
                      domains: ["*"]
                      routes:
                        - match: {prefix: "/"}
                          route:
                            cluster: dfp_cluster
                            timeout: 120s
                            retry_policy:
                              retry_on: "5xx,reset,connect-failure"
                              num_retries: 3
                http_filters:
                  - name: envoy.filters.http.ext_proc/aigateway
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
                      grpc_service:
                        envoy_grpc: {cluster_name: ai_gateway_extproc}
                        timeout: 30s
                      processing_mode:
                        request_header_mode: SEND
                        request_body_mode: BUFFERED
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router

  clusters:
    # Single DFP cluster
    - name: dfp_cluster
      connect_timeout: 10s
      lb_policy: CLUSTER_PROVIDED
      cluster_type:
        name: envoy.clusters.dynamic_forward_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig
          dns_cache_config:
            name: dynamic_forward_proxy_cache
            dns_lookup_family: V4_ONLY
      transport_socket:
        name: envoy.transport_sockets.tls
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
          common_tls_context:
            validation_context:
              trusted_ca: {filename: /etc/ssl/certs/ca-certificates.crt}
      typed_extension_protocol_options:
        envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
          "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
          explicit_http_config:
            http_protocol_options: {}
          http_filters:
            - name: envoy.filters.http.ext_proc/aigateway
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
                grpc_service:
                  envoy_grpc: {cluster_name: ai_gateway_extproc}
                  timeout: 30s
                processing_mode:
                  request_header_mode: SEND
                  request_body_mode: NONE
                  response_header_mode: SKIP
                  response_body_mode: NONE

    - name: ai_gateway_extproc
      connect_timeout: 5s
      type: STATIC
      lb_policy: ROUND_ROBIN
      typed_extension_protocol_options:
        envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
          "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
          explicit_http_config:
            http2_protocol_options: {}
      load_assignment:
        cluster_name: ai_gateway_extproc
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address: {address: 127.0.0.1, port_value: 1063}
```

## Private Endpoints

For backends reachable through VPC Endpoint or Private Link, use Pod `hostAliases`:

```yaml
spec:
  hostAliases:
    - ip: "10.142.13.17"
      hostnames: ["snc-oai-llmproxy-dev-eastus2.openai.azure.com"]
    - ip: "10.103.13.58"
      hostnames: ["bedrock-runtime.us-west-2.amazonaws.com"]
    - ip: "10.31.31.1"
      hostnames: ["us-central1-aiplatform.googleapis.com"]
```

## Implementation Details

| File | Change |
|---|---|
| `internalapi/internalapi.go` | `LLMRoutingPlanHeader` constant, `RoutingPlan` struct |
| `filterapi/filterconfig.go` | `Host`, `BackendPath` fields on `Backend` |
| `extproc/processor.go` | `routingPlanProvider` interface |
| `extproc/processor_impl.go` | Parse routing plan in `ProcessRequestBody`; set `:authority`/`:path` in `ProcessRequestHeaders` |
| `extproc/server.go` | `resolveBackendFromPlan` helper; routing plan check in `setBackend` |

### Key Behaviors

- **Backward compatible** — Requests without the header use standard Envoy routing
- **Deterministic fallback** — `backends[0]` on attempt 1, `backends[1]` on retry, etc.
- **Full re-translation** — Each attempt gets correct schema translation, auth, and headers
- **Negligible overhead** — Base64 decode + JSON unmarshal only when header is present

## Security

- The routing plan header should only be set by **trusted ext_proc filters**, not external clients
- Backend names in the plan are validated against configured backends
- Unknown backend names result in an error

## Observability

Backend selection is logged:
- `routing plan activated` when header is parsed
- Backend name logged per attempt for debugging
