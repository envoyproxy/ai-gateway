// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

// Test_NestedTeamBudgets validates hierarchical rate limit budgets using
// Dex-issued JWT tokens, the Lua JWT group fanout filter, the file-based
// storage backend, and the in-process rate limit service.
//
// Budget hierarchy (all per 1 hour, 15 tokens per request from mockllm):
//
//	Default model budget:   50 tokens
//	Engineering department: 30 tokens
//	ml-team (sub-group):    10 tokens
//
// Alice (engineering + ml-team) uses the tightest bucket (10 tokens).
// A single 15-token request exceeds the limit → 429.
func Test_NestedTeamBudgets(t *testing.T) {
	// Override the rate limit service address to point to the controller's in-process
	// gRPC service (port 8081) instead of the external envoy-ai-gateway-ratelimit container.
	require.NoError(t, e2elib.InstallOrUpgradeAIGateway(t.Context(), e2elib.AIGatewayHelmOption{
		AdditionalArgs: []string{
			"--set", "controller.storage.backend=file",
			"--set", "controller.storage.fileDir=/tmp/ratelimit",
			"--set", "controller.quotaRateLimitServiceAddr=ai-gateway-controller.envoy-ai-gateway-system.svc.cluster.local:8081",
		},
	}))
	t.Cleanup(func() {
		_ = e2elib.InstallOrUpgradeAIGateway(context.Background(), e2elib.AIGatewayHelmOption{
			AdditionalArgs: []string{
				"--set", "controller.storage.backend=file",
				"--set", "controller.storage.fileDir=/tmp/ratelimit",
				"--set", "controller.quotaRateLimitServiceAddr=envoy-ai-gateway-ratelimit.envoy-gateway-system",
			},
		})
	})

	// Deploy Dex for JWT token issuance.
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), "testdata/dex.yaml"))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), "testdata/dex.yaml")
	})
	e2elib.RequireWaitForPodReady(t, "dex", "app=dex")

	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), "testdata/nested_budget.yaml"))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), "testdata/nested_budget.yaml")
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-nested-budget"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)
	e2elib.RequireWaitForPodReady(t, "default", "app=mockllm")

	// Acquire a JWT token for alice (engineering + ml-team groups).
	aliceToken := acquireDexToken(t, "alice@example.com", "password")

	// Poll until the QuotaPolicy is reconciled and propagated to Envoy.
	// Alice is in engineering + ml-team. ml-team budget (10 tokens) is tightest.
	// mockllm returns 15 tokens per request → first request after propagation is 429.
	require.Eventually(t, func() bool {
		fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
		defer fwd.Kill()

		body := `{"messages":[{"role":"user","content":"hello"}],"model":"mock-model"}`
		req, err := http.NewRequest(http.MethodPost, fwd.Address()+"/v1/chat/completions", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Host", "openai.com")
		req.Header.Set("x-ai-eg-model", "mock-model")
		req.Header.Set("Authorization", "Bearer "+aliceToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()

		return resp.StatusCode == http.StatusTooManyRequests
	}, 30*time.Second, 500*time.Millisecond,
		"QuotaPolicy config did not propagate: expected 429 on first request")
}

// acquireDexToken obtains a JWT from Dex's password grant endpoint.
func acquireDexToken(t *testing.T, username, password string) string {
	t.Helper()

	// Port-forward to the Dex service.
	fwd := e2elib.RequireNewHTTPPortForwarder(t, "dex", "app=dex", 5556)
	defer fwd.Kill()

	tokenURL := fmt.Sprintf("%s/dex/token", fwd.Address())
	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {"ai-gateway"},
		"client_secret": {"ai-gateway-secret"},
		"username":      {username},
		"password":      {password},
		"scope":         {"openid profile email groups"},
	}

	resp, err := http.PostForm(tokenURL, form) //nolint:gosec // test code: URL is port-forwarded k8s service, not user input
	require.NoError(t, err, "failed to acquire token from Dex")
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"dex token request failed: status %d, body: %s", resp.StatusCode, string(body))

	// Extract id_token from the JSON response.
	// We use a minimal approach to avoid importing encoding/json (blocked by depguard).
	bodyStr := string(body)
	const key = `"id_token":"`
	idx := strings.Index(bodyStr, key)
	require.NotEqual(t, -1, idx, "id_token not found in Dex response: %s", bodyStr)
	start := idx + len(key)
	end := strings.Index(bodyStr[start:], `"`)
	require.NotEqual(t, -1, end, "malformed id_token in Dex response: %s", bodyStr)
	return bodyStr[start : start+end]
}
