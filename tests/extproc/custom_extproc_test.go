//go:build test_extproc

package extproc

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"runtime"
	"testing"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterconfig"
)

// TestExtProcCustomRouter tests examples/extproc_custom_router.
func TestExtProcCustomRouter(t *testing.T) {
	requireBinaries(t)
	requireRunEnvoy(t, "/dev/null", "dummy")
	requireTestUpstream(t)
	configPath := t.TempDir() + "/extproc-config.yaml"
	requireWriteExtProcConfig(t, configPath, &filterconfig.Config{
		schema: openAISchema,
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
	stdout := &bytes.Buffer{}
	requireExtProc(t, stdout, fmt.Sprintf("../../out/extproc_custom_router-%s-%s",
		runtime.GOOS, runtime.GOARCH), configPath)

	require.Eventually(t, func() bool {
		client := openai.NewClient(option.WithBaseURL(listenerAddress+"/v1/"),
			option.WithHeader(
				"x-expected-path", base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions"))),
			option.WithHeader("x-response-body",
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

	require.Contains(t, stdout.String(), "model name: something-cool") // This must be logged by the custom router.
}
