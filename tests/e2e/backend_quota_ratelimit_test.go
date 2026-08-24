// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// Test_Examples_BackendQuotaRateLimit tests the backend-level quota rate limiting
// using the QuotaPolicy CRD. This verifies that the upstream rate limit filter
// enforces per-model token quotas on requests to AIServiceBackends.
func Test_Examples_BackendQuotaRateLimit(t *testing.T) {
	storage := applyRateLimitStorage(t)
	applyQuotaRateLimitManifest(t, storage)

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-quota-ratelimit"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	// Wait for the AI Gateway rate limit service to be ready.
	e2elib.RequireWaitForPodReady(t, e2elib.EnvoyGatewayNamespace, "app=envoy-ai-gateway-ratelimit")

	// Flush any existing quota keys in the selected Redis-protocol backend.
	flushQuotaKeys(t, storage)

	// makeRequest sends a chat completion request via the test upstream with the
	// specified total_tokens in the fake response and asserts the expected status code.
	makeRequest := func(modelName string, totalTokens int, expectedStatus int, headers ...http.Header) {
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
		for _, h := range headers {
			for k, vals := range h {
				for _, v := range vals {
					req.Header.Set(k, v)
				}
			}
		}

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		require.Equal(t, expectedStatus, resp.StatusCode, "unexpected status code, body: %s", string(body))
	}

	// Test per-model quota enforcement by verifying the quota counter in Redis.
	// The QuotaPolicy sets a quota of 10 total tokens per hour for "quota-test-model".
	t.Run("per-model quota", func(t *testing.T) {
		makeRequest("quota-test-model", 20, http.StatusOK)
		requireQuotaUsage(t, storage, "quota-test-model", 21)
		makeRequest("quota-test-model", 5, http.StatusTooManyRequests)
		requireQuotaUsage(t, storage, "quota-test-model", 22)
	})
}

// rateLimitStorageExec runs the selected storage CLI command and returns its output.
func rateLimitStorageExec(t *testing.T, storage e2elib.RateLimitStorage, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{
		"exec", "-n", storage.Namespace,
		"deploy/" + storage.Deployment, "--",
		storage.CLI,
	}, args...)
	cmd := exec.CommandContext(t.Context(), "kubectl", cmdArgs...)
	out, err := cmd.Output()
	require.NoError(t, err, "%s %v failed", storage.CLI, args)
	return strings.TrimSpace(string(out))
}

// flushQuotaKeys deletes all quota-related keys from the selected storage backend.
func flushQuotaKeys(t *testing.T, storage e2elib.RateLimitStorage) {
	t.Helper()
	keys := rateLimitStorageExec(t, storage, "KEYS", "ai-gateway-quota_*")
	if keys == "" {
		return
	}
	for _, key := range strings.Split(keys, "\n") {
		key = strings.TrimSpace(key)
		if key != "" {
			rateLimitStorageExec(t, storage, "DEL", key)
		}
	}
}

// getQuotaUsage retrieves the current quota counter value from the selected backend for a model.
// The key pattern is: ai-gateway-quota_backend_name_{backend}_model_name_override_{model}_{timestamp}
func getQuotaUsage(t *testing.T, storage e2elib.RateLimitStorage, modelName string) (int, bool) {
	t.Helper()
	pattern := fmt.Sprintf("ai-gateway-quota_backend_name_*_model_name_override_%s_*", modelName)
	keys := rateLimitStorageExec(t, storage, "KEYS", pattern)
	if keys == "" {
		return 0, false
	}
	// Use the first matching key (there should be exactly one per model per time window).
	key := strings.Split(keys, "\n")[0]
	key = strings.TrimSpace(key)
	val := rateLimitStorageExec(t, storage, "GET", key)
	if val == "" {
		return 0, false
	}
	n, err := strconv.Atoi(val)
	require.NoError(t, err, "failed to parse quota counter value: %q", val)
	return n, true
}

// requireQuotaUsage polls the selected backend until the quota counter for a model reaches
// the expected value. The stream-done rate limit entry updates Redis asynchronously
// after the response, so polling is necessary.
func requireQuotaUsage(t *testing.T, storage e2elib.RateLimitStorage, modelName string, expected int) {
	t.Helper()
	require.Eventually(t, func() bool {
		usage, ok := getQuotaUsage(t, storage, modelName)
		return ok && usage == expected
	}, 30*time.Second, 500*time.Millisecond,
		"quota counter for model %q did not reach expected value %d", modelName, expected)
}
