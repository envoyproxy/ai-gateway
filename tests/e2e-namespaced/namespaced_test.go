// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

func TestMain(m *testing.M) {
	e2elib.TestMain(m, e2elib.AIGatewayHelmOption{
		Namespace: "envoy-ai-gateway-e2e", // Also install AI Gateway on a different namespace
		AdditionalArgs: []string{
			// Configure the controller to only watch certain namespaces
			// By skipping the "route1-ns" the models defined in that namespace routes
			// won't be returned in the ListModels response.
			"--set", "controller.watch.namespaces={gateway-ns,route2-ns}",
			"--set", "endpointConfig.openai=/openai",
		},
	}, false, true,
	)
}

// TestNamespaced verifies that only the routes in the watched namespaces are taken into account
// and that configuring endpointConfig.openai=/openai allows accessing the OpenAI-compatible
// Models API under /openai/v1.
// To verify this we call the ListModels endpoint, and we should only get the models exposed by
// the route in the watched namespace.
func TestNamespaced(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	const manifest = "testdata/namespaced.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(ctx, manifest))

	// Wait for the Gateway pod to be ready with the correct selector.
	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=gw"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	// Verify the extProc init container args include the endpoint prefix configuration.
	require.Eventually(t, func() bool {
		// Find a pod belonging to the Gateway.
		getPodsCmd := exec.CommandContext(ctx, "kubectl", "get", "pods", // #nosec G204
			"-n", e2elib.EnvoyGatewayNamespace,
			"-l", egSelector,
			"-o", "jsonpath={.items[0].metadata.name}")

		podNameBytes, err := getPodsCmd.Output()
		if err != nil {
			t.Logf("failed to get pod name: %v", err)
			return false
		}
		podName := string(podNameBytes)
		if len(podName) == 0 {
			t.Log("no pods found with the specified selector, retrying...")
			return false
		}
		t.Logf("found pod: %s", podName)

		// Get the init container args for ai-gateway-extproc.
		argsCmd := exec.CommandContext(ctx, "kubectl", "get", "pod", podName,
			"-n", e2elib.EnvoyGatewayNamespace,
			"-o", "jsonpath={.spec.initContainers[?(@.name=='ai-gateway-extproc')].args}")
		argsOutput, err := argsCmd.Output()
		if err != nil {
			t.Logf("failed to get container args for pod %s: %v", podName, err)
			return false
		}
		containerArgs := string(argsOutput)
		t.Logf("extProc container args: %s", containerArgs)

		defer func() {
			// Delete the pod so it can be recreated with updated configuration if needed.
			deletePodsCmd := e2elib.Kubectl(ctx, "delete", "pod", podName,
				"-n", e2elib.EnvoyGatewayNamespace,
				"--ignore-not-found=true")
			if err := deletePodsCmd.Run(); err != nil {
				t.Logf("failed to delete pod %s: %v", podName, err)
			}
		}()

		// Assert endpointPrefixes flag and mapping exists.
		if !strings.Contains(containerArgs, "-endpointPrefixes") {
			t.Log("expected -endpointPrefixes flag in extProc container args")
			return false
		}
		if !strings.Contains(containerArgs, "openaiPrefix:/openai") {
			t.Log("expected openaiPrefix:/openai mapping in extProc container args")
			return false
		}
		return true
	}, 2*time.Minute, 5*time.Second)

	// With the prefix configured, the OpenAI client should target /openai/v1.
	wantModels := []string{"route2-model"}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
		defer fwd.Kill()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		client := openai.NewClient(option.WithBaseURL(fwd.Address() + "/openai/v1/"))

		models, err := client.Models.List(ctx)
		require.NoError(c, err)

		var modelNames []string
		for _, model := range models.Data {
			modelNames = append(modelNames, model.ID)
		}
		sort.Strings(modelNames)
		t.Logf("models via /openai/v1: %v", modelNames)

		require.Equal(c, wantModels, modelNames)
	}, 40*time.Second, 3*time.Second)
}
