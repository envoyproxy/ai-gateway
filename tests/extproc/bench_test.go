// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

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
func BenchmarkChatCompletions(b *testing.B) {
	config := &filterapi.Config{
		Version: version.Parse(),
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterapi.LLMRequestCostTypeInputToken},
		},
		Backends: []filterapi.Backend{
			testUpstreamOpenAIBackend,
			testUpstreamAAWSBackend,
			testUpstreamGCPVertexAIBackend,
			testUpstreamGCPAnthropicAIBackend,
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
			name:    "OpenAI",
			backend: "openai",
			requestBody: `{
				"model": "gpt-4",
				"messages": [
					{"role": "user", "content": "Hello, this is a benchmark test message."}
				],
				"max_tokens": 100
			}`,
			responseBody: `{
				"id": "chatcmpl-benchmark",
				"object": "chat.completion",
				"created": 1234567890,
				"model": "gpt-4",
				"choices": [{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "Hello! This is a benchmark response from OpenAI."
					},
					"finish_reason": "stop"
				}],
				"usage": {
					"prompt_tokens": 10,
					"completion_tokens": 12,
					"total_tokens": 22
				}
			}`,
		},
		{
			name:    "AWS_Bedrock",
			backend: "aws-bedrock",
			requestBody: `{
				"model": "claude-3-sonnet",
				"messages": [
					{"role": "user", "content": "Hello, this is a benchmark test message."}
				],
				"max_tokens": 100
			}`,
			responseBody: `{
				"output": {
					"message": {
						"content": [{"text": "Hello! This is a benchmark response from AWS Bedrock."}],
						"role": "assistant"
					}
				},
				"stopReason": "end_turn",
				"usage": {
					"inputTokens": 10,
					"outputTokens": 12,
					"totalTokens": 22
				}
			}`,
		},
		{
			name:    "GCP_VertexAI",
			backend: "gcp-vertexai",
			requestBody: `{
				"model": "gemini-1.5-pro",
				"messages": [
					{"role": "user", "content": "Hello, this is a benchmark test message."}
				],
				"max_tokens": 100
			}`,
			responseBody: `{
				"candidates": [{
					"content": {
						"parts": [{"text": "Hello! This is a benchmark response from GCP Vertex AI."}],
						"role": "model"
					},
					"finishReason": "STOP"
				}],
				"usageMetadata": {
					"promptTokenCount": 10,
					"candidatesTokenCount": 12,
					"totalTokenCount": 22
				}
			}`,
		},
		{
			name:    "GCP_AnthropicAI",
			backend: "gcp-anthropicai",
			requestBody: `{
				"model": "claude-3-sonnet",
				"messages": [
					{"role": "user", "content": "Hello, this is a benchmark test message."}
				],
				"max_tokens": 100
			}`,
			responseBody: `{
				"id": "msg_benchmark",
				"type": "message",
				"role": "assistant",
				"stop_reason": "end_turn",
				"content": [{"type": "text", "text": "Hello! This is a benchmark response from GCP Anthropic AI."}],
				"usage": {
					"input_tokens": 10,
					"output_tokens": 12
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
					http.MethodPost, listenerAddress+"/v1/chat/completions", nil)
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

// BenchmarkEmbeddings benchmarks the embeddings endpoint.
func BenchmarkEmbeddings(b *testing.B) {
	config := &filterapi.Config{
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterapi.LLMRequestCostTypeInputToken},
		},
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
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterapi.LLMRequestCostTypeInputToken},
		},
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
