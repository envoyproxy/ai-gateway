// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"sort"
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
		},
	}, false, true,
	)
}

// TestNamespaced verifies that only the routes in the watched namespaces are taken into account.
// To verify this we call the ListModels endpoint, and we should only get the models exposed by
// the route in the watched namespace.
func TestNamespaced(t *testing.T) {
	const manifest = "testdata/namespaced.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))

	// Wait for the Gateway pod to be ready with the correct selector.
	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=gw"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	wantModels := []string{"route2-model"}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
		defer fwd.Kill()

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		client := openai.NewClient(option.WithBaseURL(fwd.Address() + "/v1/"))

		models, err := client.Models.List(ctx)
		require.NoError(c, err)

		var modelNames []string
		for _, model := range models.Data {
			modelNames = append(modelNames, model.ID)
		}
		sort.Strings(modelNames)
		t.Logf("models: %v", modelNames)

		require.Equal(c, wantModels, modelNames)
	}, 40*time.Second, 3*time.Second)
}
