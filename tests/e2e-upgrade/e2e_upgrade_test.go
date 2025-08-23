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
	"os"
	"os/exec"
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

// upgradeAIGatewayToLocal upgrades the AI Gateway from registry version to local charts.
// This is used for upgrade testing to simulate upgrading from a released version to a new version.
func upgradeAIGatewayToLocal(ctx context.Context, aiGatewayHelmFlags []string) (err error) {
	fmt.Printf("\u001b[32m=== INIT LOG: Upgrading AI Gateway to local charts\u001B[0m\n")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		fmt.Printf("\u001b[32m=== INIT LOG: \tdone (took %.2fs in total)\n\u001B[0m", elapsed.Seconds())
	}()

	fmt.Printf("\u001b[32m=== INIT LOG: \tHelm Upgrade CRDs to local\u001B[0m\n")
	helmCRD := exec.CommandContext(ctx, "go", "tool", "helm", "upgrade", "-i", "ai-eg-crd",
		"../../manifests/charts/ai-gateway-crds-helm",
		"-n", "envoy-ai-gateway-system")
	helmCRD.Stdout = os.Stdout
	helmCRD.Stderr = os.Stderr
	if err = helmCRD.Run(); err != nil {
		return
	}

	fmt.Printf("\u001b[32m=== INIT LOG: \tHelm Upgrade AI Gateway to local\u001B[0m\n")
	args := []string{
		"tool", "helm", "upgrade", "-i", "ai-eg",
		"../../manifests/charts/ai-gateway-helm",
		"-n", "envoy-ai-gateway-system",
	}
	args = append(args, aiGatewayHelmFlags...)

	helm := exec.CommandContext(ctx, "go", args...)
	helm.Stdout = os.Stdout
	helm.Stderr = os.Stderr
	if err = helm.Run(); err != nil {
		return
	}

	// Restart the controller to pick up the new changes in the AI Gateway.
	fmt.Printf("\u001b[32m=== INIT LOG: \tRestart AI Gateway controller\u001B[0m\n")
	if err = kubectlRestartDeployment(ctx, "envoy-ai-gateway-system", "ai-gateway-controller"); err != nil {
		return
	}
	return kubectlWaitForDeploymentReady("envoy-ai-gateway-system", "ai-gateway-controller")
}

// kubectlRestartDeployment restarts a deployment in the given namespace.
func kubectlRestartDeployment(ctx context.Context, namespace, deployment string) error {
	cmd := e2elib.Kubectl(ctx, "rollout", "restart", "deployment/"+deployment, "-n", namespace)
	return cmd.Run()
}

// kubectlWaitForDeploymentReady waits for a deployment to be ready.
func kubectlWaitForDeploymentReady(namespace, deployment string) (err error) {
	cmd := e2elib.Kubectl(context.Background(), "wait", "--timeout=2m", "-n", namespace,
		"deployment/"+deployment, "--for=create")
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for deployment %s in namespace %s: %w", deployment, namespace, err)
	}

	cmd = e2elib.Kubectl(context.Background(), "wait", "--timeout=2m", "-n", namespace,
		"deployment/"+deployment, "--for=condition=Available")
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for deployment %s in namespace %s: %w", deployment, namespace, err)
	}
	return
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

	err := upgradeAIGatewayToLocal(upgradeCtx, nil)
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
