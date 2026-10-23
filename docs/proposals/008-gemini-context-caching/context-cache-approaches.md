# Context Cache Deduplication: Approaches Considered

This document records the approaches evaluated for deduplicating GCP Vertex AI
`cachedContents` creation across gateway replicas, with the reasoning for each
rejection and for the final selection.

It supersedes the "Redis Store and Create Lock" section of
[gemini-cntext-cache-redis.md](./gemini-cntext-cache-redis.md). Everything else
in that amendment — the `CacheStore` interface, the failure policy, the
configuration surface, and the resolver lifecycle across reloads — is unchanged.

## Table of Contents

<!-- toc -->

- [A1. Per-replica in-memory memo](#a1-per-replica-in-memory-memo)
- [A2. `singleflight` request collapsing](#a2-singleflight-request-collapsing)
- [A3. Redis store with `SET NX PX` create lock](#a3-redis-store-with-set-nx-px-create-lock)
- [A4. Drop the lock, make LIST the sole dedupe](#a4-drop-the-lock-make-list-the-sole-dedupe)
- [A5. Cache the LIST response as a blob in Redis](#a5-cache-the-list-response-as-a-blob-in-redis)
- [A6. Warm Redis from the pages already walked](#a6-warm-redis-from-the-pages-already-walked)
- [A7. Negative caching (tombstones)](#a7-negative-caching-tombstones)
- [A8. Bound the scan (page cap, wall-clock, probabilistic)](#a8-bound-the-scan-page-cap-wall-clock-probabilistic)
- [A9. Claim key in Redis, create in GCP, delete on failure](#a9-claim-key-in-redis-create-in-gcp-delete-on-failure)
- [A10. Drop LIST entirely](#a10-drop-list-entirely)
- [A11. Selected — Redis-only lookup with background reconciler](#a11-selected--redis-only-lookup-with-background-reconciler)
- [Decision Summary](#decision-summary)

<!-- /toc -->

## A1. Per-replica in-memory memo

The original design. Each replica kept resolved cache names in a
`sync.Mutex`-guarded map.

**Pros.** No external dependency. Works in `aigw run` standalone. Zero network
cost on a hit.

**Cons.** N replicas each create independently for the same prefix. The memo is
discarded on every filterapi hot-reload. Does not collapse even within a
replica, since the mutex is released before List and Create run.

**Rejected.** Cross-replica duplication was the motivating problem, and an
in-process map cannot address it by construction.

## A2. `singleflight` request collapsing

Wrap the List → Create span in `singleflight.Group.Do` so that concurrent
identical keys within one replica share a single flight.

**Pros.** Addresses the in-replica dogpile, which a shared store cannot: N
concurrent requests on one replica all miss Redis at the same instant, so only
request collapsing prevents N× amplification. Purely additive, and
`golang.org/x/sync` is already a dependency.

**Cons.** Three sharp edges, all of which need care: every waiter receives the
same value, so `Messages` must be copied per caller or concurrent requests
cross-contaminate; only the flight leader may report `Created`, or the write is
billed N times; and the leader owns the context, so its cancellation fails the
flight for all followers.

**Implemented, then reverted.** Not currently in the tree. Its absence means
duplicate creation is presently per-request rather than per-replica. It remains
orthogonal to — and compatible with — every option below, and can be
reintroduced independently.

## A3. Redis store with `SET NX PX` create lock

The design currently in the tree. The first replica to claim a key via
`SET NX PX <sentinel>` creates the cache; others poll (`awaitLeader`) for up to
5s, then fall through to resolving themselves.

**Pros.** Narrows the cross-replica race to roughly one create per cold prefix.
Shares one key namespace for both the lock and the resolved entry, so publishing
the result *is* the unlock. With A2 reverted, it is also the only mechanism
currently collapsing in-replica callers.

**Cons.**

- **Not a guarantee.** Losing the lock does not skip the create — the waiter
  falls through to list-then-create. The code says so: "this is the duplicate
  the lock narrows but cannot fully eliminate."
- **Blocks the request path.** A waiter polls every 50ms for up to 5s inside
  `ProcessRequestHeaders` — up to ~100 Redis round trips per waiting request.
- **Two timeout constants with no empirical basis.** `lockTTL` (30s) must exceed
  p99 create latency; `lockWaitTime` (5s) must stay under the ext_proc message
  timeout. Neither was derived from measurement, and neither was verified
  against the deployed ext_proc configuration.
- **Hides billed creates.** The post-create converge branch returns
  `Created: false` and discards `TokenCount` even though a create occurred and
  was billed, so cache-write token metrics under-count.
- **Machinery cost.** A Lua CAS-delete for `unlock`, a sentinel value
  special-cased in `Get`, and four tuning constants.

**Rejected.** It pays a blocking wait, ~100 round trips, and non-trivial
protocol machinery to narrow a window that closes by itself once the first
create lands — for a cost the project has decided to accept instead.

## A4. Drop the lock, make LIST the sole dedupe

Remove the lock; fix LIST pagination so it becomes a reliable backstop; keep the
inline LIST on every store miss.

**Pros.** Deletes the lock protocol entirely. LIST matches deterministically on
`displayName`, so it does find a peer's cache. Worst-case request latency on a
cold prefix falls by up to `lockWaitTime`.

**Cons.** Makes a full client-side scan mandatory on every cold resolve.
`cachedContents.list` accepts only `parent`, `pageSize`, and `pageToken` —
there is no server-side `filter` or `orderBy`, verified against the Vertex AI
discovery document — so matching must happen client-side over every entry in the
project+region. In the worst case a request walks to the page cap and then
creates anyway, having paid N round trips to learn nothing. Requires pagination,
a page bound, and tuning for both.

**Rejected.** The premise was never tested. This design carries pagination, a
page cap, and later warm-on-walk machinery to protect a mechanism whose hit rate
was never measured. If LIST rarely finds anything Redis did not already have,
all of it is dead weight on the hot path.

## A5. Cache the LIST response as a blob in Redis

Store the whole list result under one Redis key and scan it client-side.

**Pros.** One LIST amortized across many requests.

**Cons.**

- **Wrong key shape.** Lookups ask "does a cache exist for *this* key?", which
  Redis already answers with a direct `GET`. A blob is addressed by a key we do
  not have — there is one list per project, not one per prefix.
- **No correct TTL.** Every entry has its own independent expiry (300s default,
  up to 3600s). A blob TTL is wrong in both directions: it can report an expired
  cache — injecting a dead `cachedContent` name upstream, a *wrong answer*
  rather than a redundant create — or miss a fresh one.
- **Write amplification.** Every listing replica rewrites a potentially
  multi-thousand-entry blob, versus ~100 bytes per key today.

**Rejected.** Strictly worse than the per-key entries already stored, and the
only option evaluated that can produce an incorrect result rather than a
redundant create.

## A6. Warm Redis from the pages already walked

Keep the inline LIST, but fan every entry seen during the walk into its own
per-key `SET`, each with its TTL pinned to that entry's own `expireTime`.

**Pros.** Turns the scan from pure cost into cache-warming. Same single LIST,
but yields N per-key entries in the shape every future lookup uses.
Demand-driven — no leader, no lifecycle, no staleness window beyond what already
exists.

**Cons.** Only warms keys actually paged through, and early-return-on-match
means few pages are walked in the common case — so it warms least when the cache
is healthiest. Still leaves the scan on the request path.

**Folded into A11.** Its core idea — populate per-key entries from a LIST walk,
using `SET NX` so a stale read never clobbers a fresher entry — is carried into
the reconciler, where a full walk is affordable.

## A7. Negative caching (tombstones)

Store a short-lived marker for keys a scan did not find, so a genuinely new
prefix does not re-scan on every request during its cold window.

**Pros.** Cheap. Directly reduces repeat scans for cold keys.

**Cons.** The TTL must be very short or it suppresses discovery of a peer's
create — precisely the failure the scan exists to prevent. The band between "too
short to help" and "long enough to cause the harm it was added to avoid" is
narrow.

**Rejected.** Poor risk/benefit, and made moot by A11 removing the request-path
scan altogether.

## A8. Bound the scan (page cap, wall-clock, probabilistic)

Cap pages (`listMaxPages`); or bound by deadline rather than page count; or scan
on only a sampled fraction of cold misses.

**Pros.** Each bounds worst-case request latency. A deadline adapts to real page
latency better than a page count does. Sampling converges to the same dedupe at
a fraction of the LIST load.

**Cons.** None of them *avoid* the scan — they bound its cost. Each adds a knob
requiring justification, and each degrades to "create anyway", which is the
outcome A11 reaches without the scan at all.

**Partially adopted.** Rejected as a primary mechanism. A hard page bound is
retained wherever a LIST walk survives (A11's reconciler), purely as a backstop
against a malfunctioning `nextPageToken` chain.

## A9. Claim key in Redis, create in GCP, delete on failure

Write a Redis key before creating, create in GCP, delete the key if the create
fails.

**Pros.** Intuitive. Keeps coordination in the centralized store. Avoids LIST.

**Cons.** This is A3 with different naming. "Claim a key, do work, release on
failure" is `SET NX` → create → `unlock`. It needs a TTL, or a crashed replica
blocks the prefix forever — which reintroduces `lockTTL` and the question of how
to size it. The second replica still must choose between waiting — that is
`awaitLeader` and its 5s on the request path — or proceeding, in which case the
claim bought nothing.

**Rejected.** Reintroduces the exact protocol being removed, including the
unjustified timeout constants, while appearing to be a simplification.

## A10. Drop LIST entirely

Request path becomes `GET` → miss → `CREATE` → `SET`. No list, no pagination, no
reconciler.

**Pros.** Deletes more code than the lock removal: `listAndMatch`,
`modelMatchesSuffix`, `cachedContentItem`, `listResponse`, and all pagination
concerns. Three steps a reader can hold in their head. Every failure mode is a
duplicate create — never a wrong answer, never a hang, never a scan.

**Cons.** Nothing ever repairs Redis/GCP drift. After a flush or eviction, every
prefix re-creates once and the forgotten caches bill until TTL with nothing able
to find them. At a 300s TTL that is noise; at `"1h"` it is real.

**Adopted as the request path, rejected as the complete design.** A11 keeps this
exact hot path and adds drift repair where a full scan is affordable.

## A11. Selected — Redis-only lookup with background reconciler

**Request path.** Redis `GET`. Key holds a cache id → use it. Key absent →
create in GCP, `SET` the result, return. No waiting on another replica, no LIST,
no scan.

**Reconciler.** A background task periodically walks `cachedContents.list`
(paginated, page-bounded) and writes each entry as its own key with its TTL
pinned to that entry's `expireTime`, using `SET NX` so it fills gaps without
clobbering fresher entries.

**Pros.**

- The hot path is three steps, identical to A10 — the best failure posture of
  any option considered.
- The scan moves to where it belongs. A full walk with no server-side filter is
  a background activity, not a per-request one.
- The reconciler is never load-bearing. If it is late, wedged, or dead, the
  request path still works and simply produces more duplicates — a cost already
  accepted. This differs materially from a poller whose staleness could produce
  a wrong answer; here it can only produce a redundant create.
- Duplicate creates become visible for free. Deleting the converge branch means
  every create reports `Created: true` with its real `TokenCount`, which already
  flows `ResolveResult.TokenCount` → `GCPCacheResult.WriteTokenCount` →
  `cacheWriteTokens` → `SetCacheCreationInputTokens`. No new metrics plumbing.
- The sentinel state disappears. Nothing writes a "claimed but no id" value, so
  a key either holds an encoded entry or does not exist. A stale `\x00creating`
  left by an old replica during a rolling deploy fails `decodeEntry`, reads as a
  miss, and the replica creates — correct, with no migration step.

**Cons.**

- Replicas racing the same cold prefix each create; each keeps and bills its
  own. Bounded by how many replicas hit the prefix before the first `SET` lands.
- With A2 reverted, this duplication is currently per-request rather than
  per-replica: concurrent identical requests on one replica each create.
- Cold-prefix create QPS toward GCP rises by up to a factor of replica count.
  Worst-case ext_proc latency on a cold prefix *falls* by up to `lockWaitTime`.
- Orphans bill until TTL between reconciler runs.
- Adds a background component with its own lifecycle, which A10 does not have.

**Selected** because it combines the request path with the best failure
characteristics (A10) with drift repair (A6's idea) relocated to where a full
scan is affordable. Every failure mode degrades to a duplicate create — a cost
that is bounded, self-healing, and visible in metrics — rather than to a wrong
answer, a blocked request, or an unbounded scan.

**Reconciler design points.**

- Per-key entries, never a blob (A5).
- `SET NX` for reconciler writes, so a slow walk cannot overwrite an entry a
  live request just published.
- Fleet-wide rate limiting, not leader election. Every replica walking every
  interval is N× the LIST quota and scales the wrong way; true leader election
  reintroduces the coordination primitive being removed. A Redis key used as a
  soft interval gate (`SET NX` with the interval as TTL) rate-limits the
  reconciler across the fleet, and a missed round is harmless by construction.
- Page-bounded (A8) as a backstop against a malfunctioning token chain.

## Decision Summary

| #   | Approach                                  | Outcome                | Primary reason                                                                    |
| --- | ----------------------------------------- | ---------------------- | --------------------------------------------------------------------------------- |
| A1  | Per-replica in-memory memo                | Rejected               | Cannot address cross-replica duplication                                          |
| A2  | `singleflight` request collapsing         | Implemented, reverted  | Orthogonal to the rest; reintroducible independently                              |
| A3  | Redis `SET NX PX` create lock             | Removed                | Blocks the request path; not a guarantee; unjustified timeouts; hides billed creates |
| A4  | Lock removed, LIST sole dedupe            | Rejected               | Mandatory full scan per cold resolve; LIST hit rate never measured                |
| A5  | LIST response cached as a blob            | Rejected               | Wrong key shape; no correct TTL; only option that can return a *wrong* answer     |
| A6  | Warm Redis from walked pages              | Folded into A11        | Right idea, wrong location — belongs in the reconciler                            |
| A7  | Negative caching / tombstones             | Rejected               | TTL band between useless and harmful is too narrow                                |
| A8  | Bound the scan                            | Partially adopted      | Bounds cost but never avoids it; retained as reconciler backstop                  |
| A9  | Claim key, create, delete on failure      | Rejected               | Is A3 renamed, including the timeouts it was meant to avoid                       |
| A10 | Drop LIST entirely                        | Adopted as request path | Best failure posture; no drift repair on its own                                  |
| A11 | **Redis-only lookup + background reconciler** | **Selected**       | A10's hot path plus drift repair where a full scan is affordable                  |
