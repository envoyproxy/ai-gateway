# Specification: Context Caching for GCP Vertex AI (Gemini)

This document is the implementation specification for
[proposal.md](./proposal.md). It refines the proposal into a concrete technical
design against the current codebase, with one deliberate architectural change:
**cache resolution runs in-process inside extproc** instead of in an external
cache service. The resolver is placed behind an interface so the external
service (as originally proposed) remains a future drop-in.

## Table of Contents

<!-- toc -->

- [Technical Design](#technical-design)
  - [Architecture Decision: In-Process Resolution](#architecture-decision-in-process-resolution)
  - [Schema Extensions](#schema-extensions)
  - [Cache Resolver (`internal/gcpcache`)](#cache-resolver-internalgcpcache)
  - [Region and Project Plumbing](#region-and-project-plumbing)
  - [Request Path Integration](#request-path-integration)
  - [Cross-Phase State and Token/Cost Accounting](#cross-phase-state-and-tokencost-accounting)
  - [Configuration Surface](#configuration-surface)
  - [Error Handling](#error-handling)
  - [Multi-Region Behavior](#multi-region-behavior)
  - [Streaming](#streaming)
- [User Stories](#user-stories)
- [Implementation Plan](#implementation-plan)
- [Documentation](#documentation)
- [Success Criteria](#success-criteria)

<!-- /toc -->

## Technical Design

### Architecture Decision: In-Process Resolution

The proposal describes an external HTTP cache service (`POST /v1/cache/resolve`).
This specification implements the same logic **inside extproc** instead:

| Aspect                     | External service (proposal)      | In-process (this spec)                                                  |
| -------------------------- | -------------------------------- | ----------------------------------------------------------------------- |
| New deployable             | Yes (Deployment, Service, creds) | None                                                                    |
| `aigw` standalone          | Requires extra process wiring    | Works unchanged                                                         |
| GCP credentials            | Duplicated in cache service      | Reuses `internal/backendauth`/`internal/gcpauth`                        |
| Request-path latency       | Extra network hop                | Direct Google API call only on cache miss                               |
| Multi-replica coordination | Centralized                      | Shared Redis store; Google `cachedContents.list` is the source of truth |
| Pre-warm endpoint          | Built-in                         | Not provided; use explicit `cachedContent` (GCP-created caches)         |

Resolution state lives in a Redis store shared by all replicas, so the replica
that creates a cache publishes the result for the others. Creation is
arbitrated with `SET NX PX`: the replica that claims a key creates while the
others wait for its result, so a cold prefix arriving at N replicas produces
one create rather than N.

This narrows the duplicate-create race rather than closing it. A create that
outlives the lock TTL lets a second replica proceed, and both write; sizing the
lock TTL above the p99 create duration is tuning, not a guarantee. The residual
cost is bounded by the cache TTL, and the resolver still mitigates by
re-listing after create and preferring the oldest match.

Redis is optional. When it is unconfigured the store is inert and caching is
skipped — see Error Handling below. Note that this means **`aigw` standalone no
longer caches unless the user runs a Redis**, a deliberate trade against the
`aigw` parity row above and a consequence of removing the per-replica memo. The
`CacheStore` interface is the seam for restoring it with a small in-process
implementation if standalone parity turns out to matter.

Two mechanisms are needed, not one. Redis alone does not stop an _in-replica_
dogpile: N concurrent requests for a cold prefix all miss the shared store at
the same instant. `singleflight` collapses callers that overlap in time, the
store collapses callers separated by it.

### Schema Extensions

#### 1. `cache_control` markers (already exists)

The OpenAI request schema already parses Anthropic-style `cache_control` on
content parts via `AnthropicContentFields`
(`internal/apischema/openai/openai.go`), embedded inline on text/content-part
structs. `anthropic.CacheControlEphemeralParam` carries `type` and `ttl`.
**No schema change is needed for markers.**

Semantics (per proposal):

- The **last** content part with `cache_control` is the breakpoint.
- Cached prefix = `tools` + `system` + `messages[0 : breakpoint+1]`.
- Everything after the breakpoint is sent as non-cached content.
- TTL comes from the breakpoint marker; default `300s`.

#### 2. Explicit `cachedContent` vendor field (new)

Add to `GCPVertexAIVendorFields` (`internal/apischema/openai/openai.go`, which
is embedded inline in `ChatCompletionRequest`):

```go
// CachedContent specifies a pre-existing Vertex AI cache to use.
// Format: "projects/{project}/locations/{location}/cachedContents/{cache_id}"
//
// When provided, cache_control markers on messages must be absent; the request
// is sent directly to Gemini with this cache reference.
CachedContent string `json:"cachedContent,omitzero"`
```

#### 3. Gemini wire schema (new)

Add to `gcp.GenerateContentRequest` (`internal/apischema/gcp/gcp.go`):

```go
CachedContent string `json:"cachedContent,omitzero"`
```

### Cache Resolver (`internal/gcpcache`)

New package implementing the proposal's "Cache Service Internal Flow" as a
library:

```go
type ResolveResult struct {
	// CacheName is the full Google resource name.
	CacheName string
	// Messages is the non-cached remainder of the conversation.
	Messages []openai.ChatCompletionMessageParamUnion
	// Created is true when this call created the cache (cache write cost applies).
	Created bool
	// TokenCount is the number of tokens written to the cache (from Google's response).
	TokenCount int
	// ExpireTime is the cache expiration timestamp.
	ExpireTime time.Time
}

type CacheResolver interface {
	Resolve(ctx context.Context, req *openai.ChatCompletionRequest, region, project string) (*ResolveResult, error)
}
```

Algorithm:

1. Find the **last** message content part with `cache_control` (breakpoint).
   If none, the resolver is not invoked.
2. Split: cached prefix = tools + system + `messages[0:bp+1]`; remainder =
   `messages[bp+1:]`.
3. Compute a deterministic cache key: SHA-256 over the canonical JSON of
   (model, tools, system, cached messages). The key is stored as the Google
   cache `displayName`.
4. Collapse concurrent callers for the same key with `singleflight`, so a
   dogpile on a cold prefix drives one resolution rather than N.
5. Check the shared store (key → cache name + expiry, expiring with the cache
   TTL). On hit, return without any Google call. An entry within 10 s of
   expiry is treated as a miss so there is time to re-resolve. Store errors
   are logged and treated as a miss.
6. On miss, claim the key for creation (`SET NX PX`). A caller that loses the
   claim waits for the leader to publish, then returns its result; a caller
   that times out waiting resolves itself, which degrades a stuck leader into
   an extra list rather than a duplicate create.
7. Call Google `GET .../cachedContents` for the region, match by
   `displayName` (Google's list only returns non-expired caches).
8. If not found, `POST .../cachedContents` with the translated cached prefix
   (Gemini `contents` + `systemInstruction` + `tools`, reusing the existing
   conversion helpers in `internal/translator/gemini_helper.go`), the model,
   and TTL from the breakpoint marker (default `300s`). After create, re-list
   and prefer the oldest matching cache to converge duplicate-create races.
9. Publish to the store and return `ResolveResult`. Only the caller that
   performed the create reports `Created` and a write-token count, so the
   write is billed once rather than once per collapsed caller.

Auth: reuse the GCP access token already managed for backend auth
(`internal/backendauth/gcp.go` / `internal/gcpauth`) — expose a token source
from the GCP auth handler rather than duplicating credential handling.

JSON: use `internal/json` (enforced by depguard) — never `encoding/json`.

### Region and Project Plumbing

The target region and project already exist in `filterapi.GCPAuth`
(`internal/filterapi/filterconfig.go`), consumed today only by
`internal/backendauth/gcp.go`. They are not visible to the processor or
translator.

Change: when building `RuntimeBackend` (`internal/filterapi/runtime.go`),
surface the GCP region/project (and token source) so the processor can
construct/invoke the resolver for the selected backend. This keeps the "region
is chosen by routing/load-balancing" property from the proposal — the resolver
is always called with the region of the backend the request was routed to.

### Request Path Integration

In `upstreamProcessor` (`internal/extproc/processor_impl.go`), after backend
selection and before translation:

1. If the backend schema is GCP Vertex AI **and** context caching is enabled
   for the backend **and** the request contains `cache_control` markers:
   - Validate: explicit `cachedContent` + markers → `400 invalid_cache_config`.
   - Invoke `CacheResolver.Resolve` with the backend's region/project.
   - Replace the request's messages with the filtered remainder and set the
     resolved cache name as the vendor `CachedContent` before calling the
     translator.
2. The translator (`internal/translator/openai_gcpvertexai.go`) copies
   `GCPVertexAIVendorFields.CachedContent` into
   `gcp.GenerateContentRequest.CachedContent` in `applyVendorSpecificFields`.
   This single code path serves both explicit (user-provided) and automatic
   (resolver-provided) cache references.
3. If caching is not enabled or no markers are present, behavior is unchanged.
   `cache_control` markers are already tolerated and dropped by translation
   today; that remains the behavior when the feature is disabled.

### Cross-Phase State and Token/Cost Accounting

Cache write data (`Created`, `TokenCount`) is known in the **request** phase
but billed in the **response** phase. The proposal lists three options; this
spec uses **extproc stateful context**, matching existing patterns: the
`upstreamProcessor` struct and the per-request translator instance already
persist from request to response (e.g. `costs metrics.TokenUsage`,
`requestModel`, `stream`).

- Store `cacheCreated bool` / `cacheWriteTokens int` on the processor (or
  translator) during the request phase.
- In `ResponseBody` (non-streaming: final body; streaming: the final SSE chunk
  carrying `usageMetadata`), when `cacheCreated` is true, call
  `tokenUsage.SetCacheCreationInputTokens(cacheWriteTokens)`.

Everything downstream already exists:

- `geminiUsageToOpenAIUsage` (`internal/translator/gemini_helper.go`) already
  maps `cachedContentTokenCount` → `prompt_tokens_details.cached_tokens`, and
  `ResponseBody` already calls `SetCachedInputTokens`.
- `internal/metrics` already emits `cached_input` and `cache_creation_input`
  token-type metrics.
- CEL request-cost already supports `LLMRequestCostTypeCachedInputToken` and
  `LLMRequestCostTypeCacheCreationInputToken`
  (`internal/extproc/processor_impl.go`), enabling the proposal's cost formula:

```
cache_write_cost     = cacheWriteTokens × cache_write_rate           (only when created)
cache_read_cost      = cachedContentTokenCount × cache_read_rate
standard_input_cost  = (promptTokenCount − cachedContentTokenCount) × standard_input_rate
output_cost          = candidatesTokenCount × output_rate
```

### Configuration Surface

Automatic caching is opt-in per backend.

filterapi (`internal/filterapi/filterconfig.go`), on `Backend`:

```go
// GCPContextCaching enables automatic Vertex AI context caching driven by
// cache_control markers. Only meaningful for GCP Vertex AI backends.
GCPContextCaching *GCPContextCaching `json:"gcpContextCaching,omitempty"`

type GCPContextCaching struct {
    // DefaultTTL is used when a cache_control marker has no ttl. Defaults to "300s".
    DefaultTTL string `json:"defaultTTL,omitempty"`
}
```

CRD surface: a corresponding field on `AIServiceBackend` in **both**
`api/v1alpha1` and `api/v1beta1` (v1beta1 preferred for new work; mirror per
repo convention), wired to filterapi in `internal/controller/gateway.go` next
to the existing GCP auth wiring. `make apigen codegen apidoc` must be run
after API edits.

Explicit `cachedContent` passthrough requires **no configuration** — it is
pure schema/translation and always available.

`aigw` standalone gains the feature automatically once filterapi carries the
knob (extproc runs in-process; no additional wiring).

### Error Handling

Failures split by whether the user can act on them. Google-side failures fail
fast, with no silent fallback: a create rejected for being below the model's
minimum token count means the `cache_control` markers are misplaced, and
silently serving the request uncached would hide a cost increase behind a 200. Redis-side failures fail open: the store is an optimization, and an
unreachable one must not turn a servable request into an error.

| Condition                                                 | HTTP Status | Error Code              |
| --------------------------------------------------------- | ----------- | ----------------------- |
| Both `cache_control` markers and explicit `cachedContent` | 400         | `invalid_cache_config`  |
| Cache creation failed (e.g., below model minimum tokens)  | 422         | `cache_creation_failed` |
| Google `cachedContents` API error / unreachable           | 502         | `upstream_error`        |
| GCP authentication failure                                | 401         | `gcp_auth_error`        |
| Redis unconfigured                                        | 200         | — (uncached)            |
| Redis unreachable / timeout                               | 200         | — (uncached)            |

Redis failures are swallowed inside the resolver: they are logged at warn level,
treated as a cache miss, and resolution continues against Google. They never
reach the resolver's return value, so a non-nil error from `Resolve` still means
a Google failure and still maps to 502.

Errors are returned as OpenAI-format error bodies via the existing translator
error path. The resolver call respects the ext_proc message timeout budget;
large cache writes that exceed it surface as errors rather than hanging.

### Multi-Region Behavior

Unchanged from the proposal: caches are regional and created **lazily** per
region. The resolver is invoked with the region of the routed backend, so
compound models spanning multiple regions create caches in each region on
first use only. Cost implications (per-region storage, first-request write
cost per region) are documented for users. Explicit `cachedContent` names are
region-bound; a name whose location does not match the routed backend's
region fails at Google — documented, not validated in phase 1.

### Streaming

Cache resolution completes before the upstream request is sent, so streaming
is unaffected on the request side. On the response side, Gemini reports
`usageMetadata` on the final SSE chunk; the existing streaming usage handling
already extracts it, and the cache-write merge described above happens at the
same point.

## User Stories

1. **Large system prompt** — As an application developer, I add
   `cache_control: {"type": "ephemeral"}` to my large system prompt so that
   repeated requests bill cached-token rates, with no other change to my
   OpenAI-format client.
2. **Growing conversation** — As a chat application, I move the
   `cache_control` marker to a later message as the conversation grows, and
   the gateway transparently creates a new, larger cache covering the longer
   prefix.
3. **Custom TTL** — As a developer, I set `"ttl": "3600s"` on my marker so my
   cache lives one hour instead of the 5-minute default.
4. **Pre-created cache** — As a power user, I create a cache directly via
   GCP's `cachedContents` API and pass its resource name in the request's
   `cachedContent` field; the gateway forwards it to Gemini without invoking
   any cache logic.
5. **Cost visibility** — As a platform operator, I see `cached_tokens` in
   `prompt_tokens_details` of responses, `cached_input` /
   `cache_creation_input` token metrics in OpenTelemetry, and can express
   cache read/write rates in CEL request-cost configuration.
6. **Multi-region compound model** — As a platform operator load-balancing a
   compound model across regions, caches are created lazily only in regions
   that receive traffic, and I understand each region incurs its own write
   and storage costs.
7. **Standalone developer** — As a developer using `aigw run` locally, context
   caching works identically to the Kubernetes deployment with no extra
   processes.
8. **Misconfigured request** — As a client sending both markers and an
   explicit `cachedContent`, I receive a clear 400 `invalid_cache_config`
   error instead of ambiguous behavior.

## Implementation Plan

### Phase 1 — Explicit `cachedContent` passthrough

- [x] Add `CachedContent` to `gcp.GenerateContentRequest`
      (`internal/apischema/gcp/gcp.go`).
- [x] Add `CachedContent` to `GCPVertexAIVendorFields`
      (`internal/apischema/openai/openai.go`).
- [x] Copy the vendor field into the Gemini request in
      `applyVendorSpecificFields` (`internal/translator/openai_gcpvertexai.go`).
- [x] Add validation in the gcpvertexai request path: markers + explicit
      `cachedContent` → 400 `invalid_cache_config`.
- [x] Unit tests: passthrough, validation error, marshaled request shape.
- [x] Verify `cachedContentTokenCount` → `cached_tokens` usage mapping with a
      test fixture (mapping already exists; add coverage for the cached case).

### Phase 2 — Cache resolver library

- [x] Expose GCP region/project (and access-token source) from
      `filterapi.GCPAuth` / `internal/backendauth/gcp.go` to the processor via
      `RuntimeBackend` (`internal/filterapi/runtime.go`).
- [x] Create `internal/gcpcache`: breakpoint detection, prefix split,
      deterministic key (SHA-256 → `displayName`), Google `cachedContents`
      list/create client, `CacheStore` (Redis + no-op), `CacheResolver`
      interface. The store began as an in-process TTL memo and moved to Redis;
      see the Redis cache store amendment.
- [x] Reuse `internal/translator/gemini_helper.go` conversion helpers to build
      the cached prefix in Gemini format (export or relocate as needed).
- [x] Unit tests with an `httptest` fake for the `cachedContents` API: hit,
      miss+create, expiry, create race (duplicate `displayName`), TTL default
      and override, auth failure.

### Phase 3 — Request path wiring and cost attribution

- [x] Invoke the resolver from `upstreamProcessor`
      (`internal/extproc/processor_impl.go`) when the backend is GCP Vertex
      AI, caching is enabled, and markers are present; replace messages with
      the filtered remainder and inject the cache name.
- [x] Persist `cacheCreated` / `cacheWriteTokens` across phases on the
      processor/translator; call `SetCacheCreationInputTokens` in
      `ResponseBody` (streaming and non-streaming).
- [x] Map resolver failures to the error table (fail fast).
- [x] Unit tests: end-to-end processor tests with a mocked resolver; usage
      merge in both response modes.

### Phase 4 — Configuration surface

- [x] Add `GCPContextCaching` to `filterapi.Backend`
      (`internal/filterapi/filterconfig.go`) and thread through
      `RuntimeBackend`.
- [x] Add the CRD field to `AIServiceBackend` in `api/v1beta1` and mirror in
      `api/v1alpha1`; run `make apigen codegen apidoc`.
- [x] Wire CRD → filterapi in `internal/controller/gateway.go`.
- [x] Tests: `tests/crdcel` validation, `tests/controller` filterconfig
      rendering.

### Phase 5 — Integration tests and documentation

- [ ] `tests/data-plane` (`test_extproc` tag): fake Gemini upstream +
      fake `cachedContents` server; assert cache create on first request,
      cache hit on second, `cached_tokens` in the OpenAI response, and
      cache-cost dynamic metadata.
- [ ] Real-GCP e2e case that self-skips without credentials (existing
      pattern).
- [ ] Write user documentation (see [Documentation](#documentation)).
- [ ] `make precommit test` clean; DCO-signed commits.

## Documentation

- **Site docs** (`site/`, per `site/AGENTS.md`): a new "Context Caching
  (Vertex AI)" page covering: `cache_control` marker usage and breakpoint
  semantics, TTL defaults, explicit `cachedContent` usage, enabling the
  feature on `AIServiceBackend`, multi-region cost implications, and the
  error table.
- **API reference**: regenerated automatically from CRD comments via
  `make apigen codegen apidoc` — write thorough godoc on the new API fields
  since that is the source of truth.
- **Examples**: request examples (marker-based, explicit, growing
  conversation) in the docs page; optionally an example manifest enabling
  caching on a GCP backend.
- **Proposal cross-link**: note in `proposal.md` (or this file's header) that
  the implementation uses in-process resolution, with the external cache
  service retained as a future option behind `CacheResolver`.

## Success Criteria

1. **Explicit passthrough**: a request with `cachedContent` reaches Gemini
   with the field set verbatim; a matching data-plane test passes.
2. **Automatic caching**: with caching enabled, the first marked request
   creates a Google cache (`created == true` observed via metrics) and the
   second identical request hits it with no create call (verified against the
   fake `cachedContents` server).
3. **Filtered payload**: the upstream Gemini request contains only the
   non-cached remainder plus `cachedContent`; the cached prefix is not resent.
4. **Usage mapping**: responses (streaming and non-streaming) report
   `usage.prompt_tokens_details.cached_tokens` equal to Gemini's
   `cachedContentTokenCount`.
5. **Cost attribution**: `cache_creation_input` token metric is emitted only
   on cache-creating requests with the resolver-reported token count; CEL
   costs using `CacheCreationInputToken` / `CachedInputToken` evaluate
   correctly.
6. **Validation**: markers + explicit `cachedContent` → 400
   `invalid_cache_config` with OpenAI-format error body.
7. **Fail fast on Google, fail open on Redis**: Google failures return the
   mapped 4xx/5xx errors; no request silently proceeds uncached when caching
   was requested, enabled, and the store was reachable. A missing or
   unreachable Redis instead yields 200 and an uncached request, with a
   warn-level log and no error surfaced to the client.
8. **No regression**: with the feature disabled (default), behavior and all
   existing tests are unchanged; markers are dropped as today.
9. **Parity**: the feature works under `aigw run` (standalone) only when a
   Redis is reachable; without one, `aigw` parses markers and serves requests
   uncached. This is a known regression against the per-replica memo, accepted
   when resolution state moved to a shared store.
10. **CI clean**: `make precommit test`, `test-crdcel`, `test-controller`,
    and `test-data-plane` all pass.
