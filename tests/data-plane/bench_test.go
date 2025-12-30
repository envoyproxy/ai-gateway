// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dataplane

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/version"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// BenchmarkChatCompletions benchmarks the chat/completions endpoint for various backends.
//
//	$ go install golang.org/x/perf/cmd/...@latest
//	$ git checkout origin/main # Any base branch/commit to compare against.
//	$ go test ./tests/data-plane -run='^$' -timeout=20m -count=10 -bench='BenchmarkChatCompletions' . > old.txt
//	$ git checkout <your-feature-branch>
//	$ go test ./tests/data-plane -run='^$' -timeout=20m -count=10 -bench='BenchmarkChatCompletions' . > new.txt
//	$ benchstat old.txt new.txt
func BenchmarkChatCompletions(b *testing.B) {
	config := &filterapi.Config{
		Version: version.Parse(),
		Backends: []filterapi.Backend{
			testUpstreamOpenAIBackend,
			testUpstreamAAWSBackend,
			testUpstreamGCPVertexAIBackend,
			testUpstreamGCPAnthropicAIBackend,
		},
	}

	configBytes, err := yaml.Marshal(config)
	require.NoError(b, err)
	env := startTestEnvironment(b, string(configBytes), false, false)
	time.Sleep(5 * time.Second)

	listenerPort := env.EnvoyListenerPort()
	smallRequest := createChatCompletionRequest(100)     // ~6KB.
	mediumRequest := createChatCompletionRequest(10000)  // ~600KB.
	largeRequest := createChatCompletionRequest(100000)  // ~6MB.
	xlargeRequest := createChatCompletionRequest(500000) // ~30MB.

	for _, backend := range []string{
		"openai",
		"aws-bedrock",
		"gcp-vertexai",
		"gcp-anthropicai",
	} {
		b.Run(backend, func(b *testing.B) {
			testCases := []struct {
				name                 string
				requestBody          string
				fakeResponseBodyType string
			}{
				{name: "small", requestBody: smallRequest, fakeResponseBodyType: "small"},
				{name: "medium", requestBody: mediumRequest, fakeResponseBodyType: "medium"},
				{name: "large", requestBody: largeRequest, fakeResponseBodyType: "large"},
				{name: "xlarge", requestBody: xlargeRequest, fakeResponseBodyType: "large"},
			}

			for _, tc := range testCases {
				b.Run(tc.name, func(b *testing.B) {
					listenerAddress := fmt.Sprintf("http://localhost:%d", listenerPort)

					b.ResetTimer()
					b.RunParallel(func(pb *testing.PB) {
						req, err := http.NewRequestWithContext(context.Background(),
							http.MethodPost, listenerAddress+"/v1/chat/completions", nil)
						require.NoError(b, err)

						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("x-test-backend", backend)
						req.Header.Set(testupstreamlib.FakeResponseHeaderKey, tc.fakeResponseBodyType)
						req.Header.Set(testupstreamlib.ResponseStatusKey, "200")

						for pb.Next() {
							req.Body = io.NopCloser(strings.NewReader(tc.requestBody))
							req.ContentLength = int64(len(tc.requestBody))

							resp, err := http.DefaultClient.Do(req)
							require.NoError(b, err)

							_, err = io.ReadAll(resp.Body)
							require.NoError(b, err)
							resp.Body.Close()

							require.Equal(b, http.StatusOK, resp.StatusCode)
						}
					})
				})
			}
		})
	}
}

func createChatCompletionRequest(numMessages int) string {
	var messages []string
	for i := 0; i < numMessages; i++ {
		messages = append(messages, fmt.Sprintf(`{"role": "user", "content": "This is message number %d."}`, i+1))
	}
	largeRequestBody := fmt.Sprintf(`{
		"model": "gpt-4",
		"messages": [%s],
		"max_tokens": 100
	}`, strings.Join(messages, ","))
	return largeRequestBody
}

// BenchmarkEmbeddings benchmarks the embeddings endpoint.
func BenchmarkEmbeddings(b *testing.B) {
	config := &filterapi.Config{
		Version: version.Parse(),
		Backends: []filterapi.Backend{
			testUpstreamOpenAIBackend,
		},
	}

	configBytes, err := yaml.Marshal(config)
	require.NoError(b, err)
	env := startTestEnvironment(b, string(configBytes), false, true)

	listenerPort := env.EnvoyListenerPort()

	testCases := []struct {
		name         string
		backend      string
		requestBody  string
		responseBody string
	}{
		{
			name:    "OpenAI_Embeddings",
			backend: "openai",
			requestBody: `{
				"model": "text-embedding-ada-002",
				"input": "This is a benchmark test for embeddings endpoint."
			}`,
			responseBody: `{
				"object": "list",
				"data": [{
					"object": "embedding",
					"embedding": [0.0023064255, -0.009327292, -0.0028842222, 0.012345678, -0.087654321],
					"index": 0
				}],
				"model": "text-embedding-ada-002",
				"usage": {
					"prompt_tokens": 10,
					"total_tokens": 10
				}
			}`,
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			listenerAddress := fmt.Sprintf("http://localhost:%d", listenerPort)

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				req, err := http.NewRequestWithContext(context.Background(),
					http.MethodPost, listenerAddress+"/v1/embeddings", nil)
				require.NoError(b, err)

				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("x-test-backend", tc.backend)
				req.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
					base64.StdEncoding.EncodeToString([]byte(tc.responseBody)))
				req.Header.Set(testupstreamlib.ResponseStatusKey, "200")

				for pb.Next() {
					req.Body = io.NopCloser(strings.NewReader(tc.requestBody))
					req.ContentLength = int64(len(tc.requestBody))

					resp, err := http.DefaultClient.Do(req)
					require.NoError(b, err)

					_, err = io.ReadAll(resp.Body)
					require.NoError(b, err)
					resp.Body.Close()

					require.Equal(b, http.StatusOK, resp.StatusCode)
				}
			})
		})
	}
}

// BenchmarkChatCompletionsStreaming benchmarks streaming chat completions.
func BenchmarkChatCompletionsStreaming(b *testing.B) {
	now := time.Unix(int64(time.Now().Second()), 0).UTC()

	config := &filterapi.Config{
		Version: version.Parse(),
		Backends: []filterapi.Backend{
			testUpstreamOpenAIBackend,
			testUpstreamAAWSBackend,
			testUpstreamGCPVertexAIBackend,
		},
		Models: []filterapi.Model{
			{Name: "test-model", OwnedBy: "Envoy AI Gateway", CreatedAt: now},
		},
	}

	configBytes, err := yaml.Marshal(config)
	require.NoError(b, err)
	env := startTestEnvironment(b, string(configBytes), false, true)

	listenerPort := env.EnvoyListenerPort()

	testCases := []struct {
		name         string
		backend      string
		responseType string
		requestBody  string
		responseBody string
	}{
		{
			name:         "OpenAI_Streaming",
			backend:      "openai",
			responseType: "sse",
			requestBody: `{
				"model": "gpt-4",
				"messages": [
					{"role": "user", "content": "Hello, this is a streaming benchmark test."}
				],
				"stream": true
			}`,
			responseBody: `
{"id":"chatcmpl-benchmark","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}
{"id":"chatcmpl-benchmark","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" from"},"finish_reason":null}]}
{"id":"chatcmpl-benchmark","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" streaming"},"finish_reason":null}]}
{"id":"chatcmpl-benchmark","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" benchmark"},"finish_reason":"stop"}]}
{"id":"chatcmpl-benchmark","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":8,"total_tokens":18}}
[DONE]
`,
		},
		{
			name:         "AWS_Streaming",
			backend:      "aws-bedrock",
			responseType: "aws-event-stream",
			requestBody: `{
				"model": "claude-3-sonnet",
				"messages": [
					{"role": "user", "content": "Hello, this is a streaming benchmark test."}
				],
				"stream": true
			}`,
			responseBody: `{"role":"assistant"}
{"delta":{"text":"Hello"}}
{"delta":{"text":" from"}}
{"delta":{"text":" AWS"}}
{"delta":{"text":" streaming"}}
{"stopReason":"end_turn"}
{"usage":{"inputTokens":10, "outputTokens":8, "totalTokens":18}}
`,
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			listenerAddress := fmt.Sprintf("http://localhost:%d", listenerPort)

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				req, err := http.NewRequestWithContext(context.Background(),
					http.MethodPost, listenerAddress+"/v1/chat/completions", nil)
				require.NoError(b, err)

				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("x-test-backend", tc.backend)
				req.Header.Set(testupstreamlib.ResponseTypeKey, tc.responseType)
				req.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
					base64.StdEncoding.EncodeToString([]byte(tc.responseBody)))
				req.Header.Set(testupstreamlib.ResponseStatusKey, "200")

				for pb.Next() {
					req.Body = io.NopCloser(strings.NewReader(tc.requestBody))
					req.ContentLength = int64(len(tc.requestBody))

					resp, err := http.DefaultClient.Do(req)
					require.NoError(b, err)

					require.NoError(b, resp.Body.Close())
					require.Equal(b, http.StatusOK, resp.StatusCode)
				}
			})
		})
	}
}
