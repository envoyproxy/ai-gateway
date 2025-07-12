// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestInferencePoolIntegration tests the InferencePool integration with AI Gateway.
func TestInferencePoolIntegration(t *testing.T) {
	// Apply the simplified test manifest
	const manifest = "../../examples/inference-pool/base.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), manifest))

	// Clean up after test
	t.Cleanup(func() {
		_ = kubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=inference-pool"
	requireWaitForGatewayPodReady(t, egSelector)

	// Test basic Gateway connectivity
	t.Run("basic_connectivity", func(t *testing.T) {
		testBasicGatewayConnectivity(t, egSelector)
	})
}

// testBasicGatewayConnectivity tests that the Gateway is accessible and returns a 200 status code
// for a valid request to the InferencePool backend.
func testBasicGatewayConnectivity(t *testing.T, egSelector string) {
	require.Eventually(t, func() bool {
		fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultServicePort)
		defer fwd.kill()

		// Create a request to the InferencePool backend with the correct model header
		requestBody := `{"messages":[{"role":"user","content":"Say this is a test"}],"model":"meta-llama/Llama-3.1-8B-Instruct"}`
		req, err := http.NewRequest(http.MethodPost, fwd.address()+"/v1/chat/completions", strings.NewReader(requestBody))
		if err != nil {
			t.Logf("failed to create request: %v", err)
			return false
		}

		// Set required headers for InferencePool routing
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-ai-eg-model", "meta-llama/Llama-3.1-8B-Instruct")

		// Set timeout context
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		req = req.WithContext(ctx)

		// Make the request
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("request failed: %v", err)
			return false
		}
		defer func() { _ = resp.Body.Close() }()

		// Read response body for debugging
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Logf("failed to read response body: %v", err)
			return false
		}

		t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))

		// Check for successful response (200 OK)
		if resp.StatusCode != http.StatusOK {
			t.Logf("unexpected status code: %d (expected 200), body: %s", resp.StatusCode, string(body))
			return false
		}

		// Verify we got a valid response body (should contain some content)
		if len(body) == 0 {
			t.Logf("empty response body")
			return false
		}

		t.Logf("Gateway connectivity test passed: status=%d", resp.StatusCode)
		return true
	}, 2*time.Minute, 5*time.Second, "Gateway should be accessible and return 200 status code")
}
