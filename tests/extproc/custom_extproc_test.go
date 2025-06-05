// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_extproc

package extproc

import (
	"encoding/base64"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// TestExtProcCustomRouter tests examples/extproc_custom_router.
func TestExtProcCustomRouter(t *testing.T) {
	requireBinaries(t)
	requireRunEnvoy(t, "/dev/null")
	requireTestUpstream(t)
	configPath := t.TempDir() + "/extproc-config.yaml"
	requireWriteFilterConfig(t, configPath, &filterapi.Config{
		Schema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		SelectedRouteHeaderKey: routeSelectorHeader,
		ModelNameHeaderKey:     "x-model-name",
		Rules: []filterapi.RouteRule{
			{
				Name:    "testupstream-openai-route",
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "something-cool"}},
				Backends: []filterapi.Backend{
					alwaysFailingBackend,
					testUpstreamOpenAIBackend,
				},
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
		chatCompletion, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say this is a test"),
			},
			Model: "something-cool",
		})
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}
		for _, choice := range chatCompletion.Choices {
			t.Logf("choice: %s", choice.Message.Content)
		}
		return true
	}, eventuallyTimeout, eventuallyInterval)

	// Check that the custom router logs the model name after the file is closed.
	defer func() {
		stdout, err := os.ReadFile(stdoutPath)
		require.NoError(t, err)
		t.Logf("stdout: %s", stdout)
		require.Contains(t, string(stdout), "model name: something-cool") // This must be logged by the custom router.
	}()
}

// TestExtProcCustomMetrics tests examples/extproc_custom_metrics.
func TestExtProcCustomMetrics(t *testing.T) {
	requireBinaries(t)
	requireRunEnvoy(t, "/dev/null")
	requireTestUpstream(t)
	configPath := t.TempDir() + "/extproc-config.yaml"
	requireWriteFilterConfig(t, configPath, &filterapi.Config{
		Schema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		SelectedRouteHeaderKey: routeSelectorHeader,
		ModelNameHeaderKey:     "x-model-name",
		Rules: []filterapi.RouteRule{
			{
				Name:    "testupstream-openai-route",
				Headers: []filterapi.HeaderMatch{{Name: "x-model-name", Value: "something-cool"}},
				Backends: []filterapi.Backend{
					alwaysFailingBackend,
					testUpstreamOpenAIBackend,
				},
			},
		},
	})
	stdoutPath := t.TempDir() + "/extproc-stdout.log"
	f, err := os.Create(stdoutPath)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, f.Close())
	}()
	requireExtProc(t, f, fmt.Sprintf("../../out/extproc_custom_metrics-%s-%s",
		runtime.GOOS, runtime.GOARCH), configPath)

	require.Eventually(t, func() bool {
		client := openai.NewClient(option.WithBaseURL(listenerAddress+"/v1/"),
			option.WithHeader(
				testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions"))),
			option.WithHeader(testupstreamlib.ResponseBodyHeaderKey,
				base64.StdEncoding.EncodeToString([]byte(`{"choices":[{"message":{"content":"This is a test."}}]}`)),
			))
		chatCompletion, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say this is a test"),
			},
			Model: "something-cool",
		})
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}
		for _, choice := range chatCompletion.Choices {
			t.Logf("choice: %s", choice.Message.Content)
		}
		return true
	}, eventuallyTimeout, eventuallyInterval)

	// Check the custom metrics logs after the file is closed.
	defer func() {
		stdout, err := os.ReadFile(stdoutPath)
		require.NoError(t, err)
		t.Logf("stdout: %s", stdout)
		require.Contains(t, string(stdout), "msg=StartRequest")
		require.Contains(t, string(stdout), "msg=SetModel model=something-cool")
		require.Contains(t, string(stdout), "msg=SetBackend backend=testupstream")
		require.Contains(t, string(stdout), "msg=RecordTokenUsage inputTokens=0 outputTokens=0 totalTokens=0")
		require.Contains(t, string(stdout), "msg=RecordRequestCompletion success=true")
	}()
}
