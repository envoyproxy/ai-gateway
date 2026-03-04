// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	cohere "github.com/cohere-ai/cohere-go/v2"
	"github.com/cohere-ai/cohere-go/v2/core"
	coherev2client "github.com/cohere-ai/cohere-go/v2/v2"
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
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	testUpstreamCase := examplesBasicChatCompletionsTestCase{name: "testupsream", modelName: "some-cool-self-hosted-model"}
	testUpstreamCase.run(t, egSelector)

	// This requires the following environment variables to be set:
	//   - TEST_AWS_ACCESS_KEY_ID
	//   - TEST_AWS_SECRET_ACCESS_KEY
	//   - TEST_OPENAI_API_KEY
	//   - TEST_ANTHROPIC_API_KEY
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

		anthropicManifest, err := os.ReadFile(manifestDir + "/anthropic.yaml")
		require.NoError(t, err)
		require.NoError(t, e2elib.KubectlApplyManifestStdin(t.Context(), strings.ReplaceAll(string(anthropicManifest), "ANTHROPIC_API_KEY", cc.AnthropicAPIKey)))

		// Apply Cohere resources if credentials are set
		cohereManifest, err := os.ReadFile(manifestDir + "/cohere.yaml")
		require.NoError(t, err)
		require.NoError(t, e2elib.KubectlApplyManifestStdin(t.Context(), strings.ReplaceAll(string(cohereManifest), "COHERE_API_KEY", cc.CohereAPIKey)))

		time.Sleep(5 * time.Second) // At least 5 seconds for the updated secret to be propagated.

		for _, tc := range []examplesBasicChatCompletionsTestCase{
			{name: "openai", modelName: "gpt-4o-mini", skip: !cc.OpenAIValid},
			{name: "aws", modelName: "us.meta.llama3-2-1b-instruct-v1:0", skip: !cc.AWSValid},
		} {
			tc.run(t, egSelector)
		}

		// Cohere v2 rerank test using Cohere SDK routed via gateway
		t.Run("cohere_v2_rerank", func(t *testing.T) {
			cc.MaybeSkip(t, internaltesting.RequiredCredentialCohere)
			internaltesting.RequireEventuallyNoError(t, func() error {
				fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
				defer fwd.Kill()

				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()

				client := coherev2client.NewClient(&core.RequestOptions{
					BaseURL: fwd.Address() + "/cohere",
					Token:   "dummy",
				})
				topN := 2
				req := &cohere.V2RerankRequest{
					Model: "rerank-english-v3.0",
					Query: "reset password",
					Documents: []string{
						"How to reset my password?",
						"This is unrelated content",
					},
					TopN: &topN,
				}

				resp, callErr := client.Rerank(ctx, req)
				if callErr != nil {
					return fmt.Errorf("cohere rerank error: %w", callErr)
				}
				if len(resp.Results) == 0 {
					return errors.New("no rerank results returned")
				}
				return nil
			}, 20*time.Second, 3*time.Second)
		})

		t.Run("anthropic", func(t *testing.T) {
			cc.MaybeSkip(t, internaltesting.RequiredCredentialAnthropic)
			internaltesting.RequireEventuallyNoError(t, func() error {
				fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
				defer fwd.Kill()

				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()

				client := anthropic.NewClient(
					anthropicoption.WithAPIKey("dummy"),
					anthropicoption.WithBaseURL(fwd.Address()+"/anthropic/"),
				)

				stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
					Model:     anthropic.ModelClaudeSonnet4_5,
					MaxTokens: 1024,
					Messages: []anthropic.MessageParam{
						anthropic.NewUserMessage(anthropic.NewTextBlock("say hi.")),
					},
				})

				message := anthropic.Message{}
				nonEmptyResponse := false
				for stream.Next() {
					event := stream.Current()
					err = message.Accumulate(event)
					if err != nil {
						return fmt.Errorf("failed to accumulate event: %w", err)
					}

					t.Logf("event: %+v", event)
					if eventVariant, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
						if textDelta, ok := eventVariant.Delta.AsAny().(anthropic.TextDelta); ok {
							nonEmptyResponse = true
							t.Logf("received text delta: %s", textDelta.Text)
						}
					}
				}
				if err = stream.Err(); err != nil {
					return fmt.Errorf("stream error: %w", err)
				}
				if !nonEmptyResponse {
					return errors.New("no non-empty response received")
				}
				return nil
			}, 20*time.Second, 3*time.Second)
		})
	})
}

type examplesBasicChatCompletionsTestCase struct {
	name      string
	modelName string
	skip      bool
}

func (tc examplesBasicChatCompletionsTestCase) run(t *testing.T, egSelector string) {
	t.Run(tc.name, func(t *testing.T) {
		if tc.skip {
			t.Skip("skipped due to missing credentials")
		}
		internaltesting.RequireEventuallyNoError(t, func() error {
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
				return fmt.Errorf("chat completion error: %w", err)
			}
			var choiceNonEmpty bool
			for i := range chatCompletion.Choices {
				choice := &chatCompletion.Choices[i]
				t.Logf("choice: %s", choice.Message.Content)
				if choice.Message.Content != "" {
					choiceNonEmpty = true
				}
			}
			if !choiceNonEmpty {
				return errors.New("no non-empty choice found")
			}
			return nil
		}, 20*time.Second, 3*time.Second)
	})
}
