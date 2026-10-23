# Specification: Redis-Backed Cache Store for GCP Vertex AI Context Caching

This document amends
[gemini-context-caching.md](./gemini-context-caching.md). That specification
resolves Gemini context caches in-process inside extproc, backed by a
per-replica in-memory TTL memo. This amendment replaces that memo with a
shared Redis store, and adds request collapsing within a replica.

It supersedes two decisions in the parent specification: the in-memory memo is
removed, and the "fail fast, no silent fallback" error policy is narrowed to
apply to Google API failures only. Everything else — breakpoint detection,
prefix splitting, deterministic key derivation, the `CacheResolver` interface,
request-path wiring, and token accounting — is unchanged.

## Table of Contents

<!-- toc -->

- [Context and Constraints](#context-and-constraints)
- [Technical Design](#technical-design)
  - [Layering](#layering)
  - [Request Collapsing (`singleflight`)](#request-collapsing-singleflight)
  - [The `CacheStore` Interface](#the-cachestore-interface)
  - [Redis Store and Create Lock](#redis-store-and-create-lock)
  - [Failure Policy](#failure-policy)
  - [Configuration Surface](#configuration-surface)
  - [Resolver Lifecycle Across Reloads](#resolver-lifecycle-across-reloads)
  - [Interfaces Deliberately Not Introduced](#interfaces-deliberately-not-introduced)
- [Implementation Plan](#implementation-plan)
- [Success Criteria](#success-criteria)

<!-- /toc -->

## Context and Constraints

The parent specification stores resolved cache names in a per-replica map
guarded by `sync.Mutex` (`internal/gcpcache/gcpcache.go`, `memo` field). Two
problems follow from that design.

**Cross-replica duplicate creation.** Each replica has its own memo, so N
replicas seeing the same prefix for the first time each perform an independent
`cachedContents.list` and `create`. The parent spec mitigates by re-listing
after create and preferring the oldest match, but the duplicate write has
already been billed by then. This is the problem this amendment is primarily
motivated by.

**In-replica dogpile.** A second, larger problem that Redis alone does not
solve. In `Resolve`, `r.mu` is held only inside `getMemo` and `setMemo` — it is
released before `listAndMatch` and `createCache` run. Nothing serializes the
List → Create span. N concurrent requests sharing a prefix all miss, all List,
and all Create, an N× amplification inside a _single_ replica, independent of
replica count. A shared store does not fix this: all N still miss the shared
store at the same instant. Request collapsing does.

### Constraints

1. **Redis is optional.** If unconfigured or unreachable, caching is skipped
   silently and the request proceeds uncached. Users who never configure Redis
   see no behavior change and no new failure mode.
2. **Fail-open is scoped to Redis.** Google `cachedContents` failures continue
   to fail fast per the parent spec. These are conditions the user can act on —
   a 422 below-minimum-tokens means their `cache_control` markers are placed
   wrong. Silently serving a full-price uncached request instead would be an
   invisible bill increase with no signal.
3. **No in-memory store.** The memo is removed, not layered beneath Redis.
4. **Redis is a new dependency of this module.** It is worth stating plainly,
   since the reverse is easy to assume: `grep -i redis go.mod go.sum` returns
   nothing today. Redis appears in this repository only as
   `examples/token_ratelimit/redis.yaml` (a manifest users deploy) and in e2e
   tests via `kubectl exec … redis-cli`. The `envoyproxy/ratelimit` container
   is the only component that opens a Redis connection, and it is a separate
   process that AI Gateway configures via xDS rather than talks to. This
   amendment adds the first Redis client to the **data plane** binary.
5. **The ext_proc timeout budget bounds all blocking work.** Resolution happens
   inside `ProcessRequestHeaders`; a request is held open for its duration.
   Any lock-wait must stay well under Envoy's message timeout.
6. **`golang.org/x/sync` is already a direct dependency** (`go.mod:58`,
   v0.21.0), so `singleflight` adds no module.

## Technical Design

### Layering

```
request → singleflight (collapse identical in-flight keys, per replica)
            → CacheStore (Redis, shared across replicas — or no-op)
              → Google cachedContents list → create
```

Each layer removes a distinct class of duplicate work: `singleflight` removes
duplicates within one replica, Redis removes them across replicas, and the
Google List remains the ultimate source of truth for correctness when both
are bypassed or fail.

### Request Collapsing (`singleflight`)

In `internal/gcpcache/gcpcache.go`, add `group singleflight.Group` to
`resolver` (the zero value is ready; no initialization). Extract the
token-fetch → List → Create → re-list span into a
`resolveUncached(ctx, …) (*ResolveResult, error)` method that returns
everything except `Messages`, and wrap the call:

```go
var leader bool
v, err, _ := r.group.Do(cacheKey, func() (any, error) {
    leader = true
    return r.resolveUncached(ctx, …)
})
```

The memo check stays **outside** the flight: a memo hit should not queue behind
an in-flight create for the same key.

Three hazards follow from `singleflight`'s semantics, all of which need tests:

- **Every waiter receives the same value.** Writing `Messages` on the shared
  pointer would cross-contaminate concurrent requests, which differ precisely
  in their non-cached remainder. Each caller must copy the result struct and
  set its own `Messages` on the copy.
- **Only the leader may report `Created`.** Only the flight leader performed
  the write. If all N waiters return `Created: true`, the request path calls
  `SetCacheCreationInputTokens` N times and CEL over-bills the write — the
  exact cost this feature exists to reduce.

  Do **not** use `Do`'s third return value to identify followers. It reports
  that the value was delivered to more than one caller and is true for the
  leader as well, so it cannot distinguish them; zeroing on it drops `Created`
  for every caller, and the write is then billed by nobody. Capture leadership
  inside the closure instead — the closure body executes only in the leader's
  call — and zero `Created`/`TokenCount` when `!leader`.

- **The leader owns the context.** If the leader's request is cancelled, the
  flight fails for its followers too. This is acceptable — followers return an
  error and the next request retries — but it should carry a comment rather
  than be left implicit.

### The `CacheStore` Interface

Storage moves behind a two-method interface:

```go
// CacheStore is the storage layer behind the resolver: cache key → resolved
// Google cache name and its expiry.
type CacheStore interface {
	Get(ctx context.Context, key string) (entry, bool, error)
	Set(ctx context.Context, key string, e entry, ttl time.Duration) error
}
```

Two implementations: a Redis-backed store, and a no-op store used when Redis is
unconfigured (`Get` always misses, `Set` discards). The no-op store is what
makes "unconfigured → uncached" fall out of the design naturally, rather than
requiring a nil check at every call site.

**Locking stays private to the Redis implementation.** `SET NX PX` plus
follower polling has no meaningful analogue in a no-op store, and putting
`TryLock`/`Unlock` in the contract would add two no-op methods while freezing a
protocol that is still unsettled. A two-method interface is cheap to widen
later; a four-method one is not cheap to narrow.

### Redis Store and Create Lock

New file `internal/gcpcache/redis.go`, implementing `CacheStore` — **not**
`CacheResolver`. `resolver` keeps implementing `CacheResolver` and simply holds
a different store, so `internal/extproc/processor_impl.go` and its `SetBackend`
path need no changes at all.

Client: `github.com/redis/go-redis/v9`.

- **Read**: `GET key`. A hit returns the stored cache name and expiry.
- **Write**: `SET key <sentinel> NX PX <lockTTL>` as the create lock.
  - **Lock won** → List, Create, then `SET key <name> PX <remaining-ttl>`.
  - **Lock lost** → poll `GET key` until the sentinel is replaced by a real
    name, or `WaitTime` elapses. On timeout, fall through to **List, not
    Create**, so a stuck leader degrades into an extra read rather than a
    duplicate write.

**This narrows the cross-replica race; it does not close it.** If a Create
outlives `LockTTL`, another replica acquires the lock and both write. Sizing
`LockTTL` above p99 create duration is tuning, not a guarantee. The bound on
the damage remains what the parent spec established: duplicate writes are
capped by the cache TTL, and the post-create re-list converges replicas onto a
single entry.

`WaitTime` must stay well under the ext_proc message timeout — a polling
follower is blocking inside `ProcessRequestHeaders`.

### Failure Policy

This table supersedes the Error Handling table in the parent specification.
The Google rows are unchanged from it; the Redis rows are new.

| Condition                                                  | Behavior                    |
| ---------------------------------------------------------- | --------------------------- |
| Redis unconfigured                                         | uncached, 200 OK            |
| Redis unreachable / dial failure / timeout / GET/SET error | uncached, 200 OK            |
| Redis lock acquisition error                               | uncached, 200 OK            |
| Both `cache_control` markers and explicit `cachedContent`  | 400 `invalid_cache_config`  |
| Cache creation failed (e.g. below model minimum tokens)    | 422 `cache_creation_failed` |
| Google `cachedContents` API error / unreachable            | 502 `upstream_error`        |
| GCP authentication failure                                 | 401 `gcp_auth_error`        |

Redis errors are handled **inside** the resolver: logged at warn, treated as a
cache miss, and execution continues to Google. They never reach `Resolve`'s
return value. This has a useful consequence — the processor wiring at
`internal/extproc/processor_impl.go:355-374` needs no modification. A non-nil
error from `Resolve` still means a Google failure and still maps to 502,
exactly as today.

With no in-memory fallback, "Redis unconfigured" means context caching is
simply inert: `cache_control` markers are ignored and no `cachedContents` calls
are made. This is observationally identical to
`GCPContextCaching.Enabled == false`, which is why the configuration must
document it (see below).

### Configuration Surface

Extend the existing `GCPContextCaching` in
`internal/filterapi/filterconfig.go` (L207):

```go
type GCPContextCaching struct {
	Enabled    bool
	DefaultTTL string
	// Redis is the shared cache store. When nil, context caching is inert:
	// cache_control markers are ignored and no cachedContents calls are made.
	Redis *GCPCacheRedis `json:"redis,omitempty"`
}

type GCPCacheRedis struct {
	URL string // host:port
}
```

`LockTTL` and `WaitTime` are deliberately **not** configuration. They start as
package constants in `internal/gcpcache` and are promoted only if data-plane
testing shows real tuning pressure: they are internal to the lock protocol,
`WaitTime` is bounded by Envoy's ext_proc message timeout rather than by user
preference, and adding fields later is additive whereas removing them is a
breaking API change.

Mirror onto the CRD in **both** `api/v1beta1/ai_service_backend.go`
(`GCPContextCachingSpec`, L92) and `api/v1alpha1/ai_service_backend.go`,
matching the existing kubebuilder validation style. Wire through
`internal/controller/gateway.go`beside the existing`GCPContextCaching`mapping. Redis credentials belong in a`SecretRef`— check what`BackendSecurityPolicy` already uses for secret material and follow it rather
than inventing a new shape.

`Enabled: true` with `redis` unset is a legal combination that silently does
nothing. Godoc on the CRD field is the right place to say so, since the API
reference generates from it.

`LockTTL` and `WaitTime` start as package constants and are promoted to config
only if data-plane testing shows real tuning pressure.

### Resolver Lifecycle Across Reloads

`internal/extproc/server.go` `LoadConfig` currently constructs
`gcpcache.New(nil)` for every GCP backend on every filterapi hot-reload,
discarding the memo each time. The comment at L88-90 claiming the memo is
preserved is false today.

Removing the memo deletes that bug rather than fixing it, but the resolver
should still be carried across reloads so the Redis connection pool is not torn
down and rebuilt on each config update. Keep a
`map[string]gcpcache.CacheResolver` on `Server` keyed by backend name: reuse
the entry for unchanged backends, and drop entries for backends that
disappeared or had caching disabled. Correct the stale comment while there.

### Interfaces Deliberately Not Introduced

**No `CacheBackend` interface** over `listAndMatch`/`createCache`. There is one
implementation and no second on the horizon. The existing tests drive those
methods through `httptest` against a fake Google server, which exercises real
JSON marshaling and the `gcp.CreateCachedContent` wire shape — strictly better
coverage than stubbing would give, and it is what caught the `TTL
time.Duration` serialization bug. Revisit only if a Gemini-API-vs-Vertex-AI
split appears, which is not in the proposal.

## Implementation Plan

### Phase A — Request collapsing

Purely additive. No behavior change when uncontended, no new module, and the
in-memory memo stays in place for this phase — deleting it before Redis exists
would make every marked request pay a Google List round-trip for the whole gap
between phases.

- [x] Add `group singleflight.Group` to `resolver`
      (`internal/gcpcache/gcpcache.go`).
- [x] Extract token-fetch → List → Create → re-list into `resolveUncached`,
      returning everything except `Messages`.
- [x] Wrap the call in `group.Do`, leaving the memo check outside the flight;
      copy the result per caller and set `Messages` on the copy.
- [x] Capture leadership inside the closure and zero `Created`/`TokenCount`
      when `!leader`. Do not use `Do`'s third return value for this — it is
      true for the leader too, so zeroing on it leaves the write billed by
      nobody.
- [x] Comment the leader-owns-context cancellation behavior.
- [x] Make the fake server's `listCalls`/`createCalls` counters atomic before
      adding concurrent tests — they are incremented from per-request handler
      goroutines and would otherwise race under `-race`.
- [x] Test (`-race`): N goroutines, identical request, counting fake-Google
      handler → exactly one Create; all N receive the same `CacheName`.
      Note that this test needs a `CacheStore` once the memo is gone, because
      `singleflight` alone does not give the property: a goroutine scheduled
      just after the flight completes opens a _new_ flight, and the fake list
      still returns empty, so it creates a second time. The two mechanisms
      cover different callers — `singleflight` collapses callers that overlap
      in time, the store collapses callers separated by it — and the
      end-to-end "exactly one create" claim requires both.
- [x] Test: exactly one result has `Created == true` and non-zero
      `TokenCount`; all others are `false` / `0`.
- [x] Test: concurrent callers with different remainder messages each receive
      their own `Messages`.

### Phase B — `CacheStore`, Redis, and memo removal

- [x] Define `CacheStore` and the no-op implementation in
      `internal/gcpcache`.
- [x] Delete `memoEntry`, the `memo` map, `getMemo`, `setMemo`, and the
      10-second stale-eviction logic; `resolver` holds a `CacheStore`.
- [x] Add `github.com/redis/go-redis/v9`; `go mod tidy` via `make precommit`.
- [x] Implement the Redis store in `internal/gcpcache/redis.go`: `GET` read,
      `SET NX PX` create lock, follower polling, timeout falling through to
      List rather than Create.
- [x] Swallow Redis errors inside the resolver (log at warn, treat as miss,
      continue to Google). Do not swallow Google errors.
- [x] Extend `filterapi.GCPContextCaching` with `Redis *GCPCacheRedis`.
- [x] Mirror onto `api/v1beta1` and `api/v1alpha1` backend specs with
      kubebuilder validation; document that `Enabled` without `redis` is
      inert.
- [x] Wire through `internal/controller/gateway.go`.
- [x] Run `make apigen codegen apidoc` and commit generated output.
- [x] Cache resolvers across reloads on `Server`
      (`internal/extproc/server.go`); fix the stale comment at L88-90.
- [x] Test with a fake `CacheStore` (`memStore` in `gcpcache_test.go`): it
      implements `CacheStore` but not `locker`, which is exactly the no-op
      store's shape, and its `getErr`/`setErr` hooks drive the fail-open path.
      The lock-lost and lock-expiry cases moved to the `miniredis` tests
      instead: locking lives entirely inside the Redis store, so a fake that
      does not implement `locker` cannot exercise it, and simulating `SET NX PX`
      by hand would only assert the fake.
- [x] Test with `miniredis` for real `SET NX PX` semantics, and for the
      cross-instance case: two resolvers sharing one miniredis racing on the
      same key produce exactly one Create against the fake Google server.
- [x] Test fail-open end to end: Redis down, Google healthy → 200, no cache
      applied, no error surfaced.
- [x] Confirm fail-fast preserved: the existing 422-on-create-failure
      assertion (`internal/gcpcache/gcpcache_test.go:444`) passes unchanged.
      Review the 502 assertion at
      `internal/extproc/processor_impl_test.go:2393` — it stays valid so long
      as it simulates a Google failure, not a Redis one.
- [ ] Extend the existing fake-Gemini + fake-`cachedContents` setup in
      `tests/data-plane` rather than building new infrastructure.

### Phase C — Parent specification amendments

- [x] Amend `gemini-context-caching.md`: replace the Error Handling table
      (L242-249) with the Failure Policy table above, and scope the
      "fail fast, no silent fallback" statement to Google failures.
- [x] Also update the resolution algorithm (steps 4-7), which still described
      memo lookup and memo population, and the Phase 2 implementation item
      naming the "in-memory TTL memo". These were not in the original Phase C
      list but describe the same removed component.
- [x] Amend success criterion 7 (L405) accordingly.
- [x] Update the architecture-decision table (L39-46): the
      "Multi-replica coordination" row no longer reads "per-replica memo".
- [x] Note the `aigw` standalone consequence (see Success Criteria below).

## Success Criteria

1. **In-replica collapsing**: N concurrent identical requests on one replica
   produce exactly one `cachedContents.create` and one `list`.
2. **Correct write accounting**: exactly one of those N requests reports
   `Created` and a non-zero write-token count; CEL cost and the
   `cache_creation_input` metric are billed once, not N times.
3. **No cross-contamination**: concurrent callers sharing a cached prefix each
   receive their own non-cached remainder in `Messages`.
4. **Cross-replica collapsing**: two extproc instances sharing one Redis and
   racing on the same key produce exactly one create against Google.
5. **Fail-open on Redis**: with Redis stopped or unconfigured, requests
   carrying `cache_control` markers return 200 and proceed uncached, with no
   error surfaced to the client and a warn-level log emitted.
6. **Fail-fast on Google preserved**: create failure still returns 422
   `cache_creation_failed`; list/create errors still return 502
   `upstream_error`; auth failure still returns 401 `gcp_auth_error`.
7. **No in-memory state**: the resolver holds no map of cache names;
   `internal/gcpcache` has no `sync.Mutex`-guarded cache.
8. **Connection reuse**: a filterapi hot-reload does not rebuild Redis
   connection pools for unchanged backends.
9. **No regression when disabled**: with `GCPContextCaching.Enabled` false —
   still the default — behavior and all existing tests are unchanged.
10. **Known regression, accepted**: `aigw run` standalone has no Redis, so
    context caching is unavailable there unless the user runs one. This is a
    real loss against the parent spec's criterion 9 (standalone parity) and a
    direct consequence of removing the in-memory store. If parity later
    matters, the remedy is a small in-process `CacheStore` implementation used
    when `Redis` is nil — the interface is already the seam, and the resolver
    needs no new code path.
11. **CI clean**: `make precommit test`, `test-crdcel`, `test-controller`, and
    `test-data-plane` all pass; `make apigen codegen apidoc` output is
    committed.
