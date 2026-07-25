# usage-events-receiver

A reference `UsageEventSink` HTTP receiver for the
[per-request AI usage event export proposal](../../docs/proposals/012-per-request-ai-usage-event-export/proposal.md).

It is a single self-contained binary: an HTTP server backed by an **embedded NATS server with
JetStream** for durable, deduplicated storage. It never opens a NATS network port — JetStream
is only reachable in-process — so the only externally visible surface is the HTTP API described
below. This makes it suitable to run as a sidecar (or DaemonSet, addressed over loopback) next
to Envoy AI Gateway, per the proposal's "colocated receiver" deployment topology: publication is
a local call, and because the receiver is a separate process with its own durable store, events
already accepted survive an extproc crash.

## Why JetStream

The proposal requires the reference receiver to "acknowledge events and deduplicate on
`event_id`". A plain in-memory map would satisfy that until the process restarts. JetStream's
KV store (backed by a file-based stream) gives durability across restarts and an atomic
create-if-absent primitive (`Create`) that maps directly onto "deduplicate on `event_id`" without
extra locking.

## Build & run

```sh
go build -o usage-events-receiver .
./usage-events-receiver -addr 127.0.0.1:8090 -store-dir /var/lib/usage-events-receiver
```

| Flag          | Default            | Description                                    |
| ------------- | ------------------ | ----------------------------------------------- |
| `-addr`       | `127.0.0.1:8090`   | Address the HTTP sink listens on                |
| `-store-dir`  | *(required)*       | Directory JetStream persists usage events to    |

## HTTP API

### `POST /v1/usage-events`

Accepts a single serialized `UsageEvent` JSON object (must include a non-empty `event_id`
field). Returns only after the event has been durably persisted:

- `201 Created` `{"event_id": "...", "status": "accepted"}` — new event, durably stored.
- `200 OK` `{"event_id": "...", "status": "duplicate"}` — an event with this `event_id` was
  already stored; the original write is preserved (idempotent acknowledgement, not
  last-write-wins).
- `400 Bad Request` — malformed JSON or missing `event_id`.
- `503 Service Unavailable` — JetStream failed to persist the write.

### `GET /v1/usage-events/{eventID}`

Returns `{"event_id": "...", "payload": {...}}` for a previously stored event, or `404` if
unknown. `eventID` must be URL-path-escaped (the proposal's `event_id` format uses `|` as a
delimiter, e.g. `req-abc123|llmroute|openai-primary`).

### `GET /v1/usage-events`

Returns `{"count": N, "events": [...]}` for all stored events. Intended for debugging/testing,
not for production-scale polling.

### `GET /healthz` / `GET /readyz`

Liveness and readiness probes. `/readyz` returns `503` until the embedded JetStream store is
initialized.

## Testing

```sh
go test ./...        # unit tests (TDD): dedup, persistence-across-restart, error handling
./smoketest.sh        # builds the binary and exercises every endpoint with curl end-to-end,
                       # including a real process restart to prove durability
```
