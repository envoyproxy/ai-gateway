//go:build test_extproc

package extproc

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterconfig"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// TestExtProcCustomRouter tests examples/extproc_custom_router.
func TestExtProcCustomRouter(t *testing.T) {
	requireBinaries(t)
	requireRunEnvoy(t, "/dev/null")
	requireTestUpstream(t)
	configPath := t.TempDir() + "/extproc-config.yaml"
	requireWriteFilterConfig(t, configPath, &filterconfig.Config{
		Schema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		SelectedBackendHeaderKey: "x-selected-backend-name",
		ModelNameHeaderKey:       "x-model-name",
		Rules: []filterconfig.RouteRule{
			{
				Backends: []filterconfig.Backend{{Name: "testupstream", Schema: openAISchema}},
				Headers:  []filterconfig.HeaderMatch{{Name: "x-model-name", Value: "something-cool"}},
			},
		},
	})
	stdoutPath := t.TempDir() + "/extproc-stdout.log"
	f, err := os.Create(stdoutPath)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, f.Close())
	}()
	requireExtProc(t, f, fmt.Sprintf("../../out/extproc_custom_router-%s-%s",
		runtime.GOOS, runtime.GOARCH), configPath)

	require.Eventually(t, func() bool {
		client := openai.NewClient(option.WithBaseURL(listenerAddress+"/v1/"),
			option.WithHeader(
				testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions"))),
			option.WithHeader(testupstreamlib.ResponseBodyHeaderKey,
				base64.StdEncoding.EncodeToString([]byte(`{"choices":[{"message":{"content":"This is a test."}}]}`)),
			))
		chatCompletion, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
			Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say this is a test"),
			}),
			Model: openai.F("something-cool"),
		})
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}
		for _, choice := range chatCompletion.Choices {
			t.Logf("choice: %s", choice.Message.Content)
		}
		return true
	}, 10*time.Second, 1*time.Second)

	// Check that the custom router logs the model name after the file is closed.
	defer func() {
		stdout, err := os.ReadFile(stdoutPath)
		require.NoError(t, err)
		t.Logf("stdout: %s", stdout)
		require.Contains(t, string(stdout), "model name: something-cool") // This must be logged by the custom router.
	}()
}
