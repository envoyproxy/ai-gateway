package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	ratelimitGatewaySelector = "gateway.envoyproxy.io/owning-gateway-name=test-aig-ratelimit"
)

func testRateLimitQuickstart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const manifest = "testdata/ratelimit/quickstart.yaml"
	require.NoError(t, applyRateLimitManifests(ctx, manifest))
	defer func() {
		// Check ratelimit metric
		require.Less(t, float64(0), getMetricByName(t, aigNamespace, aigControllerSelector, "ratelimit_snapshot_version"))
		// Check the metrics from ExtProc
		require.Less(t, float64(0), getMetricByName(t, "default", "app.kubernetes.io/managed-by=ai-gateway", "process_total"))
		require.NoError(t, deleteRateLimitManifests(context.Background(), manifest))
		requireLLMRouteResourcesDeleted(t, "ratelimit-quickstart", "default")
	}()

	requireWaitForPodReady(t, egNamespace, ratelimitGatewaySelector)
	requireWaitForHTTPRouteAccepted(t, "default", "llmroute-ratelimit-quickstart")

	// Send requests to backend, the first will be OK, and reset will be 429.
	tc := newDefaultV1ChatCompletionCase("backend-ratelimit", "gpt-4o-mini").
		setTotalTokens(9)
	tc.setName("first-ok").runPortForwardRetry(t, ratelimitGatewaySelector, http.StatusOK)
	tc.setName("second-rejected").runPortForwardRetry(t, ratelimitGatewaySelector, http.StatusTooManyRequests)

	// Update the rate limit configuration, and check if the new configuration is applied.
	require.NoError(t, applyManifest(ctx, "testdata/ratelimit/quickstart_updated.yaml"))

	// Send requests to backend, the first will be OK, and reset will be 429.
	tc.setName("updated/first-ok").runPortForwardRetry(t, ratelimitGatewaySelector, http.StatusOK)
	tc.setName("updated/eventually-rejected").runPortForwardRetry(t, ratelimitGatewaySelector, http.StatusTooManyRequests)

	// The model name "gpt-3-turbo" is not rate limited, so it should be accepted.
	unlimitedModelTc := newDefaultV1ChatCompletionCase("backend-ratelimit", "gpt-3-turbo").
		setTotalTokens(1000)
	for i := 0; i < 3; i++ {
		unlimitedModelTc.setName(fmt.Sprintf("unlimited-model/%d", i)).
			run(t, ratelimitGatewaySelector, http.StatusOK)
	}
}

func testRateLimitBlockUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const manifest = "testdata/ratelimit/blockUnknown.yaml"
	require.NoError(t, applyRateLimitManifests(ctx, manifest))
	defer func() {
		require.NoError(t, deleteRateLimitManifests(context.Background(), manifest))
		requireLLMRouteResourcesDeleted(t, " ratelimit-blockunknown", "default")
	}()

	requireWaitForPodReady(t, egNamespace, ratelimitGatewaySelector)
	requireWaitForHTTPRouteAccepted(t, "default", "llmroute-ratelimit-blockunknown")

	// Send requests to backend, the first will be OK, and reset will be 429.
	tc := newDefaultV1ChatCompletionCase("backend-ratelimit", "gpt-4o-mini").
		setTotalTokens(9)
	tc.setName("first-ok").run(t, ratelimitGatewaySelector, http.StatusOK)
	tc.setName("second-rejected").run(t, ratelimitGatewaySelector, http.StatusTooManyRequests)

	// The model "gpt-3-turbo" should not be blocked by ratelimit.
	unlimitedModelTc := newDefaultV1ChatCompletionCase("backend-ratelimit", "gpt-3-turbo").
		setTotalTokens(1000)
	for i := 0; i < 3; i++ {
		unlimitedModelTc.setName(fmt.Sprintf("unlimited-model/%d", i)).
			run(t, ratelimitGatewaySelector, http.StatusOK)
	}

	// The model "unknown" should be blocked by ratelimit.
	newDefaultV1ChatCompletionCase("backend-ratelimit", "unknown").
		setTotalTokens(1).
		setName("unknown-model").
		run(t, ratelimitGatewaySelector, http.StatusTooManyRequests)
}

func testRateLimitModelNameDistinct(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const manifest = "testdata/ratelimit/modelNameDistinct.yaml"
	require.NoError(t, applyRateLimitManifests(ctx, manifest))
	defer func() {
		require.NoError(t, deleteRateLimitManifests(context.Background(), manifest))
		requireLLMRouteResourcesDeleted(t, "ratelimit-distinct-model-name", "default")
	}()

	requireWaitForPodReady(t, egNamespace, ratelimitGatewaySelector)
	requireWaitForHTTPRouteAccepted(t, "default", "llmroute-ratelimit-distinct-model-name")

	for _, modelName := range []string{"gpt-4o-mini", "gpt-4-turbo", "gpt-3-turbo"} {
		tc := newDefaultV1ChatCompletionCase("backend-ratelimit", modelName).
			setTotalTokens(9)
		// First request should be OK.
		tc.setName(fmt.Sprintf("model-%s-ok", modelName)).run(t, ratelimitGatewaySelector, http.StatusOK)
		// Subsequent requests with the same model name should be rate limited.
		tc.setName(fmt.Sprintf("model-%s-rejected", modelName)).run(t, ratelimitGatewaySelector, http.StatusTooManyRequests)
	}
}

func testRateLimitWithMultipleBackends(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const manifest = "testdata/ratelimit/multipleBackends.yaml"
	require.NoError(t, applyRateLimitManifests(ctx, manifest))
	defer func() {
		require.NoError(t, deleteRateLimitManifests(context.Background(), manifest))
		requireLLMRouteResourcesDeleted(t, "ratelimit-multiple-backends", "default")
	}()

	requireWaitForPodReady(t, egNamespace, ratelimitGatewaySelector)
	requireWaitForHTTPRouteAccepted(t, "default", "llmroute-ratelimit-multiple-backends")

	// Send requests to backend, the first will be OK, and reset will be 429.
	tc1 := newDefaultV1ChatCompletionCase("testupstream", "gpt-4o-mini").
		setTotalTokens(9)
	tc1.setName("testupstream-ok").run(t, ratelimitGatewaySelector, http.StatusOK)
	tc1.setName("testupstream-rejected").run(t, ratelimitGatewaySelector, http.StatusTooManyRequests)

	// The different backend should have different rate limit, so first request should be OK.
	tcCanary := newDefaultV1ChatCompletionCase("testupstream-canary", "gpt-4o-turbo").
		setTotalTokens(9)
	tcCanary.setName("testupstream-canary-ok").run(t, ratelimitGatewaySelector, http.StatusOK)
	// The second request should be also OK as it has different limit.
	tcCanary.setName("testupstream-canary-ok-2").run(t, ratelimitGatewaySelector, http.StatusOK)
	// The third request should be rate limited.
	tcCanary.setName("testupstream-canary-rejected").run(t, ratelimitGatewaySelector, http.StatusTooManyRequests)
}

func testRateLimitMultipleLimits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const manifest = "testdata/ratelimit/limits.yaml"
	const routeName = "ratelimit-limits"
	const ns = "default"

	// Create the resource, and check the logs of the controller.
	require.NoError(t, applyRateLimitManifests(ctx, manifest))
	defer func() {
		require.NoError(t, deleteRateLimitManifests(context.Background(), manifest))
		requireLLMRouteResourcesDeleted(t, routeName, ns)
	}()

	requireWaitForPodReady(t, egNamespace, ratelimitGatewaySelector)
	requireWaitForHTTPRouteAccepted(t, "default", "llmroute-ratelimit-limits")

	tc := newDefaultV1ChatCompletionCase("backend-ratelimit", "gpt-4o-mini")
	// First, check if the request count limit works, which is set to 10 per minute.
	tc.setTotalTokens(0).setName("request-count-limit").runPortForwardRetry(t, ratelimitGatewaySelector, http.StatusTooManyRequests)
	// Let's wait for the rate limit to be reset.
	time.Sleep(1 * time.Minute)

	// Each request should consume 200 token:
	tc.setTotalTokens(200).setName("token-ok-1").run(t, ratelimitGatewaySelector, http.StatusOK)
	tc.setTotalTokens(200).setName("token-ok-2").run(t, ratelimitGatewaySelector, http.StatusOK)
	tc.setTotalTokens(200).setName("token-limited").run(t, ratelimitGatewaySelector, http.StatusTooManyRequests)
}

func applyRateLimitManifests(ctx context.Context, manifest string) error {
	for _, m := range []string{"testdata/testRateLimitBase.yaml", manifest} {
		if err := applyManifest(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func deleteRateLimitManifests(ctx context.Context, manifest string) error {
	for _, m := range []string{"testdata/testRateLimitBase.yaml", manifest} {
		if err := deleteManifest(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func testRateLimitHeaderMatchExact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const manifest = "testdata/ratelimit/headerMatchExact.yaml"
	require.NoError(t, applyRateLimitManifests(ctx, manifest))
	defer func() {
		require.NoError(t, deleteRateLimitManifests(context.Background(), manifest))
		requireLLMRouteResourcesDeleted(t, "ratelimit-header", "default")
	}()

	requireWaitForPodReady(t, egNamespace, ratelimitGatewaySelector)
	requireWaitForHTTPRouteAccepted(t, "default", "llmroute-ratelimit-header")

	// If the user id is not "user1", the request should be always accepted.
	user2Case := newDefaultV1ChatCompletionCase("backend-ratelimit", "gpt-4o-mini").
		setRequestHeaders(map[string]string{"x-user-id": "user2"}).setTotalTokens(10)
	for i := 0; i < 3; i++ {
		user2Case.setName(fmt.Sprintf("user2/ok/%d", i)).run(t, ratelimitGatewaySelector, http.StatusOK)
	}

	// Send requests to backend, the first will be OK, and reset will be 429.
	user1Case := newDefaultV1ChatCompletionCase("backend-ratelimit", "gpt-4o-mini").
		setRequestHeaders(map[string]string{"x-user-id": "user1"}).setTotalTokens(10)
	// The first request should be accepted.
	user1Case.setName("user1/ok").run(t, ratelimitGatewaySelector, http.StatusOK)
	// The second request should be rate limited.
	user1Case.setName("user1/rejected").run(t, ratelimitGatewaySelector, http.StatusTooManyRequests)
}

// nolint: gosec
// jwtToken is a test token for testing jwt claim based rate limit.
const jwtToken = "eyJhbGciOiJSUzI1NiIsImtpZCI6IkRIRmJwb0lVcXJZOHQyenBBMnFYZkNtcjVWTzVaRXI0UnpIVV8tZW52dlEiLCJ0eXAiOiJKV1QifQ.eyJleHAiOjQ2ODU5ODk3MDAsImZvbyI6ImJhciIsImlhdCI6MTUzMjM4OTcwMCwiaXNzIjoidGVzdGluZ0BzZWN1cmUuaXN0aW8uaW8iLCJzdWIiOiJ0ZXN0aW5nQHNlY3VyZS5pc3Rpby5pbyJ9.CfNnxWP2tcnR9q0vxyxweaF3ovQYHYZl82hAUsn21bwQd9zP7c-LS9qd_vpdLG4Tn1A15NxfCjp5f7QNBUo-KC9PJqYpgGbaXhaGx7bEdFWjcwv3nZzvc7M__ZpaCERdwU7igUmJqYGBYQ51vr2njU9ZimyKkfDe3axcyiBZde7G6dabliUosJvvKOPcKIWPccCgefSj_GNfwIip3-SsFdlR7BtbVUcqR-yv-XOxJ3Uc1MI0tz3uMiiZcyPV7sNCU4KRnemRIMHVOfuvHsU60_GhGbiSFzgPTAa9WTltbnarTbxudb_YEOx12JiwYToeX0DCPb43W1tzIBxgm8NxUg"

func testRateLimitJWTClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const manifest = "testdata/ratelimit/jwtClaim.yaml"
	require.NoError(t, applyRateLimitManifests(ctx, manifest))
	defer func() {
		require.NoError(t, deleteRateLimitManifests(context.Background(), manifest))
		requireLLMRouteResourcesDeleted(t, "ratelimit-jwt-claim", "default")
	}()

	requireWaitForPodReady(t, egNamespace, ratelimitGatewaySelector)
	requireWaitForHTTPRouteAccepted(t, "default", "llmroute-ratelimit-jwt-claim")

	// Send requests to backend, the first will be OK, and reset will be 429.
	tc := newDefaultV1ChatCompletionCase("backend-ratelimit", "gpt-4o-mini").
		setRequestHeaders(map[string]string{"Authorization": fmt.Sprintf("Bearer %s", jwtToken)}).
		setTotalTokens(10)
	tc.setName("first-ok").run(t, ratelimitGatewaySelector, http.StatusOK)
	tc.setName("second-rejected").run(t, ratelimitGatewaySelector, http.StatusTooManyRequests)

	// gpt-3-turbo model is not rate limited, so it should be accepted for the same claim.
	newDefaultV1ChatCompletionCase("backend-ratelimit", "gpt-3-turbo").
		setRequestHeaders(map[string]string{"Authorization": fmt.Sprintf("Bearer %s", jwtToken)}).
		setTotalTokens(10).setName("unlimited-model").
		run(t, ratelimitGatewaySelector, http.StatusOK)
}
