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
	"strings"
	"testing"

	"github.com/openai/openai-go"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// Test_Examples_BackendQuotaRateLimit tests the backend-level quota rate limiting
// using the QuotaPolicy CRD. This verifies that the upstream rate limit filter
// enforces per-model token quotas on requests to AIServiceBackends.
func Test_Examples_BackendQuotaRateLimit(t *testing.T) {
	// Apply Redis manifest (shared with token rate limit tests).
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), "../../examples/token_ratelimit/redis.yaml"))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), "../../examples/token_ratelimit/redis.yaml")
	})

	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), "testdata/backend_quota_ratelimit.yaml"))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), "testdata/backend_quota_ratelimit.yaml")
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-quota-ratelimit"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	// Wait for the redis pod to be ready so that the rate limit service can connect.
	e2elib.RequireWaitForPodReady(t, "redis-system", "app=redis")
	// Wait for the AI Gateway rate limit service to be ready.
	e2elib.RequireWaitForPodReady(t, e2elib.EnvoyGatewayNamespace, "app=envoy-ai-gateway-ratelimit")

	const modelName = "quota-test-model"

	// makeRequest sends a chat completion request via the test upstream with the
	// specified total_tokens in the fake response. It asserts that the response
	// status matches expStatus.
	makeRequest := func(totalTokens int, expStatus int) {
		fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
		defer fwd.Kill()

		requestBody := fmt.Sprintf(`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"%s"}`, modelName)
		fakeResponseBody := fmt.Sprintf(
			`{"choices":[{"message":{"content":"This is a test.","role":"assistant"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":%d}}`,
			totalTokens,
		)

		req, err := http.NewRequest(http.MethodPut, fwd.Address()+"/v1/chat/completions", strings.NewReader(requestBody))
		require.NoError(t, err)
		req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(fakeResponseBody)))
		req.Header.Set(testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions")))
		req.Header.Set("Host", "openai.com")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		if resp.StatusCode == http.StatusOK {
			var oaiBody openai.ChatCompletion
			require.NoError(t, json.Unmarshal(body, &oaiBody))
			require.Equal(t, "This is a test.", oaiBody.Choices[0].Message.Content)
			require.Equal(t, int64(totalTokens), oaiBody.Usage.TotalTokens)
		}
		require.Equal(t, expStatus, resp.StatusCode, "unexpected status code, body: %s", string(body))
	}

	// Test per-model quota enforcement.
	// The QuotaPolicy sets a quota of 10 total tokens per hour for "quota-test-model".
	//
	// The first request uses 20 total_tokens which exceeds the quota limit of 10.
	// Since ApplyOnStreamDone is used, the rate limit check happens after the response
	// is received, so the first request succeeds but burns down the quota.
	t.Run("per-model quota", func(_ *testing.T) {
		// First request: 20 total tokens. This exceeds the limit of 10, but the
		// response has already been sent, so the request succeeds. The quota counter
		// is incremented to 20 (over the limit of 10).
		makeRequest(20, http.StatusOK)

		// Second request: the quota is already exhausted from the first request,
		// so this should be rate limited.
		makeRequest(0, http.StatusTooManyRequests)
	})
}
