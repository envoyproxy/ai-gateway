// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

// TestOTELTracingWithConsoleExporter verifies that OTEL environment variables
// can be configured via Helm and are properly injected into extProc containers.
func TestOTELTracingWithConsoleExporter(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	// Upgrade existing AI Gateway installation with OTEL_TRACES_EXPORTER=console.
	t.Log("Upgrading AI Gateway with OTEL_TRACES_EXPORTER=console")

	// Upgrade the existing "ai-eg" release with new env vars.
	helm := exec.CommandContext(ctx, "go", "tool", "helm", "upgrade", "ai-eg",
		"../../manifests/charts/ai-gateway-helm",
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

	// Apply the basic example which will trigger pod creation.
	const manifest = "../../examples/basic/basic.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))

	// Wait for gateway pod to be created.
	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	// Get the pod with extProc container and verify env vars.
	t.Log("Checking extProc container for OTEL environment variables")

	// Get pod name.
	getPodsCmd := exec.CommandContext(ctx, "kubectl", "get", "pods", // #nosec G204
		"-n", e2elib.EnvoyGatewayNamespace,
		"-l", egSelector,
		"-o", "jsonpath={.items[0].metadata.name}")

	podNameBytes, err := getPodsCmd.Output()
	require.NoError(t, err)
	podName := string(podNameBytes)
	require.NotEmpty(t, podName)

	// Get the pod description to check env vars.
	describeCmd := exec.CommandContext(ctx, "kubectl", "get", "pod", podName,
		"-n", e2elib.EnvoyGatewayNamespace,
		"-o", "jsonpath={.spec.containers[?(@.name=='ai-gateway-extproc')].env}")

	describeOutput := &bytes.Buffer{}
	describeCmd.Stdout = describeOutput
	describeCmd.Stderr = describeOutput

	err = describeCmd.Run()
	require.NoError(t, err)

	envVars := describeOutput.String()

	// Verify that our OTEL env vars are present in the pod spec.
	require.Contains(t, envVars, `"name":"OTEL_TRACES_EXPORTER","value":"console"`,
		"Expected OTEL_TRACES_EXPORTER=console in extProc container spec")
	require.Contains(t, envVars, `"name":"OTEL_SERVICE_NAME","value":"ai-gateway-e2e-test"`,
		"Expected OTEL_SERVICE_NAME=ai-gateway-e2e-test in extProc container spec")

	t.Log("OTEL environment variables successfully verified in extProc container")

	// Cleanup - restore original configuration.
	t.Log("Restoring original AI Gateway configuration")
	// Re-install AI Gateway with default settings.
	restoreHelm := exec.CommandContext(ctx, "go", "tool", "helm", "upgrade", "ai-eg",
		"../../manifests/charts/ai-gateway-helm",
		"-n", "envoy-ai-gateway-system")
	_ = restoreHelm.Run()

	// Clean up the basic example resources.
	_ = e2elib.KubectlDeleteManifest(ctx, manifest)
}
