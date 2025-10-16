// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

// TestCrossNamespace tests AIGatewayRoute with cross-namespace references.
// This test validates that:
//  1. A Gateway in one namespace (gateway-ns) can be referenced by an AIGatewayRoute in another namespace (route-ns)
//  2. An AIServiceBackend in one namespace (backend-ns) can be referenced by an AIGatewayRoute in another namespace (route-ns)
//     when a valid ReferenceGrant exists
//  3. The generated HTTPRoute and other resources work correctly across namespaces
//  4. Traffic routing works end-to-end through the cross-namespace setup.
func TestCrossNamespace(t *testing.T) {
	const manifest = "testdata/cross_namespace.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))

	// Wait for the Gateway pod to be ready with the correct selector.
	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=cross-namespace-gateway"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	t.Run("cross-namespace-backend-with-referencegrant", func(t *testing.T) {
		// Test that the AIGatewayRoute in route-ns can successfully route traffic
		// to an AIServiceBackend in backend-ns (different namespace) via ReferenceGrant.
		require.Eventually(t, func() bool {
			fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
			defer fwd.Kill()

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			client := openai.NewClient(option.WithBaseURL(fwd.Address() + "/v1/"))

			chatCompletion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("Test cross-namespace backend routing with ReferenceGrant"),
				},
				Model: "cross-namespace-backend-model",
			})
			if err != nil {
				t.Logf("error: %v", err)
				return false
			}

			var choiceNonEmpty bool
			for _, choice := range chatCompletion.Choices {
				t.Logf("choice: %s", choice.Message.Content)
				if choice.Message.Content != "" {
					choiceNonEmpty = true
				}
			}
			return choiceNonEmpty
		}, 40*time.Second, 3*time.Second)
	})
}
