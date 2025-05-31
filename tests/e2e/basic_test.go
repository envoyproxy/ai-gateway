// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_e2e

package e2e

import (
	"cmp"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
)

// TestExamplesBasic tests the basic example in examples/basic directory.
func Test_Examples_Basic(t *testing.T) {
	const manifest = "../../examples/basic/basic.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), manifest))

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic"
	requireWaitForGatewayPodReady(t, egSelector)

	testUpstreamCase := examplesBasicTestCase{name: "testupsream", modelName: "some-cool-self-hosted-model"}
	testUpstreamCase.run(t, egNamespace, egSelector)

	// This requires the following environment variables to be set:
	//   - TEST_AWS_ACCESS_KEY_ID
	//   - TEST_AWS_SECRET_ACCESS_KEY
	//   - TEST_OPENAI_API_KEY
	//
	// A test case will be skipped if the corresponding environment variable is not set.
	t.Run("with credentials", func(t *testing.T) {
		read, err := os.ReadFile(manifest)
		require.NoError(t, err)
		// Replace the placeholder with the actual credentials.
		openAiApiKey := os.Getenv("TEST_OPENAI_API_KEY")
		awsAccessKeyID := os.Getenv("TEST_AWS_ACCESS_KEY_ID")
		awsSecretAccessKey := os.Getenv("TEST_AWS_SECRET_ACCESS_KEY")
		replaced := strings.ReplaceAll(string(read), "OPENAI_API_KEY", cmp.Or(openAiApiKey, "dummy-openai-api-key"))
		replaced = strings.ReplaceAll(replaced, "AWS_ACCESS_KEY_ID", cmp.Or(awsAccessKeyID, "dummy-aws-access-key-id"))
		replaced = strings.ReplaceAll(replaced, "AWS_SECRET_ACCESS_KEY", cmp.Or(awsSecretAccessKey, "dummy-aws-secret-access-key"))
		require.NoError(t, kubectlApplyManifestStdin(t.Context(), replaced))

		time.Sleep(5 * time.Second) // At least 5 seconds for the updated secret to be propagated.

		for _, tc := range []examplesBasicTestCase{
			{name: "openai", modelName: "gpt-4o-mini", skip: openAiApiKey == ""},
			{name: "aws", modelName: "us.meta.llama3-2-1b-instruct-v1:0", skip: awsAccessKeyID == "" || awsSecretAccessKey == ""},
		} {
			tc.run(t, egNamespace, egSelector)
		}
	})
}

type examplesBasicTestCase struct {
	name      string
	modelName string
	skip      bool
}

func (tc examplesBasicTestCase) run(t *testing.T, egNamespace, egSelector string) {
	t.Run(tc.name, func(t *testing.T) {
		if tc.skip {
			t.Skip("skipped due to missing credentials")
		}
		require.Eventually(t, func() bool {
			fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultServicePort)
			defer fwd.kill()

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			client := openai.NewClient(option.WithBaseURL(fwd.address() + "/v1/"))

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
