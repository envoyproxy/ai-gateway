// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

var (
	basicResp = &openai.ChatCompletionResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: openai.JSONUNIXTime(time.Unix(1234567890, 0)),
		Model:   openai.ModelGPT5Nano,
		Choices: []openai.ChatCompletionResponseChoice{{
			Index: 0,
			Message: openai.ChatCompletionResponseChoiceMessage{
				Role:    "assistant",
				Content: ptr("Hello! How can I help you today?"),
			},
			FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
		}},
		Usage: openai.Usage{
			PromptTokens:     20,
			CompletionTokens: 10,
			TotalTokens:      30,
		},
	}
	basicRespBody = mustJSON(basicResp)

	toolsResp = &openai.ChatCompletionResponse{
		ID:    "chatcmpl-123",
		Model: openai.ModelGPT5Nano,
		Choices: []openai.ChatCompletionResponseChoice{{
			Index: 0,
			Message: openai.ChatCompletionResponseChoiceMessage{
				Role:    "assistant",
				Content: ptr("I can help you with that."),
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{{
					ID:   ptr("call_123"),
					Type: openai.ChatCompletionMessageToolCallType("function"),
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      "get_weather",
						Arguments: `{"location":"NYC"}`,
					},
				}},
			},
			FinishReason: openai.ChatCompletionChoicesFinishReasonToolCalls,
		}},
		Usage: openai.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}

	detailedResp = &openai.ChatCompletionResponse{
		ID:                "chatcmpl-Bx5kNovDsMvLVkXYomgZvfV95lhEd",
		Object:            "chat.completion",
		Created:           openai.JSONUNIXTime(time.Unix(1753423143, 0)),
		Model:             "gpt-4.1-nano-2025-04-14",
		ServiceTier:       "default",
		SystemFingerprint: "fp_38343a2f8f",
		Choices: []openai.ChatCompletionResponseChoice{{
			Index: 0,
			Message: openai.ChatCompletionResponseChoiceMessage{
				Role:    "assistant",
				Content: ptr("Hello! How can I assist you today?"),
			},
			FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
		}},
		Usage: openai.Usage{
			PromptTokens:     9,
			CompletionTokens: 9,
			TotalTokens:      18,
			PromptTokensDetails: &openai.PromptTokensDetails{
				AudioTokens:  0,
				CachedTokens: 0,
			},
			CompletionTokensDetails: &openai.CompletionTokensDetails{
				AcceptedPredictionTokens: 0,
				AudioTokens:              0,
				ReasoningTokens:          0,
				RejectedPredictionTokens: 0,
			},
		},
	}
)

var (
	embeddingsResp = &openai.EmbeddingResponse{
		Object: "list",
		Data: []openai.Embedding{
			{
				Object: "embedding",
				Embedding: openai.EmbeddingUnion{
					Value: []float64{0.1, -0.2, 0.3},
				},
				Index: 0,
			},
		},
		Model: "text-embedding-3-small",
		Usage: openai.EmbeddingUsage{
			PromptTokens: 5,
			TotalTokens:  5,
		},
	}

	embeddingsBase64Resp = &openai.EmbeddingResponse{
		Object: "list",
		Data: []openai.Embedding{
			{
				Object: "embedding",
				// Base64 encoding of two float32 values: 0.5 and -0.5 in little-endian
				// 0.5 in float32 = 0x3f000000, -0.5 in float32 = 0xbf000000
				Embedding: openai.EmbeddingUnion{
					Value: "AAAAPwAAAL8=", // base64 of [0x00, 0x00, 0x00, 0x3f, 0x00, 0x00, 0x00, 0xbf]
				},
				Index: 0,
			},
		},
		Model: "text-embedding-3-small",
		Usage: openai.EmbeddingUsage{
			PromptTokens: 3,
			TotalTokens:  3,
		},
	}
)

func TestBuildResponseAttributes(t *testing.T) {
	tests := []struct {
		name          string
		resp          *openai.ChatCompletionResponse
		expectedAttrs []attribute.KeyValue
	}{
		{
			name: "successful response",
			resp: basicResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.OutputMessageAttribute(0, openinference.MessageRole), "assistant"),
				attribute.String(openinference.OutputMessageAttribute(0, openinference.MessageContent), "Hello! How can I help you today?"),
				attribute.Int(openinference.LLMTokenCountPrompt, 20),
				attribute.Int(openinference.LLMTokenCountCompletion, 10),
				attribute.Int(openinference.LLMTokenCountTotal, 30),
			},
		},
		{
			name: "response with tool calls",
			resp: toolsResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, openai.ModelGPT5Nano),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.OutputMessageAttribute(0, openinference.MessageRole), "assistant"),
				attribute.String(openinference.OutputMessageAttribute(0, openinference.MessageContent), "I can help you with that."),
				attribute.String(openinference.OutputMessageToolCallAttribute(0, 0, openinference.ToolCallID), "call_123"),
				attribute.String(openinference.OutputMessageToolCallAttribute(0, 0, openinference.ToolCallFunctionName), "get_weather"),
				attribute.String(openinference.OutputMessageToolCallAttribute(0, 0, openinference.ToolCallFunctionArguments), `{"location":"NYC"}`),
				attribute.Int(openinference.LLMTokenCountPrompt, 10),
				attribute.Int(openinference.LLMTokenCountCompletion, 20),
				attribute.Int(openinference.LLMTokenCountTotal, 30),
			},
		},
		{
			name: "response with detailed usage",
			resp: detailedResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.LLMModelName, "gpt-4.1-nano-2025-04-14"),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.OutputMessageAttribute(0, openinference.MessageRole), "assistant"),
				attribute.String(openinference.OutputMessageAttribute(0, openinference.MessageContent), "Hello! How can I assist you today?"),
				attribute.Int(openinference.LLMTokenCountPrompt, 9),
				attribute.Int(openinference.LLMTokenCountPromptAudio, 0),
				attribute.Int(openinference.LLMTokenCountPromptCacheHit, 0),
				attribute.Int(openinference.LLMTokenCountCompletion, 9),
				attribute.Int(openinference.LLMTokenCountCompletionAudio, 0),
				attribute.Int(openinference.LLMTokenCountCompletionReasoning, 0),
				attribute.Int(openinference.LLMTokenCountTotal, 18),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildResponseAttributes(tt.resp, openinference.NewTraceConfig())

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, attrs)
		})
	}
}

func TestBuildEmbeddingsResponseAttributes(t *testing.T) {
	tests := []struct {
		name          string
		resp          *openai.EmbeddingResponse
		config        *openinference.TraceConfig
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:   "successful embeddings response with float vectors",
			resp:   embeddingsResp,
			config: openinference.NewTraceConfig(),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.EmbeddingModelName, "text-embedding-3-small"),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.Float64Slice(openinference.EmbeddingVectorAttribute(0), []float64{0.1, -0.2, 0.3}),
				attribute.Int(openinference.LLMTokenCountPrompt, 5),
				attribute.Int(openinference.LLMTokenCountTotal, 5),
			},
		},
		{
			name:   "embeddings response with base64 vectors",
			resp:   embeddingsBase64Resp,
			config: openinference.NewTraceConfig(),
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.EmbeddingModelName, "text-embedding-3-small"),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.Float64Slice(openinference.EmbeddingVectorAttribute(0), []float64{0.5, -0.5}),
				attribute.Int(openinference.LLMTokenCountPrompt, 3),
				attribute.Int(openinference.LLMTokenCountTotal, 3),
			},
		},
		{
			name: "hide embeddings vectors",
			resp: embeddingsResp,
			config: &openinference.TraceConfig{
				HideEmbeddingsVectors: true,
			},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.EmbeddingModelName, "text-embedding-3-small"),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.Int(openinference.LLMTokenCountPrompt, 5),
				attribute.Int(openinference.LLMTokenCountTotal, 5),
			},
		},
		{
			name: "hide outputs",
			resp: embeddingsResp,
			config: &openinference.TraceConfig{
				HideOutputs: true,
			},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.EmbeddingModelName, "text-embedding-3-small"),
				attribute.Int(openinference.LLMTokenCountPrompt, 5),
				attribute.Int(openinference.LLMTokenCountTotal, 5),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildEmbeddingsResponseAttributes(tt.resp, tt.config)

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, attrs)
		})
	}
}

func TestDecodeBase64Embeddings(t *testing.T) {
	tests := []struct {
		name           string
		encoded        string
		expectedFloats []float64
		expectError    bool
	}{
		{
			name: "decode two float32 values",
			// 0.5 in float32 = 0x3f000000, -0.5 in float32 = 0xbf000000 (little-endian)
			encoded:        "AAAAPwAAAL8=",
			expectedFloats: []float64{0.5, -0.5},
			expectError:    false,
		},
		{
			name: "decode single float32 value",
			// 1.0 in float32 = 0x3f800000 (little-endian)
			encoded:        "AACAPw==",
			expectedFloats: []float64{1.0},
			expectError:    false,
		},
		{
			name:           "invalid base64",
			encoded:        "not-valid-base64!@#",
			expectedFloats: nil,
			expectError:    true,
		},
		{
			name:           "empty string",
			encoded:        "",
			expectedFloats: []float64{},
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			floats, err := decodeBase64Embeddings(tt.encoded)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(floats) != len(tt.expectedFloats) {
				t.Errorf("expected %d floats, got %d", len(tt.expectedFloats), len(floats))
				return
			}

			for i, expected := range tt.expectedFloats {
				if floats[i] != expected {
					t.Errorf("float[%d]: expected %f, got %f", i, expected, floats[i])
				}
			}
		})
	}
}
