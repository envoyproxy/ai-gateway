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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const manifest = "../../examples/basic/basic.yaml"
	require.NoError(t, kubectlApplyManifest(ctx, manifest))

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-basic"
	requireWaitForPodReady(t, egNamespace, egSelector)

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
		require.NoError(t, kubectlApplyManifestStdin(ctx, replaced))

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
			fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultPort)
			defer fwd.kill()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			client := openai.NewClient(option.WithBaseURL(fwd.address() + "/v1/"))

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
		}, 20*time.Second, 3*time.Second)
	})
}
