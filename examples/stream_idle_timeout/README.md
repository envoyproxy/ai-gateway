# Stream idle timeout with automatic backend fallback

This example demonstrates the `StreamIdleTimeout` field on `AIGatewayRouteRule`, a per-rule deadline on how long Envoy will wait between the response bytes from an upstream model backend on a streaming response.

There are two example backends configured:

1. Never sends any bytes. This is a Time To First Token (TTFT) timeout, in this case we fallover to the next backend.
2. Delivers a few bytes, and then stops, in this case we fail with a 504 error.

The rule in `base.yaml` is configured with:

- `timeouts.request: 30s` - the overall response deadline.
- `streamIdleTimeout: 5s` - Envoy will reset the upstream stream if no byte has arrived within 5s of the request starting, or the last byte arriving.

The backends are defined in `backends.yaml`

There are 2 rules, with two backends each:

- `stream-idle-timeout-silent-demo`:
  - `stream-idle-timeout-silent` (priority 0): accepts the TCP connection and then holds it open without sending anything, always triggering the stream timeout.
  - `stream-idle-timeout-healthy` (priority 1): the standard `testupstream` configured to return a streaming OpenAI response.
- `stream-idle-timeout-semi-silent-demo`:
  - `stream-idle-timeout-semi-silent` (priority 0): accepts the TCP connection, sends some bytes and then holds it open without sending anything, always triggering the stream timeout deadline.
  - `stream-idle-timeout-healthy` (priority 1): the standard `testupstream` configured to return a streaming OpenAI response.

## Prerequisites

A Kubernetes cluster with Envoy Gateway and the Envoy AI Gateway installed. The quickest way to get there locally is the [Getting Started guide](https://aigateway.envoyproxy.io/docs/getting-started/).

## Apply

```shell
kubectl apply -f examples/stream_idle_timeout/backends.yaml
kubectl apply -f examples/stream_idle_timeout/base.yaml

# Wait for the Gateway to become ready.
kubectl wait --for=condition=Programmed gateway/stream-idle-timeout --timeout=120s
```

## Verify

### Fallback

Port-forward the Gateway and send a streaming chat completion. The first attempt should hit the stalling backend, stall for ~5s, then fall over to the healthy backend, which returns a response.

```shell
# The Service that fronts the Gateway is named envoy-<ns>-<gateway>-<hash>.
GATEWAY_SVC=$(kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=stream-idle-timeout \
  -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n envoy-gateway-system "svc/$GATEWAY_SVC" 8080:80 &

curl -w '\nstream_timeout=%{time_starttransfer}s total=%{time_total}s\n' \
  -X POST http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
        "model": "stream-idle-timeout-silent-demo",
        "stream": true,
        "messages": [{"role":"user","content":"hi"}]
      }'
```

You should see something along the lines of:

```json
{"choices": [{"index": 0,"message": {"role": "assistant","content": "The quick brown fox jumps over the lazy dog."},"finish_reason": "stop"}],"usage": {"prompt_tokens": 1,"completion_tokens": 100,"total_tokens": 300}}
stream_timeout=5.084152s total=5.084261s
```

`stream_timeout` should be just over `5s`, as that means the stalling backend held the stream until the per-try idle timer fired, after which Envoy retried to the healthy backend.

### Failure

Send this instead to test the failure case, where the backend sends some bytes and then stalls, which will trigger the stream idle timeout and return a 504 error.

```shell
curl -w '\nstream_timeout=%{time_starttransfer}s total=%{time_total}s\n' \
  -X POST http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
        "model": "stream-idle-timeout-semi-silent-demo",
        "stream": true,
        "messages": [{"role":"user","content":"hi"}]
      }'
```

You should see something along the lines of:

```json
data: hello

curl: (18) transfer closed with outstanding read data remaining

stream_timeout=0.017271s total=6.527017s
```
