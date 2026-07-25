#!/usr/bin/env bash
# Smoke test for the usage-events-receiver sidecar: builds the binary, starts it, and
# exercises every HTTP endpoint with curl, including durability across a restart.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

WORKDIR="$(mktemp -d)"
STORE_DIR="$WORKDIR/store"
BIN="$WORKDIR/usage-events-receiver"
ADDR="127.0.0.1:18090"
BASE_URL="http://$ADDR"
PID=""
FAILED=0

cleanup() {
  if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1"; FAILED=1; }

start_server() {
  "$BIN" -addr "$ADDR" -store-dir "$STORE_DIR" >"$WORKDIR/server.log" 2>&1 &
  PID=$!
  for _ in $(seq 1 50); do
    if curl -s -o /dev/null "$BASE_URL/healthz"; then
      return 0
    fi
    sleep 0.1
  done
  echo "server did not become healthy; log follows:"
  cat "$WORKDIR/server.log"
  exit 1
}

stop_server() {
  kill "$PID"
  wait "$PID" 2>/dev/null || true
  PID=""
}

echo "==> building $BIN"
mkdir -p "$STORE_DIR"
go build -o "$BIN" .

echo "==> starting receiver on $ADDR (store: $STORE_DIR)"
start_server

echo "==> GET /healthz"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/healthz")
[[ "$code" == "200" ]] && pass "healthz returns 200" || fail "healthz returned $code"

echo "==> GET /readyz"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/readyz")
[[ "$code" == "200" ]] && pass "readyz returns 200" || fail "readyz returned $code"

echo "==> POST /v1/usage-events (new event, pipe-delimited event_id from the proposal's example payload)"
EVENT_ID='req-abc123|llmroute|openai-primary'
BODY=$(cat <<JSON
{
  "schema_version": "v1",
  "event_id": "$EVENT_ID",
  "status": "succeeded",
  "status_code": 200,
  "provider": "openai",
  "backend": "openai-primary",
  "model_requested": "o4-mini",
  "model_response": "o4-mini",
  "input_tokens": 120,
  "output_tokens": 480,
  "cached_input_tokens": 10,
  "cache_write_input_tokens": 0,
  "reasoning_tokens": 320
}
JSON
)
resp=$(curl -s -w '\n%{http_code}' -X POST "$BASE_URL/v1/usage-events" -H 'Content-Type: application/json' -d "$BODY")
code=$(tail -n1 <<<"$resp")
payload=$(sed '$d' <<<"$resp")
if [[ "$code" == "201" ]] && grep -q '"status":"accepted"' <<<"$payload"; then
  pass "publish new event -> 201 accepted"
else
  fail "publish new event -> got $code: $payload"
fi

echo "==> GET /v1/usage-events/<event_id> (URL-encoded)"
encoded_id=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=''))" "$EVENT_ID")
resp=$(curl -s -w '\n%{http_code}' "$BASE_URL/v1/usage-events/$encoded_id")
code=$(tail -n1 <<<"$resp")
payload=$(sed '$d' <<<"$resp")
if [[ "$code" == "200" ]] && grep -q '"output_tokens":480' <<<"$payload"; then
  pass "fetch stored event returns persisted payload"
else
  fail "fetch stored event -> got $code: $payload"
fi

echo "==> POST duplicate event_id (dedup)"
DUP_BODY=$(cat <<JSON
{"event_id": "$EVENT_ID", "output_tokens": 999}
JSON
)
resp=$(curl -s -w '\n%{http_code}' -X POST "$BASE_URL/v1/usage-events" -H 'Content-Type: application/json' -d "$DUP_BODY")
code=$(tail -n1 <<<"$resp")
payload=$(sed '$d' <<<"$resp")
if [[ "$code" == "200" ]] && grep -q '"status":"duplicate"' <<<"$payload"; then
  pass "duplicate publish -> 200 duplicate"
else
  fail "duplicate publish -> got $code: $payload"
fi

echo "==> confirm dedup did not overwrite the original payload"
resp=$(curl -s "$BASE_URL/v1/usage-events/$encoded_id")
if grep -q '"output_tokens":480' <<<"$resp"; then
  pass "dedup preserved first write"
else
  fail "dedup did not preserve first write: $resp"
fi

echo "==> POST invalid JSON"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/v1/usage-events" -H 'Content-Type: application/json' -d '{not json')
[[ "$code" == "400" ]] && pass "invalid JSON -> 400" || fail "invalid JSON -> got $code"

echo "==> POST missing event_id"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/v1/usage-events" -H 'Content-Type: application/json' -d '{"output_tokens": 5}')
[[ "$code" == "400" ]] && pass "missing event_id -> 400" || fail "missing event_id -> got $code"

echo "==> GET unknown event_id"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/v1/usage-events/does-not-exist")
[[ "$code" == "404" ]] && pass "unknown event -> 404" || fail "unknown event -> got $code"

echo "==> GET /v1/usage-events (list)"
resp=$(curl -s "$BASE_URL/v1/usage-events")
if grep -q '"count":1' <<<"$resp"; then
  pass "list returns exactly 1 event (dedup honored)"
else
  fail "list -> $resp"
fi

echo "==> restart the server against the same store-dir and verify durability"
stop_server
start_server
resp=$(curl -s -w '\n%{http_code}' "$BASE_URL/v1/usage-events/$encoded_id")
code=$(tail -n1 <<<"$resp")
payload=$(sed '$d' <<<"$resp")
if [[ "$code" == "200" ]] && grep -q '"output_tokens":480' <<<"$payload"; then
  pass "event survived server restart (JetStream durability)"
else
  fail "event did not survive restart -> got $code: $payload"
fi

stop_server

if [[ "$FAILED" == "0" ]]; then
  echo
  echo "ALL SMOKE TESTS PASSED"
  exit 0
else
  echo
  echo "SMOKE TESTS FAILED"
  exit 1
fi
