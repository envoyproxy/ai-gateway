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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

func TestMain(m *testing.M) {
	e2elib.TestMain(m, e2elib.TestMainConfig{
		AIGatewayHelmFlags: nil,
		InferenceExtension: false,
		RegistryVersion:    "0.3.0",
	})
}

// TestUpgrade tests the upgrade process from v0.3.0 to the local version.
// It waits for the first successful request, then continuously makes requests during the upgrade to ensure no downtime.
func TestUpgrade(t *testing.T) {
	const manifest = "../e2e/testdata/testupstream.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=translation-testupstream"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	// Set up port forwarding to the gateway.
	fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
	defer fwd.Kill()

	// Test URL for making requests.
	testURL := fmt.Sprintf("%s/v1/chat/completions", fwd.Address())

	// Setup context for the test duration.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	// Wait for the first successful request before starting continuous testing.
	t.Log("Waiting for first successful request...")
	waitForFirstSuccess(t, testURL)
	t.Log("First successful request received, starting continuous requests...")

	// Channel to track request success/failure.
	resultChan := make(chan bool, 1000)
	var wg sync.WaitGroup

	// Start making continuous requests.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond) // 10 RPS.
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				success := makeTestRequest(t, testURL)
				resultChan <- success
			}
		}
	}()

	// Wait a brief moment to establish baseline after successful startup.
	time.Sleep(2 * time.Second)
	t.Log("Baseline established, starting upgrade...")

	// Perform the upgrade.
	upgradeCtx, upgradeCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer upgradeCancel()

	err := e2elib.UpgradeAIGatewayToLocal(upgradeCtx, nil)
	require.NoError(t, err, "Upgrade should succeed")

	t.Log("Upgrade completed, continuing requests for 2 more minutes...")

	// Continue requests for 2 more minutes after upgrade.
	time.Sleep(2 * time.Minute)

	// Stop request generation.
	cancel()
	wg.Wait()
	close(resultChan)

	// Analyze results.
	totalRequests := 0
	successfulRequests := 0
	for success := range resultChan {
		totalRequests++
		if success {
			successfulRequests++
		}
	}

	t.Logf("Total requests: %d, Successful: %d, Success rate: %.2f%%",
		totalRequests, successfulRequests, float64(successfulRequests)/float64(totalRequests)*100)

	// Require 100% success rate during upgrade since we wait for initial success.
	require.Positive(t, totalRequests, "Should have made some requests")
	require.Equal(t, totalRequests, successfulRequests, "All requests should succeed after initial success - no downtime during upgrade")
}

// waitForFirstSuccess waits for the first successful request before starting continuous testing.
func waitForFirstSuccess(t *testing.T, url string) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("Timeout waiting for first successful request")
		case <-ticker.C:
			if makeTestRequest(t, url) {
				return
			}
			t.Log("Waiting for first successful request...")
		}
	}
}

// makeTestRequest makes a single test request and returns whether it was successful.
func makeTestRequest(t *testing.T, url string) bool {
	req, err := http.NewRequest("POST", url, strings.NewReader(
		`{"model":"some-cool-model","messages":[{"role":"user","content":"Hello!"}]}`,
	))
	if err != nil {
		t.Logf("Failed to create request: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("Request error: %v", err)
		return false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Logf("Failed to read response body: %v", err)
		return false
	}
	t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))
	return resp.StatusCode == http.StatusOK
}
