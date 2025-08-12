// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

// TestOTELTracingWithConsoleExporter verifies that OTEL environment variables
// can be configured via Helm and are properly injected into extProc containers.
func TestOTELTracingWithConsoleExporter(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	// Get the source directory relative to this test file.
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get source file path")
	sourceDir := filepath.Dir(filename)

	helmChartPath := filepath.Join(sourceDir, "..", "..", "manifests", "charts", "ai-gateway-helm")
	manifest := filepath.Join(sourceDir, "testdata", "otel_tracing_console.yaml")

	// Upgrade existing AI Gateway installation with OTEL_TRACES_EXPORTER=console.
	t.Log("Upgrading AI Gateway with OTEL_TRACES_EXPORTER=console")

	// Upgrade the existing "ai-eg" release with new env vars.
	helm := exec.CommandContext(ctx, "go", "tool", "helm", "upgrade", "ai-eg",
		helmChartPath,
		"--set", "controller.metricsRequestHeaderLabels=x-user-id:user_id", // Keep existing setting.
		"--set", "extProc.extraEnvVars[0].name=OTEL_TRACES_EXPORTER",
		"--set", "extProc.extraEnvVars[0].value=console",
		"--set", "extProc.extraEnvVars[1].name=OTEL_SERVICE_NAME",
		"--set", "extProc.extraEnvVars[1].value=ai-gateway-e2e-test",
		"-n", "envoy-ai-gateway-system")

	helmOutput := &bytes.Buffer{}
	helm.Stdout = helmOutput
	helm.Stderr = helmOutput

	err := helm.Run()
	if err != nil {
		t.Logf("Helm output: %s", helmOutput.String())
	}
	require.NoError(t, err, "Failed to upgrade AI Gateway with OTEL env vars")

	// Setup cleanup to restore original configuration after test.
	t.Cleanup(func() {
		t.Log("Restoring original AI Gateway configuration")
		restoreCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// Re-install AI Gateway with default settings.
		restoreHelm := exec.CommandContext(restoreCtx, "go", "tool", "helm", "upgrade", "ai-eg",
			helmChartPath,
			"-n", "envoy-ai-gateway-system")
		_ = restoreHelm.Run()

		// Clean up the test manifest resources including namespace.
		_ = e2elib.KubectlDeleteManifest(restoreCtx, manifest)

		// Delete the test namespace to clean up completely.
		deleteNs := exec.CommandContext(restoreCtx, "kubectl", "delete", "namespace",
			"otel-test-namespace", "--ignore-not-found=true")
		_ = deleteNs.Run()
	})

	// Restart controller to pick up new configuration.
	restartCmd := exec.CommandContext(ctx, "kubectl", "rollout", "restart",
		"deployment/ai-gateway-controller", "-n", "envoy-ai-gateway-system")
	err = restartCmd.Run()
	require.NoError(t, err)

	// Wait for deployment to be ready.
	waitCmd := exec.CommandContext(ctx, "kubectl", "wait", "--timeout=2m",
		"-n", "envoy-ai-gateway-system", "deployment/ai-gateway-controller", "--for=condition=available")
	err = waitCmd.Run()
	require.NoError(t, err)

	// Apply the test manifest which will trigger pod creation.
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))

	// Wait for gateway pod to be created.
	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-otel-test"

	// Wait for the pod to be ready.
	waitPodCmd := exec.CommandContext(ctx, "kubectl", "wait", "--timeout=60s", // #nosec G204
		"-n", e2elib.EnvoyGatewayNamespace,
		"-l", egSelector,
		"--for=condition=ready", "pod")
	_ = waitPodCmd.Run() // Ignore error as pods might not be fully ready.

	// Get the pod with extProc container and verify env vars.
	t.Log("Checking extProc container for OTEL environment variables")

	// Get pod name from envoy-gateway-system namespace (where pods are created).
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		getPodsCmd := exec.CommandContext(ctx, "kubectl", "get", "pods", // #nosec G204
			"-n", e2elib.EnvoyGatewayNamespace,
			"-l", egSelector,
			"-o", "jsonpath={.items[0].metadata.name}")

		podNameBytes, err := getPodsCmd.Output()
		require.NoError(c, err)
		podName := string(podNameBytes)
		require.NotEmpty(c, podName)

		// Get the pod description to check env vars.
		describeCmd := exec.CommandContext(ctx, "kubectl", "get", "pod", podName,
			"-n", e2elib.EnvoyGatewayNamespace,
			"-o", "jsonpath={.spec.containers[?(@.name=='ai-gateway-extproc')].env}")

		describeOutput := &bytes.Buffer{}
		describeCmd.Stdout = describeOutput
		describeCmd.Stderr = describeOutput

		err = describeCmd.Run()
		require.NoError(c, err)

		envVars := describeOutput.String()

		// Verify that our OTEL env vars are present in the pod spec.
		require.Contains(c, envVars, `"name":"OTEL_TRACES_EXPORTER","value":"console"`,
			"Expected OTEL_TRACES_EXPORTER=console in extProc container spec")
		require.Contains(c, envVars, `"name":"OTEL_SERVICE_NAME","value":"ai-gateway-e2e-test"`,
			"Expected OTEL_SERVICE_NAME=ai-gateway-e2e-test in extProc container spec")
	}, 1*time.Minute, 1*time.Second)

	t.Log("OTEL environment variables successfully verified in extProc container")
}
