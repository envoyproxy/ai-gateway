// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

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

	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// TestTranslationWithTestUpstream tests the translation with the test upstream.
func TestTranslationWithTestUpstream(t *testing.T) {
	const manifest = "testdata/translation_testupstream.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), manifest))

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=translation-testupstream"
	requireWaitForGatewayPodReady(t, egSelector)

	t.Run("/chat/completions", func(t *testing.T) {
		for _, tc := range []struct {
			name              string
			modelName         string
			expHost           string
			expTestUpstreamID string
			expPath           string
			fakeResponseBody  string
		}{
			{
				name:              "openai",
				modelName:         "some-cool-model",
				expTestUpstreamID: "primary",
				expPath:           "/v1/chat/completions",
				expHost:           "testupstream.default.svc.cluster.local",
				fakeResponseBody:  `{"choices":[{"message":{"content":"This is a test."}}]}`,
			},
			{
				name:              "aws-bedrock",
				modelName:         "another-cool-model",
				expTestUpstreamID: "canary",
				expHost:           "testupstream-canary.default.svc.cluster.local",
				expPath:           "/model/another-cool-model/converse",
				fakeResponseBody:  `{"output":{"message":{"content":[{"text":"response"},{"text":"from"},{"text":"assistant"}],"role":"assistant"}},"stopReason":null,"usage":{"inputTokens":10,"outputTokens":20,"totalTokens":30}}`,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.Eventually(t, func() bool {
					fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultServicePort)
					defer fwd.kill()

					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()

					t.Logf("modelName: %s", tc.modelName)
					client := openai.NewClient(option.WithBaseURL(fwd.address()+"/v1/"),
						option.WithHeader(
							testupstreamlib.ExpectedTestUpstreamIDKey, tc.expTestUpstreamID),
						option.WithHeader(
							testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte(tc.expPath))),
						option.WithHeader(testupstreamlib.ResponseBodyHeaderKey,
							base64.StdEncoding.EncodeToString([]byte(tc.fakeResponseBody)),
						),
						option.WithHeader(testupstreamlib.ExpectedHostKey, tc.expHost),
					)

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
				}, 10*time.Second, 1*time.Second)
			})
		}
	})
}
