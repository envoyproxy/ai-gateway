// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestInferencePoolIntegration tests the InferencePool integration with AI Gateway.
func TestInferencePoolIntegration(t *testing.T) {
	// Apply the base test manifest.
	const baseManifest = "../../examples/inference-pool/base.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), baseManifest))

	// Test inferencePool with AIGatewayRoute.
	const aiGWRouteManifest = "../../examples/inference-pool/aigwroute.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), aiGWRouteManifest))

	egSelector := "gateway.envoyproxy.io/owning-gateway-name=inference-pool-with-aigwroute"
	requireWaitForGatewayPodReady(t, egSelector)

	// Test connectivity to inferencePool + inference pods with valid metrics.
	t.Run("endpointpicker_with_aigwroute_valid_pod_metrics", func(t *testing.T) {
		testInferenceGatewayConnectivity(t, egSelector, "meta-llama/Llama-3.1-8B-Instruct")
	})

	// Test connectivity to inferencePool + inference pods with invalid metrics.
	t.Run("endpointpicker_with_aigwroute_invalid_pod_metrics", func(t *testing.T) {
		testInferenceGatewayConnectivity(t, egSelector, "mistral:latest")
	})

	// Test connectivity to aiservicebackend within the same aigatewayroute with inferencePool.
	t.Run("endpointpicker_with_aigwroute_aiservicebackend", func(t *testing.T) {
		testInferenceGatewayConnectivity(t, egSelector, "some-cool-self-hosted-model")
	})

	t.Cleanup(func() {
		_ = kubectlDeleteManifest(context.Background(), aiGWRouteManifest)
	})

	// Test inferencePool with HTTPRoute.
	const httpRouteManifest = "../../examples/inference-pool/httproute.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), httpRouteManifest))

	egSelector = "gateway.envoyproxy.io/owning-gateway-name=inference-pool-with-httproute"
	requireWaitForPodReady(t, egSelector)

	// Test connectivity to inferencePool + inference pods with valid metrics.
	t.Run("endpointpicker_with_httproute_valid_pod_metrics", func(t *testing.T) {
		testInferenceGatewayConnectivity(t, egSelector, "meta-llama/Llama-3.1-8B-Instruct")
	})

	t.Cleanup(func() {
		_ = kubectlDeleteManifest(context.Background(), httpRouteManifest)
	})
}

// testInferenceGatewayConnectivity tests that the Gateway is accessible and returns a 200 status code
// for a valid request to the InferencePool backend.
func testInferenceGatewayConnectivity(t *testing.T, egSelector, model string) {
	require.Eventually(t, func() bool {
		fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultServicePort)
		defer fwd.kill()

		// Create a request to the InferencePool backend with the correct model header.
		requestBody := fmt.Sprintf(`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"%s"}`, model)
		t.Logf("Request body: %s", requestBody)
		req, err := http.NewRequest(http.MethodPost, fwd.address()+"/v1/chat/completions", strings.NewReader(requestBody))
		if err != nil {
			t.Logf("failed to create request: %v", err)
			return false
		}

		// Set required headers for InferencePool routing.
		req.Header.Set("Content-Type", "application/json")

		// Set timeout context.
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		req = req.WithContext(ctx)

		// Make the request.
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("request failed: %v", err)
			return false
		}
		defer func() { _ = resp.Body.Close() }()

		// Read response body for debugging.
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Logf("failed to read response body: %v", err)
			return false
		}

		t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))

		// Check for successful response (200 OK).
		if resp.StatusCode != http.StatusOK {
			t.Logf("unexpected status code: %d (expected 200), body: %s", resp.StatusCode, string(body))
			return false
		}

		// Verify we got a valid response body (should contain some content).
		if len(body) == 0 {
			t.Logf("empty response body")
			return false
		}

		t.Logf("Gateway connectivity test passed: status=%d", resp.StatusCode)
		return true
	}, 2*time.Minute, 5*time.Second, "Gateway should be accessible and return 200 status code")
}
