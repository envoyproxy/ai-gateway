**Description**

This commit replaces the external `envoyproxy/ratelimit` + Redis dependency with an in-process Go rate limit gRPC service backed by a pluggable `Store` interface. It adds a Lua HTTP filter to fan out JWT group claims into request headers for hierarchical bucket rule matching, and adds E2E test coverage for file-backed storage and nested budget enforcement.

The `Store` interface (`Increment(ctx, counter, limit, delta)`) has three backends:

- **PostgreSQL** — `INSERT ... ON CONFLICT DO UPDATE` with window expiry
- **File** — `syscall.Flock` for cross-process mutual exclusion on an emptyDir volume
- **Redis** — Lua scripting for atomic compare-and-increment with TTL

The in-process gRPC service receives merged rate limit configs via a `ConfigObserver` callback on the xDS runner, keeping the service config in sync without a separate xDS connection.

The JWT fanout Lua filter (`envoy.filters.http.lua/ai-gateway-jwt-fanout`) reads the `groups` claim from JWT authn dynamic metadata and fans it out to repeated `x-jwt-groups` headers. It is inserted before the rate limit filter when `--enableJWTGroupFanout=true`, enabling bucket rules that match on group membership.

New CLI flags: `--storageBackend`, `--storagePostgresDSN`, `--storageFileDir`, `--storageRedisURL`, `--enableJWTGroupFanout`. Corresponding Helm values under `controller.storage.*` and `controller.enableJWTGroupFanout`.

Fixes a CRD validation regression where `ServiceQuota` was a value type, causing `omitempty` to serialize `{}` and triggering a spurious `required [quota]` error that blocked finalizer attachment on QuotaPolicies without a `serviceQuota` field.

**Related Issues/PRs (if applicable)**

None.

**Special notes for reviewers (if applicable)**

The nested budget E2E test (`Test_NestedTeamBudgets`) requires the mock LLM image (`docker.io/envoyproxy/ai-gateway-mockllm:latest`) built by `make build-e2e`. The Envoy proxy pod may time out waiting for the `envoyproxy/envoy` image to pull in kind; a pre-pulled image or local registry mirror helps. The test manifest at `tests/e2e/testdata/nested_budget.yaml` includes `dex.yaml` resources for the full JWT flow but the test currently sends the `x-jwt-groups` header directly without Dex — restoring the full JWT path is a follow-up.
