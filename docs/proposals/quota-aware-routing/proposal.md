# Quota-Aware Routing Proposal

## Overview

This proposal describes a quota-aware routing system for AI Gateway that enables intelligent traffic distribution between Provisioned Throughput (PT) and On-Demand capacity endpoints based on real-time quota consumption. The system leverages the existing `AIGatewayRoute` backendRefs and routing rules to define endpoint pools, with quota enforcement applied at the upstream ext_proc filter level.

## Goals

1. **Soft Quota Enforcement**: Implement soft quota limits per AIServiceBackend where requests exceeding the quota trigger fallback routing instead of rejection
2. **Capacity-Aware Routing**: Route requests to PT backends when quota is available, automatically fallback to on-demand backends when PT quota is exhausted
3. **Priority-Based Fallback**: When quota is exhausted for a backend, skip it and try the next backend in priority order
4. **Reuse Existing Primitives**: Leverage existing `backendRefs` and routing rules to define PT and on-demand endpoint pools across multiple regions/providers

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Request Flow                                   │
└─────────────────────────────────────────────────────────────────────────────┘

                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Router-Level AI Gateway ExtProc Filter                   │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  1. Parse request, extract model                                    │    │
│  │  2. Resolve backend based on AIGatewayRoute rules                   │    │
│  │  3. Set headers for upstream routing (PT/OD endpoint pool)          │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Envoy Router (Route Selection)                      │
│  ┌─────────────────────────────────────────────────────────────────────-┐   │
│  │  Select cluster based on route matching                              │   │
│  └─────────────────────────────────────────────────────────────────────-┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────-┐
│              Upstream AI Gateway ExtProc Filter (per-cluster)                │
│  ┌─────────────────────────────────────────────────────────────────────-┐    │
│  │  1. Check quota for current backend (rate limit in quota mode)       │    │
│  │  2. If quota available → Proceed to backend                          │    │
│  │  3. If quota exceeded (soft limit) →                                 │    │
│  │     a. Set dynamic metadata: quota_exceeded = true                   │    │
│  │     b. Return "try next backend" signal                              │    │
│  │     c. Skip this backend, fallback to next priority backend          │    │
│  │  4. Transform request to backend schema                              │    │
│  └─────────────────────────────────────────────────────────────────────-┘    │
│                                                                              │
│  Rate Limit Check (Quota Mode):                                              │
│  - Calls rate limit service for backend quota                                │
│  - Returns quota status in dynamic metadata                                  │
│  - Does NOT reject request, allows fallback routing                          │
└─────────────────────────────────────────────────────────────────────────────-┘
                                    │
                    ┌───────────────┼──────────────--─┐
                    │               │                 │
            ┌───────▼───────┐ ┌─────▼─────-┐  ┌───────▼───────┐
            │ Backend 1 (PT)│ │ Backend 2  │  │ Backend 3 (OD)│
            │ Priority: 1   │ │ Priority: 1│  │ Priority: 2   │
            │ AWS us-east-1 │ │ GCP central│  │ Anthropic API │
            └───────────────┘ └───────────-┘  └───────────────┘
```

## Key Design Decisions

### 0. Tenant Quota Check at Router Rate Limit Filter

The **router-level rate limit filter** tracks tenant-level quota using QuotaMode:

**Configuration:**
- **Soft Limit** (`quota_mode: true`): Tenant quota threshold for pool selection
  - When under soft limit: Tenant has "normal" capacity, prefer PT pool if available
  - When over soft limit: Tenant is consuming high quota, triggers PT availability check
  - Rate limit service populates `quotaModeViolations` in dynamic metadata when soft limit exceeded
  - Always returns `OK` status (never rejects)
- **Hard Limit** (normal mode): Absolute maximum tenant quota
  - When exceeded: Returns `OVER_LIMIT` → 429 response

**Router ExtProc Processing Flow:**
1. Router-level ExtProc reads dynamic metadata from rate limit filter
2. If no `quotaModeViolations` (under soft limit):
   - Set routing header: `x-endpoint-pool: provisioned` → Route to PT pool (Priority 0)
3. If `quotaModeViolations` present (over soft limit):
   - **Query rate limit service** for PT endpoint pool quota availability
   - Check PT pool quota descriptors (e.g., `pool=provisioned-throughput`)
   - If PT pool has available quota:
     - Set routing header: `x-endpoint-pool: provisioned` → Route to PT pool (Priority 0)
   - If PT pool quota exhausted:
     - Set routing header: `x-endpoint-pool: on-demand` → Route to on-demand pool (Priority 1)

**Purpose:**
- Tenant-level quota management with intelligent pool selection
- When tenant quota stressed, check PT availability before routing
- Graceful degradation to on-demand when PT capacity exhausted

### 1. Model Quota Check at Upstream Rate Limit Filter

The **upstream rate limit filter** (per-AIServiceBackend) tracks model-level quota for each backend:

**Configuration:**
- Each AIServiceBackend has its own rate limit filter in the upstream filter chain
- Rate limit descriptors include backend name and model for granular tracking
- Cost calculation based on model provider pricing (input/output tokens, cached tokens, etc.)
- **Normal rate limit mode** (NOT QuotaMode): Returns 429 when quota exceeded

**Filter Chain Setup:**
- Both PT pool and on-demand pool contain multiple AIServiceBackends
- Each backend has independent quota limits based on provider capacity
- Priority-based routing within each pool for retry/fallback

**Enforcement:**
- When backend quota available: Request proceeds to backend
- When backend quota exceeded: Returns 429, triggers retry to next backend in same pool
- Uses Envoy's priority-based retry with `previous_hosts` predicate

### 2. Reuse Existing BackendRefs for Endpoint Pools

Instead of defining endpoint pools in `AIServiceBackend`, we use the existing `AIGatewayRoute.backendRefs` with priority ordering:

```yaml
backendRefs:
  - name: aws-claude-pt-us-east-1      # PT, Priority 1
  - name: aws-claude-pt-us-west-2      # PT, Priority 1
  - name: gcp-claude-pt-us-central1    # PT, Priority 1
  - name: anthropic-claude-ondemand    # On-demand, Priority 2 (fallback)
```

### 3. Fallback via Priority-Based Retry

When a backend's quota is exceeded:

1. **Upstream rate limit filter** checks backend quota (normal mode, NOT QuotaMode)
2. If quota exceeded:
   - Rate limit filter returns `OVER_LIMIT` status → 429 response
3. **Envoy retry mechanism** triggered by 429 status code
4. **Retry policy with `previous_hosts` predicate**:
   - Skips the backend that returned 429
   - Selects next backend in the same pool (same priority level)
5. Process repeats until a backend with available quota is found or retries exhausted

## API Design

### QuotaPolicy in AIServiceBackend

```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: aws-claude-pt-us-east-1
  namespace: ai-gateway
spec:
  backendRef:
    name: bedrock-us-east-1
    port: 443
  # Backend quota policy configuration
  backendQuotaRef:
    name: aws-bedrock-model-quota
---
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: QuotaPolicy
metadata:
  name: aws-bedrock-model-quota
  namespace: ai-gateway
spec:
  perModelQuota:
    - modelName: claude-4-sonnet
      costExpression: input_tokens + 3 * output_tokens + 0.1 * cached_input_tokens + 1.25 * cache_creation_input_tokens
      rules:
        - clientSelectors:
          - headers:
              - name: service_tier
                value: reserved
          quotaValue:
            limit: 1M
            duration: 30s
        - clientSelectors:
            - headers:
              - name: service_tier
                value: default
          quotaValue:
            limit: 2M
            duration: 60s
```

### AIGatewayRoute with Priority-Based BackendRefs

```yaml
apiVersion: ai-gateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: claude-route
  namespace: ai-gateway
spec:
  parentRefs:
    - name: ai-gateway

  rules:
    - matches:
        - headers:
            - name: x-ai-model
              value: claude-4-sonnet

      # Backend refs in priority order (first = highest priority)
      # When quota exceeded, fallback to next in list
      backendRefs:
        # Priority 1: AWS PT us-east-1
        - name: aws-claude-pt-us-east-1
          priority: 0

        # Priority 1: AWS PT us-west-2 (regional failover)
        - name: aws-claude-pt-us-west-2
          priority: 0  # Only used as fallback

        # Priority 1: GCP PT us-central1 (cross-cloud failover)
        - name: gcp-claude-pt-us-central1
          priority: 0

        # Priority 4: On-demand fallback (always available)
        - name: anthropic-claude-ondemand
          priority: 1

      # Quota-based routing configuration
      quotaRouting:
        enabled: true
        # Retry to next backend when quota exceeded
        fallbackOnQuotaExceeded: true
        # Maximum backends to try before failing
        maxFallbackAttempts: 3
```

## Router ExtProc Filter Flow

### Pool Selection with PT Quota Check

```go
// ProcessRequestHeaders in router-level ext_proc filter
func (p *RouterProcessor) ProcessRequestHeaders(ctx context.Context, req *extprocv3.ProcessingRequest) (*extprocv3.ProcessingResponse, error) {
    // 1. Extract tenant and model from request headers
    tenant := p.getTenantFromHeaders(req.RequestHeaders)
    model := p.getModelFromHeaders(req.RequestHeaders)

    // 2. Read dynamic metadata from router rate limit filter
    rateLimitMetadata := p.getRateLimitMetadata(req.Attributes)

    // 3. Determine endpoint pool based on tenant quota status
    endpointPool := p.selectEndpointPool(ctx, tenant, model, rateLimitMetadata)

    // 4. Set routing header for pool selection
    return &extprocv3.ProcessingResponse{
        Response: &extprocv3.ProcessingResponse_RequestHeaders{
            RequestHeaders: &extprocv3.HeadersResponse{
                Response: &extprocv3.CommonResponse{
                    HeaderMutation: &extprocv3.HeaderMutation{
                        SetHeaders: []*corev3.HeaderValueOption{
                            {
                                Header: &corev3.HeaderValue{
                                    Key:   "x-endpoint-pool",
                                    Value: endpointPool, // "provisioned" or "on-demand"
                                },
                            },
                        },
                    },
                },
            },
        },
    }, nil
}

// selectEndpointPool determines which pool to route to based on tenant quota and PT availability
func (p *RouterProcessor) selectEndpointPool(ctx context.Context, tenant, model string, rateLimitMetadata *structpb.Struct) string {
    // Check if tenant soft limit exceeded
    quotaModeViolations := p.getQuotaModeViolations(rateLimitMetadata)

    if len(quotaModeViolations) == 0 {
        // Tenant under soft limit → prefer PT pool
        p.logger.Debug("tenant under soft limit, routing to PT pool",
            "tenant", tenant,
            "model", model)
        return "provisioned"
    }

    // Tenant over soft limit → check PT pool availability
    p.logger.Info("tenant over soft limit, checking PT pool availability",
        "tenant", tenant,
        "model", model)

    // Query rate limit service for PT pool quota
    ptAvailable, err := p.checkPTPoolQuota(ctx, model)
    if err != nil {
        p.logger.Error("failed to check PT pool quota, defaulting to on-demand",
            "error", err,
            "tenant", tenant,
            "model", model)
        return "on-demand"
    }

    if ptAvailable {
        p.logger.Info("PT pool has available quota, routing to PT",
            "tenant", tenant,
            "model", model)
        return "provisioned"
    }

    p.logger.Info("PT pool quota exhausted, routing to on-demand",
        "tenant", tenant,
        "model", model)
    return "on-demand"
}

// checkPTPoolQuota queries rate limit service for PT pool quota availability
func (p *RouterProcessor) checkPTPoolQuota(ctx context.Context, model string) (bool, error) {
    // Build rate limit request for PT pool quota descriptors
    request := &ratelimitv3.RateLimitRequest{
        Domain: "ai-gateway-quota",
        Descriptors: []*ratelimitv3.RateLimitDescriptor{
            {
                Entries: []*ratelimitv3.RateLimitDescriptor_Entry{
                    {Key: "pool", Value: "provisioned-throughput"},
                    {Key: "model", Value: model},
                },
            },
        },
        HitsAddend: 0, // Query only, don't consume quota
    }

    // Call rate limit service
    response, err := p.rateLimitClient.ShouldRateLimit(ctx, request)
    if err != nil {
        return false, fmt.Errorf("rate limit service error: %w", err)
    }

    // Check if PT pool has available quota
    // In normal operation, OK means quota available
    ptAvailable := response.OverallCode == ratelimitv3.RateLimitResponse_OK

    p.logger.Debug("PT pool quota check result",
        "available", ptAvailable,
        "response_code", response.OverallCode,
        "model", model)

    return ptAvailable, nil
}

// getQuotaModeViolations extracts quota mode violations from rate limit metadata
func (p *RouterProcessor) getQuotaModeViolations(metadata *structpb.Struct) []int {
    if metadata == nil {
        return nil
    }

    // Navigate to envoy.filters.http.ratelimit namespace
    rlNamespace, ok := metadata.Fields["envoy.filters.http.ratelimit"]
    if !ok {
        return nil
    }

    rlStruct := rlNamespace.GetStructValue()
    if rlStruct == nil {
        return nil
    }

    // Get quotaModeViolations list
    violations, ok := rlStruct.Fields["quotaModeViolations"]
    if !ok {
        return nil
    }

    violationsList := violations.GetListValue()
    if violationsList == nil {
        return nil
    }

    // Convert to int slice
    result := make([]int, 0, len(violationsList.Values))
    for _, v := range violationsList.Values {
        result = append(result, int(v.GetNumberValue()))
    }

    return result
}
```

## Fallback Routing Implementation

> **Note:** `ClearRouteCache` does NOT work in upstream filters because by the time
> the request reaches the upstream filter, the Router has already selected the route
> and cluster. The route decision is committed before upstream filters execute.
> Therefore, we use Envoy's **priority-based routing with retry** to achieve fallback.

### Priority-Based Backend Configuration

Envoy's load balancer supports **priority levels** for endpoints. When backends at a higher
priority level fail (or return retriable errors), Envoy can failover to lower priority backends.
This maps naturally to PT (priority 0) vs On-Demand (priority 1) routing.

#### How Priority-Based Load Balancing Works

1. **Priority 0 (highest)**: PT backends with quota limits
2. **Priority 1 (fallback)**: On-demand backends (always available)

Envoy selects backends from priority 0 first. When:
- All priority 0 backends return retriable errors (quota exceeded → 429)
- The `previous_priorities` retry predicate marks priority 0 as exhausted

Envoy automatically fails over to priority 1 backends.

### Envoy Cluster Configuration with Priority Levels

The cluster endpoints are configured with explicit priority levels. Each backend maps to
a locality within a priority level:

```yaml
# Generated Envoy cluster configuration
cluster:
  name: "ai-gateway-backends"
  load_balancing_policy:
    policies:
      - typed_extension_config:
          name: envoy.load_balancing_policies.least_request
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.load_balancing_policies.least_request.v3.LeastRequest
            locality_lb_config:
              locality_weighted_lb_config: {}

  load_assignment:
    cluster_name: "ai-gateway-backends"
    endpoints:
      # Priority 0: PT backends (try first)
      - priority: 0
        locality:
          region: "aws-claude-pt-us-east-1"
        lb_endpoints:
          - endpoint:
              address:
                socket_address:
                  address: "bedrock.us-east-1.amazonaws.com"
                  port_value: 443
            metadata:
              filter_metadata:
                aigateway.envoy.io:
                  backend_name: "aws-claude-pt-us-east-1"
                  capacity_type: "provisioned"
            load_balancing_weight: 1
        load_balancing_weight: 1

      - priority: 0
        locality:
          region: "aws-claude-pt-us-west-2"
        lb_endpoints:
          - endpoint:
              address:
                socket_address:
                  address: "bedrock.us-west-2.amazonaws.com"
                  port_value: 443
            metadata:
              filter_metadata:
                aigateway.envoy.io:
                  backend_name: "aws-claude-pt-us-west-2"
                  capacity_type: "provisioned"
            load_balancing_weight: 1
        load_balancing_weight: 1

      # Priority 1: On-demand backends (fallback)
      - priority: 1
        locality:
          region: "anthropic-claude-ondemand"
        lb_endpoints:
          - endpoint:
              address:
                socket_address:
                  address: "api.anthropic.com"
                  port_value: 443
            metadata:
              filter_metadata:
                aigateway.envoy.io:
                  backend_name: "anthropic-claude-ondemand"
                  capacity_type: "on-demand"
            load_balancing_weight: 1
        load_balancing_weight: 1
```

### Retry Policy with Priority Failover

Configure the retry policy to use `previous_priorities` predicate, which tracks failed
priority levels and skips them on retry:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: quota-retry-policy
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: claude-route

  # Router-level rate limit with QuotaMode for tenant quota
  rateLimit:
    rules:
      # Soft limit with QuotaMode - tracks tenant quota for pool selection
      - limit: 1000000  # 1M tokens per minute (soft limit)
        clientSelectors:
          - headers:
              - type: Exact
                name: x-ai-gateway-tenant
                value: tenant-a
        quotaMode: true  # Don't reject, populate metadata when exceeded

      # Hard limit - absolute maximum per tenant
      - limit: 5000000  # 5M tokens per minute (hard limit)
        clientSelectors:
          - headers:
              - type: Exact
                name: x-ai-gateway-tenant
                value: tenant-a
        # Normal mode - returns 429 when exceeded

  # Retry policy for backend failover
  retry:
    numRetries: 3
    retryOn:
      - "retriable-status-codes"
      - "5xx"
    retriableStatusCodes:
      - 429  # Returned by upstream rate limit when backend quota exceeded
    perTryTimeout: 30s

    # Skip hosts that were already attempted
    retryHostPredicate:
      - name: envoy.retry_host_predicates.previous_hosts

    # Skip priority levels where all hosts failed
    retryPriority:
      name: envoy.retry_priorities.previous_priorities
      typedConfig:
        "@type": type.googleapis.com/envoy.extensions.retry.priority.previous_priorities.v3.PreviousPrioritiesConfig
        updateFrequency: 2  # Update priority load after every 2 retries

    # Allow multiple host selection attempts within each retry
    hostSelectionRetryMaxAttempts: 5
```

### How Priority Failover Works

1. **Initial Request**: LB selects host from priority 0 (e.g., `aws-claude-pt-us-east-1`)
2. **Quota Check**: Upstream ext_proc checks quota → exceeded
3. **429 Response**: Ext_proc returns immediate 429 response
4. **First Retry**:
   - `previous_hosts` predicate rejects `aws-claude-pt-us-east-1`
   - LB selects another priority 0 host (e.g., `aws-claude-pt-us-west-2`)
5. **Quota Check Again**: Also exceeded → 429
6. **Second Retry**:
   - `previous_hosts` rejects both attempted hosts
   - No more hosts in priority 0 available
   - `previous_priorities` marks priority 0 as exhausted
   - LB fails over to priority 1
7. **Priority 1 Success**: Request goes to `anthropic-claude-ondemand`

### Key Benefits of Priority-Based Approach

1. **Explicit Failover Order**: PT backends always tried before on-demand
2. **Efficient Skip**: Once a priority is exhausted, entire level is skipped
3. **Native Envoy Support**: Uses built-in retry predicates, no custom code
4. **Works with Locality LB**: Compatible with existing locality-weighted config
5. **Configurable**: Can adjust `updateFrequency` to control failover sensitivity

## Rate Limit Service Configuration

### Tenant Quota Descriptors (Router Level)

```yaml
domain: ai-gateway-tenant-quota
descriptors:
  # Tenant soft limit with QuotaMode
  - key: tenant
    value: tenant-a
    descriptors:
      - key: limit_type
        value: soft
        rate_limit:
          unit: minute
          requests_per_unit: 1000000  # 1M tokens
        quota_mode: true  # Don't reject, populate metadata

  # Tenant hard limit (normal mode)
  - key: tenant
    value: tenant-a
    descriptors:
      - key: limit_type
        value: hard
        rate_limit:
          unit: minute
          requests_per_unit: 5000000  # 5M tokens
        # Normal mode - rejects when exceeded
```

### PT Pool Quota Descriptors (Router Level Check)

```yaml
domain: ai-gateway-quota
descriptors:
  # PT pool aggregate quota across all PT backends
  - key: pool
    value: provisioned-throughput
    descriptors:
      - key: model
        value: claude-4-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 50000  # Combined PT capacity
        # Normal mode - used for availability check (HitsAddend: 0)
```

### Per-Backend Quota Descriptors (Upstream Level)

```yaml
domain: ai-gateway-quota
descriptors:
  # AWS Claude PT us-east-1 backend
  - key: backend
    value: aws-claude-pt-us-east-1
    descriptors:
      - key: model
        value: claude-4-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 20000  # PT backend capacity
        # Normal mode - returns 429 when exceeded

  # AWS Claude PT us-west-2 backend
  - key: backend
    value: aws-claude-pt-us-west-2
    descriptors:
      - key: model
        value: claude-4-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 15000
        # Normal mode - returns 429 when exceeded

  # GCP Claude PT us-central1 backend
  - key: backend
    value: gcp-claude-pt-us-central1
    descriptors:
      - key: model
        value: claude-4-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 15000
        # Normal mode - returns 429 when exceeded

  # On-demand backend (high limit)
  - key: backend
    value: anthropic-claude-ondemand
    descriptors:
      - key: model
        value: claude-4-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 1000000  # Very high for on-demand
        # Normal mode - returns 429 when exceeded
```

## Sequence Diagram

### Priority-Based Failover Flow

```
┌──────┐  ┌────────────┐  ┌─────────────┐  ┌────────┐  ┌──────────────┐  ┌──────────────┐  ┌─────────┐
│Client│  │Router RL   │  │Router ExtProc│ │Router/LB│ │Upstream RL   │  │RateLimit Svc │  │ Backend │
│      │  │Filter      │  │              │  │         │  │Filter        │  │              │  │         │
└──┬───┘  └─────┬──────┘  └──────┬──────┘  └───┬────┘  └───────┬──────┘  └──────┬───────┘  └────┬────┘
   │             │                │             │                │                │               │
   │ POST /chat  │                │             │                │                │               │
   │────────────>│                │             │                │                │               │
   │             │                │             │                │                │               │
   │             │ Check tenant quota (QuotaMode)                │                │               │
   │             │───────────────────────────────────────────────────────────────>│               │
   │             │                │             │                │                │               │
   │             │ SOFT LIMIT EXCEEDED (quotaModeViolations in metadata)          │               │
   │             │<───────────────────────────────────────────────────────────────│               │
   │             │                │             │                │                │               │
   │             │ Pass metadata  │             │                │                │               │
   │             │───────────────>│             │                │                │               │
   │             │                │             │                │                │               │
   │             │                │ Detect soft limit exceeded   │                │               │
   │             │                │ Check PT pool availability   │                │               │
   │             │                │─────────────────────────────────────────────>│               │
   │             │                │             │                │                │               │
   │             │                │ PT pool OK (quota available) │                │               │
   │             │                │<─────────────────────────────────────────────│               │
   │             │                │             │                │                │               │
   │             │                │ Set: x-endpoint-pool=provisioned              │               │
   │             │                │────────────>│                │                │               │
   │             │                │             │                │                │               │
   │             │                │             │ Select Priority 0: PT-east-1   │               │
   │             │                │             │───────────────>│                │               │
   │             │                │             │                │                │               │
   │             │                │             │                │ Check backend quota (normal)  │
   │             │                │             │                │───────────────>│               │
   │             │                │             │                │                │               │
   │             │                │             │                │ OVER_LIMIT (429)              │
   │             │                │             │                │<───────────────│               │
   │             │                │             │                │                │               │
   │             │                │             │ 429 Response   │                │               │
   │             │                │             │<───────────────│                │               │
   │             │                │             │                │                │               │
   │             │                │             │ Retry: previous_hosts skips PT-east-1          │
   │             │                │             │ Select PT-west-2                               │
   │             │                │             │───────────────>│                │               │
   │             │                │             │                │                │               │
   │             │                │             │                │ Check quota    │               │
   │             │                │             │                │───────────────>│               │
   │             │                │             │                │                │               │
   │             │                │             │                │ OK             │               │
   │             │                │             │                │<───────────────│               │
   │             │                │             │                │                │               │
   │             │                │             │                │ Forward request──────────────>│
   │             │                │             │                │                │               │
   │             │                │             │                │                │   Response    │
   │             │                │             │                │                │<──────────────│
   │             │                │             │                │                │               │
   │             │                │             │                │ Record token usage            │
   │             │                │             │                │───────────────>│               │
   │             │                │             │                │                │               │
   │<────────────────────────────────────────────────────────────────────────────────────────────│
   │  Response   │                │             │                │                │               │
```

### Legend

- **Priority 0 (P0)**: Provisioned Throughput backends (PT-east-1, PT-west-2)
- **Priority 1 (P1)**: On-demand backends (fallback)
- **previous_hosts**: Retry predicate that skips already-attempted hosts
- **previous_priorities**: Retry predicate that skips exhausted priority levels

## Metrics and Observability

```go
var (
    quotaCheckTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "ai_gateway_quota_checks_total",
            Help: "Total quota checks per backend",
        },
        []string{"backend", "result"},  // result: "allowed", "exceeded", "error"
    )

    quotaFallbackTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "ai_gateway_quota_fallbacks_total",
            Help: "Total fallbacks due to quota exceeded",
        },
        []string{"from_backend", "to_backend"},
    )

    quotaUtilization = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "ai_gateway_quota_utilization_ratio",
            Help: "Current quota utilization (0.0-1.0+)",
        },
        []string{"backend", "capacity_type"},
    )
)
```

## Implementation Items

### 1: Router ExtProc - PT Pool Availability Check
- Read tenant quota metadata from router-level rate limit filter
- Parse `quotaModeViolations` from dynamic metadata (`envoy.filters.http.ratelimit` namespace)
- When soft limit exceeded:
  - Query rate limit service for PT pool quota descriptors (with `HitsAddend: 0` for query-only)
  - Check PT pool availability based on response status
- Set routing header (`x-endpoint-pool`) based on PT availability:
  - PT available → `provisioned` (Priority 0)
  - PT exhausted → `on-demand` (Priority 1)
- Record pool selection metrics

### 2: Upstream Rate Limit Filter per AIServiceBackend
- Configure rate limit filter in upstream filter chain for each backend
- Use normal rate limit mode (NOT QuotaMode) - returns 429 when quota exceeded
- Rate limit descriptors with backend name and model
- Cost calculation based on model provider pricing (input/output/cached tokens)
- Token usage recorded post-response using dynamic metadata

### 3: Retry Policy Configuration
- Configure router-level rate limit with QuotaMode for tenant quota
- Configure `previous_hosts` retry predicate to skip backends that returned 429
- Set `numRetries` based on number of backends in pool
- Configure `retriableStatusCodes` to include 429 (from upstream rate limit)
- Set appropriate `perTryTimeout` for backend requests

### 4: Rate Limit Service Configuration
- **Tenant quota descriptors**: Soft limit (QuotaMode) + hard limit (normal)
- **PT pool quota descriptors**: Aggregate PT capacity for availability check
- **Per-backend quota descriptors**: Individual backend limits (normal mode)

## Open Questions

1. How to handle the latency overhead of quota check on each request?
   - Option: Cache quota status with short TTL
   - Option: Async quota check with optimistic routing

2. Should fallback be transparent to the client or return a header indicating fallback occurred?

3. How to handle streaming requests where token count is unknown upfront?
   - Option: Estimate based on input tokens
   - Option: Reserve capacity and reconcile after response

4. Should there be a "sticky" preference to avoid flip-flopping between backends near quota boundary?
