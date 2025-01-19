//go:build test_e2e

package e2e

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
)

// TestTranslationWithTestUpstream tests the translation with the test upstream.
func TestTranslationWithTestUpstream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const manifest = "testdata/translation_testupstream.yaml"
	require.NoError(t, kubectlApplyManifest(ctx, manifest))
	defer func() {
		// require.NoError(t, kubectlDeleteManifest(context.Background(), manifest))
	}()

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=translation-testupstream"
	requireWaitForPodReady(t, egNamespace, egSelector)

	fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultPort)
	defer fwd.kill()

	t.Run("/chat/completions", func(t *testing.T) {
		for _, tc := range []struct {
			name             string
			modelName        string
			expPath          string
			fakeResponseBody string
		}{
			{
				name:             "openai",
				modelName:        "some-cool-model",
				expPath:          "/v1/chat/completions",
				fakeResponseBody: `{"choices":[{"message":{"content":"This is a test."}}]}`,
			},
			{
				name:             "aws-bedrock",
				modelName:        "another-cool-model",
				expPath:          "/model/another-cool-model/converse",
				fakeResponseBody: `{"output":{"message":{"content":[{"text":"response"},{"text":"from"},{"text":"assistant"}],"role":"assistant"}},"stopReason":null,"usage":{"inputTokens":10,"outputTokens":20,"totalTokens":30}}`,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.Eventually(t, func() bool {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()

					t.Logf("modelName: %s", tc.modelName)
					client := openai.NewClient(option.WithBaseURL(fwd.address()+"/v1/"),
						option.WithHeader(
							"x-test-case-name", tc.name),
						option.WithHeader(
							"x-expected-path", base64.StdEncoding.EncodeToString([]byte(tc.expPath))),
						option.WithHeader("x-response-body",
							base64.StdEncoding.EncodeToString([]byte(tc.fakeResponseBody)),
						))

					chatCompletion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
						Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
							openai.UserMessage("Say this is a test"),
						}),
						Model: openai.F(tc.modelName),
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
				}, 10*time.Second, 1*time.Second)
			})
		}
	})
}
