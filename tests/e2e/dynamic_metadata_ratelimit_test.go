// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// Test_DynamicMetadataRateLimit verifies that a per-request limit carried in
// io.envoy.ai_gateway dynamic metadata (emitted by ext_proc from GatewayConfig
// globalRateLimits) gates traffic instead of the static BackendTrafficPolicy limit.
// The source metadata ("2/HOUR") is set to 2 requests/hour per x-tenant-id.
func Test_DynamicMetadataRateLimit(t *testing.T) {
	// limit.fromMetadata (envoyproxy/gateway#9216) only exists on Envoy Gateway main; older releases
	// prune it, so nothing gets rate limited.
	if !e2elib.EnvoyGatewaySupportsLimitFromMetadata() {
		t.Skipf("needs Envoy Gateway %s, have %s", e2elib.EnvoyGatewayLatestVersion, e2elib.EnvoyGatewayVersion())
	}

	// Apply Redis manifest (shared with the other rate limit tests). The Envoy
	// Gateway global rate limit service (envoy-ratelimit) is backed by Redis.
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), "../../examples/token_ratelimit/redis.yaml"))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), "../../examples/token_ratelimit/redis.yaml")
	})

	const manifest = "testdata/dynamic_metadata_ratelimit.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-dynamic-ratelimit"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	// Wait for the redis pod to be ready so that the rate limit can be performed
	// correctly. Until the redis pod is ready, envoy-ratelimit will be in
	// CrashLoopBackOff, so restart it to get a clean state up faster.
	require.NoError(t, e2elib.KubectlRestartDeployment(t.Context(), e2elib.EnvoyGatewayNamespace, "envoy-ratelimit"))
	e2elib.RequireWaitForPodReady(t, e2elib.EnvoyGatewayNamespace, "app.kubernetes.io/component=ratelimit")
	e2elib.RequireWaitForPodReady(t, "redis-system", "app=redis")

	// The ext_authz auth server supplies the per-tenant limit; it must be up or requests fail closed.
	e2elib.RequireWaitForPodReady(t, "default", "app=ext-auth-server-dynamic-ratelimit")

	const modelName = "dynamic-ratelimit-model"
	makeRequest := func(t *testing.T, tenantID string, expStatus int) {
		fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
		defer fwd.Kill()

		requestBody := fmt.Sprintf(`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"%s"}`, modelName)
		const fakeResponseBody = `{"choices":[{"message":{"content":"This is a test.","role":"assistant"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

		req, err := http.NewRequest(http.MethodPut, fwd.Address()+"/v1/chat/completions", strings.NewReader(requestBody))
		require.NoError(t, err)
		req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(fakeResponseBody)))
		req.Header.Set(testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions")))
		req.Header.Set("x-tenant-id", tenantID)
		req.Header.Set("Host", "openai.com")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, expStatus, resp.StatusCode, "unexpected status code, body: %s", string(body))
	}

	// Fresh x-tenant-id per block: rate-limit budgets persist in Redis across the run.
	baseID := int(time.Now().UnixNano())

	t.Run("dynamic limit gates traffic at 2/HOUR", func(t *testing.T) {
		tenantID := strconv.Itoa(baseID)
		makeRequest(t, tenantID, http.StatusOK)
		makeRequest(t, tenantID, http.StatusOK)
		makeRequest(t, tenantID, http.StatusTooManyRequests)
	})

	t.Run("a different tenant has an independent budget", func(t *testing.T) {
		tenantID := strconv.Itoa(baseID + 1)
		makeRequest(t, tenantID, http.StatusOK)
		makeRequest(t, tenantID, http.StatusOK)
		makeRequest(t, tenantID, http.StatusTooManyRequests)
	})
}
