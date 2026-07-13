# Per-Request LLM Backend Routing via Routing Plan Header

Enables arbitrary per-request backend selection and fallback for LLM requests.
A custom ext_proc sets a routing plan header; the AI Gateway ext_proc uses it
to control which backend each attempt targets, with DFP handling the network.

**Status: Implemented.** No controller or CRDs required — runs with static config files.

---

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

---

## Routing Plan Header

```
x-ai-eg-routing-plan: <base64-encoded JSON>
```

```json
{
  "backends": ["azure-primary", "gcp-ptu", "aws-bedrock"],
  "fallbackEnabled": true
}
```

- **`backends`** — Ordered list of backend names matching `filterapi.Backend.Name`
- **`fallbackEnabled`** — When `false`, only `backends[0]` is used (default: `true`)
- Requests without this header use standard Envoy routing (fully backward compatible)

---

## Deployment: Static Config, No Controller

The ext_proc reads a YAML config file via `-configPath`. No K8s controller,
no CRDs, no Envoy Gateway extension server. Just two files:

### 1. ExtProc Config (`filter-config.yaml`)

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

  - name: "aws-bedrock"
    schema: {name: "AWSBedrock"}
    host: "bedrock-runtime.us-west-2.amazonaws.com"
    backendPath: "/model/anthropic.claude-3-sonnet/converse"
    auth:
      aws:
        region: "us-west-2"
        credentialFileLiteral: |
          [default]
          aws_access_key_id = AKIA...
          aws_secret_access_key = ...
```

### 2. Envoy Config (`envoy.yaml`)

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
                          metadata:
                            filter_metadata:
                              aigateway.envoy.io:
                                aigw_route_name: "default/my-route"
                http_filters:
                  # AI Gateway ext_proc — router level
                  - name: envoy.filters.http.ext_proc/aigateway
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
                      grpc_service:
                        envoy_grpc: {cluster_name: ai_gateway_extproc}
                        timeout: 30s
                      metadata_options:
                        receiving_namespaces:
                          untyped: ["aigateway.envoy.io"]
                      processing_mode:
                        request_header_mode: SEND
                        request_body_mode: BUFFERED
                        response_header_mode: SEND
                        response_body_mode: BUFFERED
                      message_timeout: 10s
                      allow_mode_override: true
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router

  clusters:
    # Single DFP cluster — routes to any backend dynamically via :authority
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
      metadata:
        filter_metadata:
          aigateway.envoy.io:
            per_route_rule_backend_name: "azure-primary"  # fallback when no routing plan
      typed_extension_protocol_options:
        envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
          "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
          explicit_http_config:
            http_protocol_options: {}
          http_filters:
            # Upstream ext_proc — runs per attempt
            - name: envoy.filters.http.ext_proc/aigateway
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
                grpc_service:
                  envoy_grpc: {cluster_name: ai_gateway_extproc}
                  timeout: 30s
                metadata_options:
                  receiving_namespaces:
                    untyped: ["aigateway.envoy.io"]
                request_attributes:
                  - "xds.upstream_host_metadata.filter_metadata['aigateway.envoy.io']['per_route_rule_backend_name']"
                  - "xds.cluster_metadata.filter_metadata['aigateway.envoy.io']['per_route_rule_backend_name']"
                  - "xds.route_metadata.filter_metadata['aigateway.envoy.io']['aigw_route_name']"
                processing_mode:
                  request_header_mode: SEND
                  request_body_mode: NONE
                  response_header_mode: SKIP
                  response_body_mode: NONE
                message_timeout: 10s
                allow_mode_override: true
            - name: envoy.filters.http.header_mutation
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.http.header_mutation.v3.HeaderMutation
                mutations:
                  request_mutations:
                    - append:
                        header:
                          key: content-length
                          value: "%DYNAMIC_METADATA(aigateway.envoy.io:content_length)%"
                        append_action: ADD_IF_ABSENT
            - name: envoy.filters.http.upstream_codec
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.http.upstream_codec.v3.UpstreamCodec

    # AI Gateway ext_proc gRPC server
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

### 3. Tunneling via hostAliases (for private endpoints)

If backends are reachable through private endpoints (VPC Endpoint, Private Link, etc.),
use Pod `hostAliases` to map provider hostnames to tunnel IPs. DFP resolves from
`/etc/hosts` and uses the original hostname as TLS SNI.

```yaml
spec:
  hostAliases:
    - ip: "10.142.13.17"
      hostnames: ["snc-oai-llmproxy-dev-eastus2.openai.azure.com"]
    - ip: "10.103.13.58"
      hostnames: ["bedrock-runtime.us-west-2.amazonaws.com"]
    - ip: "10.31.31.1"
      hostnames:
        - "us-central1-aiplatform.googleapis.com"
        - "us-west1-aiplatform.googleapis.com"
```

This gives per-provider tunneling with a single DFP cluster — no per-provider
clusters needed, cross-provider fallback works natively.

---

## Code Changes (Implemented)

All changes are in `internal/extproc/` and `internal/filterapi/`:

| File | Change |
|---|---|
| `internalapi/internalapi.go` | `LLMRoutingPlanHeader` constant, `RoutingPlan` struct |
| `filterapi/filterconfig.go` | `Host`, `BackendPath` fields on `Backend` |
| `extproc/processor.go` | `routingPlanProvider` interface |
| `extproc/processor_impl.go` | Parse routing plan in `ProcessRequestBody`; set `:authority`/`:path` in `ProcessRequestHeaders` |
| `extproc/server.go` | `resolveBackendFromPlan` helper; routing plan check in `setBackend` |

### Key behaviors

- **Backward compatible** — requests without the header are completely unaffected
- **Deterministic fallback** — `backends[0]` on attempt 1, `backends[1]` on retry, etc.
- **Full re-translation** — each attempt gets correct schema translation, auth, and headers for its backend
- **Negligible overhead** — base64 decode + JSON unmarshal only when header is present

---

## Usage Examples

See [examples/llm-arbitrary-routing](examples/llm-arbitrary-routing/) for complete, runnable examples:

- **[Python](examples/llm-arbitrary-routing/clients/example_python.py)** — OpenAI SDK with routing plan header
- **[JavaScript](examples/llm-arbitrary-routing/clients/example_javascript.js)** — Fetch API with base64 encoding
- **[Go](examples/llm-arbitrary-routing/clients/example_go.go)** — net/http client example

Also included: [filter-config.yaml](examples/llm-arbitrary-routing/filter-config.yaml) with three backends (Azure, GCP, AWS) and [examples/llm-arbitrary-routing/README.md](examples/llm-arbitrary-routing/README.md) for setup instructions.

---

## Retry and Fallback

Cross-provider fallback works because the single DFP cluster handles all backends.
Each retry attempt re-enters the upstream ext_proc, which picks the next backend
from the routing plan and sets a new `:authority`.

```
Attempt 1: :authority=azure.openai.com     → DFP → Azure (503)
Attempt 2: :authority=aiplatform.google.com → DFP → GCP   (200) ✓
```

**Requirements:**
- Envoy `retry_policy.num_retries` must be ≥ `len(backends) - 1`
- `retry_on` must include the status codes that should trigger fallback

---

## Considerations

- **Retry count** — The ext_proc cannot force retries. Envoy's retry policy must allow enough attempts.
- **Security** — The routing plan header should only be set by trusted ext_proc filters, not by external clients.
- **Backend validation** — Unknown backend names in the plan result in an `Internal` gRPC error.
- **Observability** — Backend selection is logged (`routing plan activated`, backend name per attempt).
