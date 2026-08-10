// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// TestDynamicFallback exercises per-request dynamic backend ordering end to end through a real
// Envoy Gateway data plane: an annotated AIGatewayRoute with aliased backends, the trusted
// x-ai-eg-fallback-chain header resolved by the extproc, shared per-backend clusters selected
// per attempt via the matcher cluster specifier with refresh_cluster_on_retry, and candidate
// discovery on /v1/models.
//
// Requires the data plane to run Envoy >= 1.39; the manifest's EnvoyProxy overrides the proxy
// image accordingly. Backend selection is asserted with the testupstream's
// x-expected-testupstream-id contract: a backend whose TESTUPSTREAM_ID does not match replies
// with a retriable 400, so "only backend X may serve" makes every other backend fail.
func TestDynamicFallback(t *testing.T) {
	const manifest = "testdata/dynamic_fallback.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=dynamic-fallback"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	requestBody := `{"messages":[{"role":"user","content":"Say this is a test"}],"model":"model-a"}`
	doRequest := func(t *testing.T, headers map[string]string) (int, string, string) {
		fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
		defer fwd.Kill()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fwd.Address()+"/v1/chat/completions", strings.NewReader(requestBody))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte(`{"choices":[{"message":{"content":"test"}}]}`)))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, "", ""
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, resp.Header.Get("testupstream-id"), string(body)
	}

	t.Run("chain steers the first attempt", func(t *testing.T) {
		// Only backend-c (alias gamma) may serve; the chain puts gamma first, so the request
		// succeeds on attempt 1. Without the chain, the default order (alpha, beta) would burn
		// both attempts on 400s — see the scrubbed-forged-headers case below.
		require.Eventually(t, func() bool {
			status, id, body := doRequest(t, map[string]string{
				internalapi.FallbackChainHeader:           "gamma,alpha",
				testupstreamlib.ExpectedTestUpstreamIDKey: "backend-c",
			})
			t.Logf("status=%d id=%s body=%s", status, id, body)
			return status == http.StatusOK && id == "backend-c"
		}, 2*time.Minute, 2*time.Second)
	})

	t.Run("chain walks to the next entry on failure", func(t *testing.T) {
		// Only backend-a (alias alpha) may serve; gamma is first in the chain and fails with a
		// retriable 400, so the refreshed second attempt must land on alpha.
		require.Eventually(t, func() bool {
			status, id, _ := doRequest(t, map[string]string{
				internalapi.FallbackChainHeader:           "gamma,alpha",
				testupstreamlib.ExpectedTestUpstreamIDKey: "backend-a",
			})
			return status == http.StatusOK && id == "backend-a"
		}, 2*time.Minute, 2*time.Second)
	})

	t.Run("default order without a chain", func(t *testing.T) {
		// No chain: the declared priorities apply, so backend-a (priority 0) serves attempt 1.
		require.Eventually(t, func() bool {
			status, id, _ := doRequest(t, map[string]string{
				testupstreamlib.ExpectedTestUpstreamIDKey: "backend-a",
			})
			return status == http.StatusOK && id == "backend-a"
		}, 2*time.Minute, 2*time.Second)
	})

	t.Run("forged matcher inputs are scrubbed", func(t *testing.T) {
		// The client forges the slot header and attempt count to steer attempt 1 to gamma
		// WITHOUT the trusted chain header. The extproc must scrub both, so the request follows
		// the default order (alpha, beta) — and with only backend-c allowed to serve, both
		// attempts 400 and the request must NOT succeed. numRetries is 1, so the defaults can
		// never reach backend-c.
		require.Eventually(t, func() bool {
			status, id, _ := doRequest(t, map[string]string{
				internalapi.EnvoyAttemptCountHeader:               "0",
				internalapi.DynamicFallbackSlotHeaderPrefix + "0": "gamma",
				testupstreamlib.ExpectedTestUpstreamIDKey:         "backend-c",
			})
			// A 200 from backend-c here would mean the forged headers steered the request.
			return status == http.StatusBadRequest && id != "backend-c"
		}, 2*time.Minute, 2*time.Second)
	})

	t.Run("models listing publishes fallback candidates", func(t *testing.T) {
		require.Eventually(t, func() bool {
			fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
			defer fwd.Kill()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fwd.Address()+"/v1/models", nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return false
			}
			var models openai.ModelList
			if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
				return false
			}
			for _, m := range models.Data {
				if m.ID == "model-a" {
					return len(m.FallbackCandidates) == 3 &&
						m.FallbackCandidates[0] == "alpha" &&
						m.FallbackCandidates[1] == "beta" &&
						m.FallbackCandidates[2] == "gamma"
				}
			}
			return false
		}, 2*time.Minute, 2*time.Second)
	})
}
