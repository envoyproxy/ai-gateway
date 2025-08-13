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
		Model:   openai.ModelGPT41Nano,
		Choices: []openai.ChatCompletionResponseChoice{{
			Index: 0,
			Message: openai.ChatCompletionResponseChoiceMessage{
				Role:    "assistant",
				Content: ptr("Hello! How can I help you today?"),
			},
			FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
		}},
		Usage: openai.ChatCompletionResponseUsage{
			PromptTokens:     20,
			CompletionTokens: 10,
			TotalTokens:      30,
		},
	}
	basicRespBody = mustJSON(basicResp)

	toolsResp = &openai.ChatCompletionResponse{
		ID:    "chatcmpl-123",
		Model: openai.ModelGPT41Nano,
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
		Usage: openai.ChatCompletionResponseUsage{
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
		Usage: openai.ChatCompletionResponseUsage{
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
				attribute.String(openinference.LLMModelName, openai.ModelGPT41Nano),
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
				attribute.String(openinference.LLMModelName, openai.ModelGPT41Nano),
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
