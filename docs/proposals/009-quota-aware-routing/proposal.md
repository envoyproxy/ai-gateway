# Quota-Aware Routing Proposal

## Overview

This proposal describes a quota-aware routing system for AI Gateway that enables intelligent traffic distribution between Provisioned Throughput (PT) and On-Demand capacity endpoints based on real-time quota consumption.
The system leverages the existing `AIGatewayRoute` backendRefs and routing rules to define endpoint pools, with quota enforcement applied at the **router-level rate limit filter** using QuotaMode to search available backend quota and route according to priority.

## Goals

1. **Capacity-Aware Routing**: Route requests to PT backends when quota is available, automatically fallback to on-demand backends when PT quota is exhausted
2. **Priority-Based Fallback**: When quota is exhausted for a backend, skip it and try the next backend in priority order
3. **Reuse Existing Primitives**: Leverage existing `backendRefs` and routing rules to define PT and on-demand endpoint pools across multiple regions/providers

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Request Flow                                   │
└─────────────────────────────────────────────────────────────────────────────┘

                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│               Router-Level Rate Limit Filter (QuotaMode)                    │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  1. Check per-backend quota (QuotaMode) for all backends            │    │
│  │  2. Populate quotaModeViolations in dynamic metadata                │    │
│  │     - Lists which backend descriptors have exceeded quota           │    │
│  │  3. Always returns OK (never rejects in QuotaMode)                  │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Router-Level AI Gateway ExtProc Filter                   │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  1. Parse request, extract model                                    │    │
│  │  2. Read quotaModeViolations from RL filter dynamic metadata        │    │
│  │  3. Identify which backends have available quota by priority         │    │
│  │  4. Route to highest-priority backend with available quota          │    │
│  │  5. If all PT backends exceeded → route to on-demand pool           │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Envoy Router (Route Selection)                      │
│  ┌─────────────────────────────────────────────────────────────────────-┐   │
│  │  Select cluster/endpoint based on routing headers set by ExtProc     │   │
│  └─────────────────────────────────────────────────────────────────────-┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                    ┌───────────────┼──────────────--─┐
                    │               │                 │
            ┌───────▼───────┐ ┌─────▼─────-┐  ┌───────▼───────┐
            │ Backend 1 (PT)│ │ Backend 2  │  │ Backend 3 (OD)│
            │ Priority: 0   │ │ Priority: 0│  │ Priority: 1   │
            │ AWS us-east-1 │ │ GCP central│  │ Anthropic API │
            └───────────────┘ └───────────-┘  └───────────────┘
```

## Key Design Decisions

### 1. Per-Backend Quota Check at Router-Level Rate Limit Filter (QuotaMode)

The **router-level rate limit filter** checks per-backend quota for all backends using QuotaMode:

**Configuration:**

- A single router-level rate limit filter contains descriptors for **every** backend
- Rate limit descriptors include backend name and model for granular tracking
- Cost calculation based on model provider pricing (input/output tokens, cached tokens, etc.)
- **QuotaMode** (`quota_mode: true`): Always returns OK, populates `quotaModeViolations` in dynamic metadata for any backend whose quota is exceeded

**How It Works:**

- The RL filter evaluates quota descriptors for all backends in a single check
- Backends that exceed their quota appear in `quotaModeViolations` metadata
- The router ExtProc reads this metadata to know which backends have available quota
- ExtProc routes to the highest-priority backend with available quota

**Routing Decision (in ExtProc):**

- When backend quota available: ExtProc sets routing headers to direct request to that backend
- When backend quota exceeded: ExtProc skips that backend and selects the next one by priority
- No retry-based failover needed — the routing decision is made upfront at the router level

### 2. Reuse Existing BackendRefs for Endpoint Pools

Instead of defining endpoint pools in `AIServiceBackend`, we use the existing `AIGatewayRoute.backendRefs` with priority ordering:

```yaml
backendRefs:
  - name: aws-claude-pt-us-east-1 # PT, Priority 1
  - name: aws-claude-pt-us-west-2 # PT, Priority 1
  - name: gcp-claude-pt-us-central1 # PT, Priority 1
  - name: anthropic-claude-ondemand # On-demand, Priority 2 (fallback)
```

### 3. Fallback via Router-Level Quota Search

When a backend's quota is exceeded, failover is handled **upfront at the router level** — not via retry:

1. **Router-level rate limit filter** checks all backend quotas (QuotaMode)
2. `quotaModeViolations` metadata lists which backends have exceeded quota
3. **Router ExtProc reads metadata** and identifies available backends by priority:
   - Iterates through backends in priority order (Priority 0 first, then Priority 1, etc.)
   - Skips any backend whose descriptor index appears in `quotaModeViolations`
   - Selects the first backend with available quota
4. **ExtProc sets routing headers** to direct the request to the selected backend
5. If all backends at Priority 0 are exceeded → routes to Priority 1 (on-demand)
6. If all backends at all priorities are exceeded → returns 429 to client

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
          priority: 0 # Only used as fallback

        # Priority 1: GCP PT us-central1 (cross-cloud failover)
        - name: gcp-claude-pt-us-central1
          priority: 0

        # Priority 4: On-demand fallback (always available)
        - name: anthropic-claude-ondemand
          priority: 1
```

## Router ExtProc Filter Flow

### Quota-Aware Backend Selection

The router-level RL filter checks quota for all backends in a single pass using QuotaMode.
The ExtProc then reads the `quotaModeViolations` metadata to determine which backends have
available quota and routes to the highest-priority one.

```go
// Backend represents a backend with its priority and RL descriptor index
type Backend struct {
	Name            string
	Priority        int
	DescriptorIndex int // Index in the RL filter's descriptor list
}

// ProcessRequestHeaders in router-level ext_proc filter
func (p *RouterProcessor) ProcessRequestHeaders(ctx context.Context, req *extprocv3.ProcessingRequest) (*extprocv3.ProcessingResponse, error) {
	// 1. Extract tenant and model from request headers
	tenant := p.getTenantFromHeaders(req.RequestHeaders)
	model := p.getModelFromHeaders(req.RequestHeaders)

	// 2. Read quotaModeViolations from router-level RL filter dynamic metadata
	//    This metadata contains descriptor indices for backends that exceeded quota
	rateLimitMetadata := p.getRateLimitMetadata(req.Attributes)
	violatedIndices := p.getQuotaModeViolations(rateLimitMetadata)

	// 3. Select highest-priority backend with available quota
	selectedBackend := p.selectBackendByPriority(tenant, model, violatedIndices)

	if selectedBackend == nil {
		// All backends at all priorities exceeded → reject
		p.logger.Error("all backend quotas exceeded, rejecting request",
			"tenant", tenant,
			"model", model)
		return p.buildImmediateResponse(429, "all backend quotas exceeded"), nil
	}

	// 4. Set routing headers to direct request to the selected backend
	p.logger.Info("selected backend based on quota availability",
		"backend", selectedBackend.Name,
		"priority", selectedBackend.Priority,
		"tenant", tenant,
		"model", model)

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: &extprocv3.HeaderMutation{
						SetHeaders: []*corev3.HeaderValueOption{
							{
								Header: &corev3.HeaderValue{
									Key:   "x-ai-backend",
									Value: selectedBackend.Name,
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

// selectBackendByPriority iterates through backends in priority order,
// skipping any whose descriptor index appears in quotaModeViolations.
func (p *RouterProcessor) selectBackendByPriority(tenant, model string, violatedIndices []int) *Backend {
	violatedSet := make(map[int]bool, len(violatedIndices))
	for _, idx := range violatedIndices {
		violatedSet[idx] = true
	}

	// Backends are pre-sorted by priority (0 = highest)
	for _, backend := range p.backends {
		if violatedSet[backend.DescriptorIndex] {
			// This backend's quota is exceeded, skip it
			p.logger.Debug("skipping backend with exceeded quota",
				"backend", backend.Name,
				"priority", backend.Priority,
				"descriptor_index", backend.DescriptorIndex)
			continue
		}

		// Found a backend with available quota
		return &backend
	}

	// All backends exceeded
	return nil
}

// getQuotaModeViolations extracts quota mode violations from rate limit metadata.
// The RL filter in QuotaMode populates quotaModeViolations with the descriptor
// indices that exceeded their configured limits.
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

	// Get quotaModeViolations list — contains descriptor indices that exceeded quota
	violations, ok := rlStruct.Fields["quotaModeViolations"]
	if !ok {
		return nil
	}

	violationsList := violations.GetListValue()
	if violationsList == nil {
		return nil
	}

	result := make([]int, 0, len(violationsList.Values))
	for _, v := range violationsList.Values {
		result = append(result, int(v.GetNumberValue()))
	}

	return result
}
```

## Failover Routing Implementation

### Router-Level Quota Search for Failover

The quota mode RL filter is injected at the **router level**, not the upstream level.
This enables the router ExtProc to search available quota across all backends and route
directly to a backend with available capacity — no retry-based failover needed.

#### How Router-Level Quota Search Works

1. **Priority 0 (highest)**: PT backends with quota limits
2. **Priority 1 (fallback)**: On-demand backends (always available)

The router-level RL filter evaluates quota descriptors for every backend in a single pass.
Backends that exceed their quota are reported via `quotaModeViolations` in dynamic metadata.
The ExtProc reads these violations and selects the highest-priority backend with available quota.

### Router-Level Rate Limit Filter Configuration

The rate limit filter is configured at the router level with QuotaMode descriptors
for per-backend quota vis envoy xDS.

### How Failover Works (Router-Level Quota Search)

1. **Request Arrives**: Router-level RL filter evaluates all backend quota descriptors
2. **Metadata Populated**: `quotaModeViolations` lists descriptor indices that exceeded quota
   - Example: `[2]` means descriptor 2 (aws-claude-pt-us-east-1) is over quota
3. **ExtProc Reads Metadata**: Identifies which backends have available quota
4. **Priority-Based Selection**:
   - Iterate backends sorted by priority (0 = highest)
   - Skip backends whose descriptor index appears in `quotaModeViolations`
   - Select first backend with available quota
5. **Route Directly**: ExtProc sets `x-ai-backend` header → request routed to selected backend
6. **All Exhausted**: If no backend has available quota → return 429 to client

**Example Walkthrough:**

- Backends: `PT-east-1` (P0, desc 2), `PT-west-2` (P0, desc 3), `OD-anthropic` (P1, desc 4)
- `quotaModeViolations: [2]` → PT-east-1 exceeded
- ExtProc checks P0 backends: PT-east-1 ✗ (violated), PT-west-2 ✓ (available)
- Routes to `PT-west-2`
- If `quotaModeViolations: [2, 3]` → both P0 backends exceeded
- ExtProc checks P0: all exceeded → checks P1: OD-anthropic ✓ (available)
- Routes to `OD-anthropic`

### Key Benefits of Router-Level Quota Search

1. **No Retry Overhead**: Routing decision made upfront, no 429 retry loops
2. **Single Check**: All backend quotas evaluated in one RL filter pass
3. **Priority Ordering**: PT backends always preferred when quota available
4. **Lower Latency**: Avoids upstream round-trips to backends that would reject
5. **Simpler Architecture**: No upstream RL filters needed for quota checking

## Rate Limit Service Configuration

All rate limit descriptors are evaluated at the **router level**. Per-backend quota
uses QuotaMode so the RL filter populates `quotaModeViolations` metadata instead of rejecting.

### Per-Backend Quota Descriptors (Router Level — QuotaMode)

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
          requests_per_unit: 20000 # PT backend capacity
        quota_mode: true # QuotaMode — populates metadata, does NOT reject

  # AWS Claude PT us-west-2 backend
  - key: backend
    value: aws-claude-pt-us-west-2
    descriptors:
      - key: model
        value: claude-4-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 15000
        quota_mode: true

  # GCP Claude PT us-central1 backend
  - key: backend
    value: gcp-claude-pt-us-central1
    descriptors:
      - key: model
        value: claude-4-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 15000
        quota_mode: true

  # On-demand backend (high limit)
  - key: backend
    value: anthropic-claude-ondemand
    descriptors:
      - key: model
        value: claude-4-sonnet
        rate_limit:
          unit: minute
          requests_per_unit: 1000000 # Very high for on-demand
        quota_mode: true
```

## Sequence Diagram

### Router-Level Quota Search Failover Flow

```
┌──────┐  ┌─────────────────────┐  ┌─────────────┐  ┌────────┐  ┌─────────┐
│Client│  │Router RL Filter     │  │RouterExtProc│  │  Envoy │  │ Backend │
│      │  │(QuotaMode)          │  │             │  │ Router │  │         │
└──┬───┘  └──────────┬──────────┘  └──────┬──────┘  └───┬────┘  └────┬────┘
   │                  │                    │             │             │
   │ POST /chat       │                    │             │             │
   │─────────────────>│                    │             │             │
   │                  │                    │             │             │
   │                  │ Check ALL quotas in one pass:    │             │
   │                  │  - PT-east-1 quota  (desc 2)     │             │
   │                  │  - PT-west-2 quota  (desc 3)     │             │
   │                  │  - OD-anthropic     (desc 4)     │             │
   │                  │                    │             │             │
   │                  │ Result: quotaModeViolations=[2]  │             │
   │                  │ (PT-east-1 exceeded, others OK)  │             │
   │                  │                    │             │             │
   │                  │ Pass metadata ────>│             │             │
   │                  │                    │             │             │
   │                  │                    │ Read quotaModeViolations  │
   │                  │                    │ Backends by priority:     │
   │                  │                    │  P0: PT-east-1 ✗ (desc 2 violated)
   │                  │                    │  P0: PT-west-2 ✓ (desc 3 OK)
   │                  │                    │  P1: OD-anthropic ✓      │
   │                  │                    │             │             │
   │                  │                    │ Selected: PT-west-2 (P0) │
   │                  │                    │ Set header: x-ai-backend=PT-west-2
   │                  │                    │────────────>│             │
   │                  │                    │             │             │
   │                  │                    │             │ Route to    │
   │                  │                    │             │ PT-west-2   │
   │                  │                    │             │────────────>│
   │                  │                    │             │             │
   │                  │                    │             │  Response   │
   │                  │                    │             │<────────────│
   │                  │                    │             │             │
   │<─────────────────────────────────────────────────────────────────│
   │  Response        │                    │             │             │
```

### All PT Exhausted → Failover to On-Demand

```
┌──────┐  ┌─────────────────────┐  ┌─────────────┐  ┌────────┐  ┌─────────┐
│Client│  │Router RL Filter     │  │RouterExtProc│  │  Envoy │  │ Backend │
│      │  │(QuotaMode)          │  │             │  │ Router │  │         │
└──┬───┘  └──────────┬──────────┘  └──────┬──────┘  └───┬────┘  └────┬────┘
   │                  │                    │             │             │
   │ POST /chat       │                    │             │             │
   │─────────────────>│                    │             │             │
   │                  │                    │             │             │
   │                  │ Check ALL quotas   │             │             │
   │                  │ Result: quotaModeViolations=[2,3]│             │
   │                  │ (Both PT backends exceeded)      │             │
   │                  │                    │             │             │
   │                  │ Pass metadata ────>│             │             │
   │                  │                    │             │             │
   │                  │                    │ Read quotaModeViolations  │
   │                  │                    │ Backends by priority:     │
   │                  │                    │  P0: PT-east-1 ✗ (violated)
   │                  │                    │  P0: PT-west-2 ✗ (violated)
   │                  │                    │  P1: OD-anthropic ✓ (OK)  │
   │                  │                    │             │             │
   │                  │                    │ Selected: OD-anthropic (P1)
   │                  │                    │ Set header: x-ai-backend=OD-anthropic
   │                  │                    │────────────>│             │
   │                  │                    │             │             │
   │                  │                    │             │ Route to    │
   │                  │                    │             │ OD-anthropic│
   │                  │                    │             │────────────>│
   │                  │                    │             │             │
   │                  │                    │             │  Response   │
   │                  │                    │             │<────────────│
   │                  │                    │             │             │
   │<─────────────────────────────────────────────────────────────────│
   │  Response        │                    │             │             │
```

### Legend

- **Priority 0 (P0)**: Provisioned Throughput backends (PT-east-1, PT-west-2)
- **Priority 1 (P1)**: On-demand backends (fallback)
- **quotaModeViolations**: Descriptor indices reported by the router-level RL filter for backends exceeding quota
- **No retry needed**: Backend selection happens upfront at the router level

## Metrics and Observability

```go
var (
	quotaCheckTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ai_gateway_quota_checks_total",
			Help: "Total quota checks per backend",
		},
		[]string{"backend", "result"}, // result: "allowed", "exceeded", "error"
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

### 1: Router-Level Rate Limit Filter with QuotaMode (Per-Backend Quota)

- Inject a **single router-level RL filter** with QuotaMode descriptors for every backend
- Each backend has a descriptor with its quota limit configured as QuotaMode
- RL filter evaluates all descriptors in one pass and populates `quotaModeViolations` metadata
- Cost calculation based on model provider pricing (input/output/cached tokens)

### 2: Router ExtProc - Quota-Aware Backend Selection

- Read `quotaModeViolations` from dynamic metadata (`envoy.filters.http.ratelimit` namespace)
- Map violated descriptor indices to backend names
- Iterate backends sorted by priority (0 = highest)
- Skip backends whose descriptor index appears in violations
- Set routing header (`x-ai-backend`) to the selected backend
- If all backends exhausted → return 429 to client
- Record backend selection metrics

### 3: Rate Limit Service Configuration

- **Per-backend quota descriptors**: Individual backend limits (**QuotaMode** — router level)
- All descriptors evaluated at the router level in a single RL filter pass

## Open Questions

1. Should fallback be transparent to the client or return a header indicating fallback occurred?

2. How to handle streaming requests where token count is unknown upfront?
   - Option: Estimate based on input tokens
   - Option: Reserve capacity and reconcile after response

3. Should there be a "sticky" preference to avoid flip-flopping between backends near quota boundary?
