// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

// TestExamplesBasic tests the basic example in examples/basic directory.
func Test_Examples_Basic(t *testing.T) {
	const manifestDir = "../../examples/basic"
	const manifest = manifestDir + "/basic.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(t.Context(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	testUpstreamCase := examplesBasicTestCase{name: "testupsream", modelName: "some-cool-self-hosted-model"}
	testUpstreamCase.run(t, egSelector)

	// This requires the following environment variables to be set:
	//   - TEST_AWS_ACCESS_KEY_ID
	//   - TEST_AWS_SECRET_ACCESS_KEY
	//   - TEST_OPENAI_API_KEY
	//
	// A test case will be skipped if the corresponding environment variable is not set.
	t.Run("with credentials", func(t *testing.T) {
		cc := internaltesting.RequireNewCredentialsContext()

		// Replace the placeholders with the actual credentials and apply the manifests.
		openAIManifest, err := os.ReadFile(manifestDir + "/openai.yaml")
		require.NoError(t, err)
		require.NoError(t, e2elib.KubectlApplyManifestStdin(t.Context(), strings.ReplaceAll(string(openAIManifest), "OPENAI_API_KEY", cc.OpenAIAPIKey)))
		awsManifest, err := os.ReadFile(manifestDir + "/aws.yaml")
		require.NoError(t, err)
		awsManifestReplaced := strings.ReplaceAll(string(awsManifest), "AWS_ACCESS_KEY_ID", cc.AWSAccessKeyID)
		awsManifestReplaced = strings.ReplaceAll(awsManifestReplaced, "AWS_SECRET_ACCESS_KEY", cc.AWSSecretAccessKey)
		require.NoError(t, e2elib.KubectlApplyManifestStdin(t.Context(), awsManifestReplaced))

		time.Sleep(5 * time.Second) // At least 5 seconds for the updated secret to be propagated.

		for _, tc := range []examplesBasicTestCase{
			{name: "openai", modelName: "gpt-4o-mini", skip: !cc.OpenAIValid},
			{name: "aws", modelName: "us.meta.llama3-2-1b-instruct-v1:0", skip: !cc.AWSValid},
		} {
			tc.run(t, egSelector)
		}
	})
}

type examplesBasicTestCase struct {
	name      string
	modelName string
	skip      bool
}

func (tc examplesBasicTestCase) run(t *testing.T, egSelector string) {
	t.Run(tc.name, func(t *testing.T) {
		if tc.skip {
			t.Skip("skipped due to missing credentials")
		}
		require.Eventually(t, func() bool {
			fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
			defer fwd.Kill()

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			client := openai.NewClient(option.WithBaseURL(fwd.Address() + "/v1/"))

			chatCompletion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("Say this is a test"),
				},
				Model: tc.modelName,
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
		}, 20*time.Second, 3*time.Second)
	})
}
