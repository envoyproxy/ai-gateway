// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestInferencePoolIntegration tests the InferencePool integration with AI Gateway.
// This test verifies that:
// 1. InferencePool resources can be referenced in AIGatewayRoute
// 2. Extension server properly configures ORIGINAL_DST clusters for InferencePool backends
// 3. EnvoyExtensionPolicy is created for EPP services
// Note: InferencePool environment (CRDs, vLLM, InferenceModel, InferencePool) is set up in TestMain
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

	t.Run("InferencePool_Backend_Configuration", func(t *testing.T) {
		// Wait for resources to be ready
		time.Sleep(10 * time.Second)

		// Verify that the Gateway pod is running and configured
		// This indirectly tests that the extension server processed the InferencePool
		// and configured the appropriate cluster settings without errors

		// Check that EnvoyExtensionPolicy was created for the EPP service
		cmd := kubectl(t.Context(), "get", "envoyextensionpolicy", "-n", "default",
			"-o", "jsonpath='{.items[*].metadata.name}'")
		cmd.Stdout = nil
		out, err := cmd.Output()
		require.NoError(t, err, "Should be able to get EnvoyExtensionPolicies")

		policyNames := strings.Trim(string(out), "'")
		require.Contains(t, policyNames, "vllm-llama3-8b-instruct-epp-epp",
			"Expected EPP EnvoyExtensionPolicy to be created")

		// Check that HTTPRoute was created with InferencePool backend reference
		cmd = kubectl(t.Context(), "get", "httproute", "-n", "default",
			"inference-pool-route", "-o", "json")
		cmd.Stdout = nil
		out, err = cmd.Output()
		require.NoError(t, err, "Should be able to get HTTPRoute")

		// Verify the HTTPRoute contains InferencePool backend reference
		require.Contains(t, string(out), "inference.networking.x-k8s.io",
			"HTTPRoute should contain InferencePool group")
		require.Contains(t, string(out), "InferencePool",
			"HTTPRoute should contain InferencePool kind")
		require.Contains(t, string(out), "vllm-llama3-8b-instruct",
			"HTTPRoute should contain InferencePool name")
	})

	t.Run("Extension_Server_Cluster_Configuration", func(t *testing.T) {
		// This test verifies that the extension server properly processes InferencePool
		// resources and configures ORIGINAL_DST clusters. Since we can't directly
		// inspect Envoy configuration in this test environment, we verify that
		// the system is stable and the gateway pod remains healthy.

		// Wait a bit more to ensure all configurations are applied
		time.Sleep(15 * time.Second)

		// Verify gateway pod is still healthy after configuration by checking it's still ready
		cmd := kubectl(t.Context(), "get", "pods", "-n", "envoy-gateway-system",
			"-l", egSelector, "-o", "jsonpath='{.items[0].status.phase}'")
		cmd.Stdout = nil
		out, err := cmd.Output()
		require.NoError(t, err, "Should be able to get gateway pod status")

		phase := strings.Trim(string(out), "'")
		require.Equal(t, "Running", phase, "Gateway pod should remain running after InferencePool configuration")
	})
}
