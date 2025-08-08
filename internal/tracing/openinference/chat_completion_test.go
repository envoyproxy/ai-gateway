// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openinference

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/tests/testotel"
)

func TestChatCompletionRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		req              *openai.ChatCompletionRequest
		reqBody          []byte
		expectedSpanName string
	}{
		{
			name:             "basic request",
			req:              basicReq,
			reqBody:          basicReqBody,
			expectedSpanName: "ChatCompletion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := ChatCompletionRecorder{}

			spanName, opts := recorder.StartParams(tt.req, tt.reqBody)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestChatCompletionRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.ChatCompletionRequest
		reqBody       []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic request",
			req:     basicReq,
			reqBody: basicReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(SpanKind, SpanKindLLM),
				attribute.String(LLMSystem, LLMSystemOpenAI),
				attribute.String(LLMModelName, openai.ModelGPT41Nano),
				attribute.String(InputValue, string(basicReqBody)),
				attribute.String(InputMimeType, MimeTypeJSON),
				attribute.String(LLMInvocationParameters, `{"model":"gpt-4.1-nano"}`),
				attribute.String(InputMessageAttribute(0, MessageRole), openai.ChatMessageRoleUser),
				attribute.String(InputMessageAttribute(0, MessageContent), "Hello!"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := ChatCompletionRecorder{}

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, tt.reqBody)
				return false
			})

			requireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
		})
	}
}

func TestChatCompletionRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		respBody       []byte
		expectedAttrs  []attribute.KeyValue
		expectedEvents []trace.Event
		expectedStatus trace.Status
	}{
		{
			name:       "successful response",
			statusCode: 200,
			respBody:   basicRespBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(LLMModelName, "gpt-4"),
				attribute.String(OutputMimeType, MimeTypeJSON),
				attribute.String(OutputMessageAttribute(0, MessageRole), "assistant"),
				attribute.String(OutputMessageAttribute(0, MessageContent), "Hello! How can I help you today?"),
				attribute.Int(LLMTokenCountPrompt, 20),
				attribute.Int(LLMTokenCountCompletion, 10),
				attribute.Int(LLMTokenCountTotal, 30),
				attribute.String(OutputValue, string(basicRespBody)),
			},
			expectedEvents: nil,
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:          "error response",
			statusCode:    400,
			respBody:      []byte(`{"error":{"message":"Invalid request","type":"invalid_request_error"}}`),
			expectedAttrs: []attribute.KeyValue{},
			expectedEvents: []trace.Event{{
				Name: "exception",
				Attributes: []attribute.KeyValue{
					attribute.String("exception.type", "BadRequestError"),
					attribute.String("exception.message", `Error code: 400 - {"error":{"message":"Invalid request","type":"invalid_request_error"}}`),
				},
				Time: time.Time{},
			}},
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: `Error code: 400 - {"error":{"message":"Invalid request","type":"invalid_request_error"}}`,
			},
		},
		{
			name:       "partial/invalid JSON response",
			statusCode: 200,
			respBody:   []byte(`{"id":"chatcmpl-123","object":"chat.completion"`),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(OutputValue, `{"id":"chatcmpl-123","object":"chat.completion"`),
			},
			expectedEvents: []trace.Event{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := ChatCompletionRecorder{}

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.statusCode, tt.respBody)
				return false
			})

			requireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			requireEventsEqual(t, tt.expectedEvents, actualSpan.Events)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestChatCompletionRecorder_RecordChunk(t *testing.T) {
	recorder := ChatCompletionRecorder{}

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordChunk(span, 0)
		recorder.RecordChunk(span, 1)
		return false
	})

	// No events when not streaming.
	require.Empty(t, actualSpan.Events)
}

// Tests for ChatCompletionStreamingRecorder.

func TestChatCompletionStreamingRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		req              *openai.ChatCompletionRequest
		expectedSpanName string
	}{
		{
			name: "basic streaming request",
			req: &openai.ChatCompletionRequest{
				Model: openai.ModelGPT41Nano,
				Messages: []openai.ChatCompletionMessageParamUnion{{
					Type: openai.ChatMessageRoleUser,
					Value: openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"},
						Role:    openai.ChatMessageRoleUser,
					},
				}},
				Stream: true,
			},
			expectedSpanName: "ChatCompletion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := ChatCompletionStreamingRecorder{}
			reqBody := mustJSON(tt.req)

			spanName, opts := recorder.StartParams(tt.req, reqBody)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestChatCompletionStreamingRecorder_RecordChunk(t *testing.T) {
	recorder := ChatCompletionStreamingRecorder{}

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordChunk(span, 0) // First chunk should add event.
		recorder.RecordChunk(span, 1) // Subsequent chunks should not.
		return false                  // Recording of chunks shouldn't end the span.
	})

	requireEventsEqual(t, []trace.Event{
		{
			Name: "First Token Stream Event",
			Time: time.Time{},
		},
	}, actualSpan.Events)
}

func TestChatCompletionStreamingRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		body          []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:       "streaming response with SSE data",
			statusCode: 200,
			body: []byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1702000000,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"logprobs":null,"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1702000000,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" world"},"logprobs":null,"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1702000000,"model":"gpt-4","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}

data: [DONE]
`),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(LLMModelName, "gpt-4"),
				attribute.String(OutputMimeType, MimeTypeJSON),
				attribute.String(OutputMessageAttribute(0, MessageRole), "assistant"),
				attribute.String(OutputMessageAttribute(0, MessageContent), "Hello world"),
				attribute.Int(LLMTokenCountPrompt, 10),
				attribute.Int(LLMTokenCountCompletion, 2),
				attribute.Int(LLMTokenCountTotal, 12),
				attribute.String(OutputValue, `{
  "id": "chatcmpl-123",
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "message": {
        "content": "Hello world",
        "role": "assistant"
      }
    }
  ],
  "created": 1702000000,
  "model": "gpt-4",
  "object": "chat.completion.chunk",
  "usage": {
    "completion_tokens": 2,
    "prompt_tokens": 10,
    "total_tokens": 12
  }
}`),
			},
		},
		{
			name:       "SSE single chunk response",
			statusCode: 200,
			body: []byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1702000000,"model":"gpt-4.1-nano-2025-04-14","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello! How can I assist you today?"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":9,"total_tokens":18}}

data: [DONE]`),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(LLMModelName, "gpt-4.1-nano-2025-04-14"),
				attribute.String(OutputMimeType, MimeTypeJSON),
				attribute.String(OutputMessageAttribute(0, MessageRole), "assistant"),
				attribute.String(OutputMessageAttribute(0, MessageContent), "Hello! How can I assist you today?"),
				attribute.Int(LLMTokenCountPrompt, 9),
				attribute.Int(LLMTokenCountCompletion, 9),
				attribute.Int(LLMTokenCountTotal, 18),
				attribute.String(OutputValue, `{
  "id": "chatcmpl-123",
  "choices": [
    {
      "finish_reason": "stop",
      "index": 0,
      "message": {
        "content": "Hello! How can I assist you today?",
        "role": "assistant"
      }
    }
  ],
  "created": 1702000000,
  "model": "gpt-4.1-nano-2025-04-14",
  "object": "chat.completion.chunk",
  "usage": {
    "completion_tokens": 9,
    "prompt_tokens": 9,
    "total_tokens": 18
  }
}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := ChatCompletionStreamingRecorder{}

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.statusCode, tt.body)
				return false // Recording response shouldn't end the span.
			})

			requireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, trace.Status{Code: codes.Ok, Description: ""}, actualSpan.Status)
		})
	}
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func ptr[T any](v T) *T {
	return &v
}
