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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

func TestMain(m *testing.M) {
	e2elib.TestMain(m, e2elib.TestMainConfig{
		AIGatewayHelmFlags: nil,
		InferenceExtension: false,
		RegistryVersion:    "0.3.0",
	})
}

// TestUpgrade tests the upgrade process from v0.3.0 to the local version.
// It continuously makes requests during the upgrade to ensure no downtime.
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

	// Channel to track request success/failure.
	resultChan := make(chan bool, 1000)
	var wg sync.WaitGroup

	// Start making continuous requests.
	t.Log("Starting continuous requests...")
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
				success := makeTestRequest(testURL)
				resultChan <- success
			}
		}
	}()

	// Wait for some initial requests to establish baseline.
	time.Sleep(5 * time.Second)
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

	// Require at least 95% success rate during upgrade.
	require.Positive(t, totalRequests, "Should have made some requests")
	successRate := float64(successfulRequests) / float64(totalRequests)
	require.GreaterOrEqual(t, successRate, 0.95, "Success rate should be at least 95%% during upgrade")
}

// makeTestRequest makes a single test request and returns whether it was successful.
func makeTestRequest(url string) bool {
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return false
	}

	// Set required headers for the test upstream.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-ai-eg-model", "some-cool-model")
	req.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
		base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions")))
	req.Header.Set(testupstreamlib.ExpectedTestUpstreamIDKey, "primary")
	req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, `{"test":"response"}`)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	_, err = io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	return resp.StatusCode == http.StatusOK
}
