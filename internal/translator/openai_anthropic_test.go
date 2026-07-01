// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	// Define a common input request to use across subtests.
	openAIReq := &openai.ChatCompletionRequest{
		Model: claudeTestModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{Content: openai.ContentUnion{Value: "You are a helpful assistant."}, Role: openai.ChatMessageRoleSystem},
			},
			{
				OfUser: &openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"}, Role: openai.ChatMessageRoleUser},
			},
		},
		MaxTokens:   ptr.To(int64(1024)),
		Temperature: ptr.To(0.7),
	}

	t.Run("Basic request - path, model in body, no anthropic_version", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		hm, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, body)

		// Check the path header.
		pathHeader := hm[0]
		require.Equal(t, pathHeaderName, pathHeader.Key())
		require.Equal(t, "/v1/messages", pathHeader.Value())

		// The anthropic-version header must be present (defaults to the SDK-pinned version).
		var foundVersion bool
		for _, h := range hm {
			if h.Key() == anthropicVersionHeaderName {
				foundVersion = true
				require.Equal(t, anthropicDefaultVersion, h.Value())
			}
		}
		require.True(t, foundVersion, "anthropic-version header should be present")

		// Model MUST be present in the body for first-party Anthropic.
		require.True(t, gjson.GetBytes(body, "model").Exists())
		require.Equal(t, claudeTestModel, gjson.GetBytes(body, "model").String())

		// First-party Anthropic must NOT have anthropic_version in the body (that's a GCP/Bedrock thing).
		require.False(t, gjson.GetBytes(body, "anthropic_version").Exists(), "body should not contain anthropic_version")

		// Messages/system structure.
		require.Equal(t, "You are a helpful assistant.", gjson.GetBytes(body, "system.0.text").String())
		require.Equal(t, "Hello!", gjson.GetBytes(body, "messages.0.content.0.text").String())
		require.Equal(t, int64(1024), gjson.GetBytes(body, "max_tokens").Int())
	})

	t.Run("API version override sets header", func(t *testing.T) {
		customVersion := "2024-01-01"
		translator := NewChatCompletionOpenAIToAnthropicTranslator(customVersion, "")
		hm, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, body)

		var foundVersion bool
		for _, h := range hm {
			if h.Key() == anthropicVersionHeaderName {
				foundVersion = true
				require.Equal(t, customVersion, h.Value())
			}
		}
		require.True(t, foundVersion, "anthropic-version header should reflect override")

		// The version is a header, not a body key.
		require.False(t, gjson.GetBytes(body, "anthropic_version").Exists())
	})

	t.Run("Model name override - used in body", func(t *testing.T) {
		overrideModelName := "claude-3-opus-override"
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", overrideModelName)

		_, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		// The model in the body should be the override.
		require.Equal(t, overrideModelName, gjson.GetBytes(body, "model").String())
	})

	t.Run("Streaming request - stream:true and streamParser init", func(t *testing.T) {
		streamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		hm, body, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)

		require.True(t, gjson.GetBytes(body, "stream").Bool(), `body should contain "stream": true`)
		require.Equal(t, claudeTestModel, gjson.GetBytes(body, "model").String())

		// The stream parser should have been initialized.
		o := translator.(*openAIToAnthropicTranslatorV1ChatCompletion)
		require.NotNil(t, o.streamParser)
	})

	t.Run("Image content request", func(t *testing.T) {
		imageReq := &openai.ChatCompletionRequest{
			MaxCompletionTokens: ptr.To(int64(200)),
			Model:               "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{
							Value: []openai.ChatCompletionContentPartUserUnionParam{
								{OfText: &openai.ChatCompletionContentPartTextParam{Text: "What is in this image?"}},
								{OfImageURL: &openai.ChatCompletionContentPartImageParam{
									ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
										URL: "data:image/jpeg;base64,dGVzdA==", // "test" in base64.
									},
								}},
							},
						},
						Role: openai.ChatMessageRoleUser,
					},
				},
			},
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, imageReq, false)
		require.NoError(t, err)

		imageBlock := gjson.GetBytes(body, "messages.0.content.1")
		require.Equal(t, "image", imageBlock.Get("type").String())
		require.Equal(t, "base64", imageBlock.Get("source.type").String())
		require.Equal(t, "image/jpeg", imageBlock.Get("source.media_type").String())
		require.Equal(t, "dGVzdA==", imageBlock.Get("source.data").String())
	})

	t.Run("Multiple system prompts concatenated", func(t *testing.T) {
		firstMsg := "First system prompt."
		secondMsg := "Second developer prompt."
		thirdMsg := "Hello!"
		multiSystemReq := &openai.ChatCompletionRequest{
			Model: claudeTestModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfSystem: &openai.ChatCompletionSystemMessageParam{Content: openai.ContentUnion{Value: firstMsg}, Role: openai.ChatMessageRoleSystem}},
				{OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{Content: openai.ContentUnion{Value: secondMsg}, Role: openai.ChatMessageRoleDeveloper}},
				{OfUser: &openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: thirdMsg}, Role: openai.ChatMessageRoleUser}},
			},
			MaxTokens: ptr.To(int64(100)),
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, multiSystemReq, false)
		require.NoError(t, err)

		require.Equal(t, firstMsg, gjson.GetBytes(body, "system.0.text").String())
		require.Equal(t, secondMsg, gjson.GetBytes(body, "system.1.text").String())
		require.Equal(t, thirdMsg, gjson.GetBytes(body, "messages.0.content.0.text").String())
	})

	t.Run("Structured outputs - output_config present (isGCPBackend=false)", func(t *testing.T) {
		// Use a model that supports structured outputs.
		structuredReq := &openai.ChatCompletionRequest{
			Model:     "claude-opus-4-5",
			MaxTokens: ptr.To(int64(100)),
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfUser: &openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: "Return JSON"}, Role: openai.ChatMessageRoleUser}},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
				OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
					Type: "json_schema",
					JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
						Name:   "math_response",
						Schema: []byte(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`),
						Strict: true,
					},
				},
			},
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, structuredReq, false)
		require.NoError(t, err)

		// output_config should be present, proving isGCPBackend=false (structured outputs ON).
		outputConfig := gjson.GetBytes(body, "output_config")
		require.True(t, outputConfig.Exists(), "output_config should be present for first-party Anthropic")
		require.Equal(t, "json_schema", outputConfig.Get("format.type").String())
	})

	t.Run("reasoning_effort -> output_config.effort", func(t *testing.T) {
		// Use a model that supports effort.
		effortReq := &openai.ChatCompletionRequest{
			Model:           "claude-opus-4-5",
			MaxTokens:       ptr.To(int64(100)),
			ReasoningEffort: openai.ReasoningEffortHigh,
			Messages: []openai.ChatCompletionMessageParamUnion{
				{OfUser: &openai.ChatCompletionUserMessageParam{Content: openai.StringOrUserRoleContentUnion{Value: "Think hard"}, Role: openai.ChatMessageRoleUser}},
			},
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, effortReq, false)
		require.NoError(t, err)

		effort := gjson.GetBytes(body, "output_config.effort")
		require.True(t, effort.Exists(), "output_config.effort should be present")
		require.Equal(t, "high", effort.String())
	})

	t.Run("Invalid temperature (above bound)", func(t *testing.T) {
		invalidTempReq := &openai.ChatCompletionRequest{
			Model:       claudeTestModel,
			Messages:    []openai.ChatCompletionMessageParamUnion{},
			MaxTokens:   ptr.To(int64(100)),
			Temperature: ptr.To(2.5),
		}
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, _, err := translator.RequestBody(nil, invalidTempReq, false)
		require.ErrorIs(t, err, internalapi.ErrInvalidRequestBody)
	})

	t.Run("content-length header matches body", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		hm, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		var contentLength string
		for _, h := range hm {
			if h.Key() == contentLengthHeaderName {
				contentLength = h.Value()
			}
		}
		require.Equal(t, strconv.Itoa(len(body)), contentLength)
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	t.Run("invalid json body", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, _, _, _, err := translator.ResponseBody(map[string]string{statusHeaderName: "200"}, bytes.NewBufferString("invalid json"), true, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal body")
	})

	tests := []struct {
		name                   string
		inputResponse          *anthropic.Message
		respHeaders            map[string]string
		expectedOpenAIResponse openai.ChatCompletionResponse
	}{
		{
			name: "basic text response",
			inputResponse: &anthropic.Message{
				ID:         "msg_01XYZ123",
				Model:      "claude-3-5-sonnet-20241022",
				Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content:    []anthropic.ContentBlockUnion{{Type: "text", Text: "Hello there!"}},
				StopReason: anthropic.StopReasonEndTurn,
				Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 5, CacheCreationInputTokens: 3},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ123",
				Model:   "claude-3-5-sonnet-20241022",
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens:     18,
					CompletionTokens: 20,
					TotalTokens:      38,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens:        5,
						CacheCreationTokens: 3,
					},
					CompletionTokensDetails: &openai.CompletionTokensDetails{},
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index:        0,
						Message:      openai.ChatCompletionResponseChoiceMessage{Role: "assistant", Content: ptr.To("Hello there!")},
						FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					},
				},
			},
		},
		{
			name: "response with tool use",
			inputResponse: &anthropic.Message{
				ID:    "msg_01XYZ123",
				Model: "claude-3-5-sonnet-20241022",
				Role:  constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content: []anthropic.ContentBlockUnion{
					{Type: "text", Text: "Ok, I will call the tool."},
					{Type: "tool_use", ID: "toolu_01", Name: "get_weather", Input: []byte(`{"location":"Tokyo","unit":"celsius"}`)},
				},
				StopReason: anthropic.StopReasonToolUse,
				Usage:      anthropic.Usage{InputTokens: 25, OutputTokens: 15, CacheReadInputTokens: 10, CacheCreationInputTokens: 7},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ123",
				Model:   "claude-3-5-sonnet-20241022",
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens: 42, CompletionTokens: 15, TotalTokens: 57,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens:        10,
						CacheCreationTokens: 7,
					},
					CompletionTokensDetails: &openai.CompletionTokensDetails{},
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: openai.ChatCompletionChoicesFinishReasonToolCalls,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Role:    string(anthropic.MessageParamRoleAssistant),
							Content: ptr.To("Ok, I will call the tool."),
							ToolCalls: []openai.ChatCompletionMessageToolCallParam{
								{
									ID:   ptr.To("toolu_01"),
									Type: openai.ChatCompletionMessageToolCallTypeFunction,
									Function: openai.ChatCompletionMessageToolCallFunctionParam{
										Name:      "get_weather",
										Arguments: `{"location":"Tokyo","unit":"celsius"}`,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "model fallback to request model when response has none",
			inputResponse: &anthropic.Message{
				ID:         "msg_01XYZ123",
				Model:      "",
				Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
				Content:    []anthropic.ContentBlockUnion{{Type: "text", Text: "No model in response."}},
				StopReason: anthropic.StopReasonEndTurn,
				Usage:      anthropic.Usage{InputTokens: 8, OutputTokens: 12},
			},
			respHeaders: map[string]string{statusHeaderName: "200"},
			expectedOpenAIResponse: openai.ChatCompletionResponse{
				ID:      "msg_01XYZ123",
				Model:   claudeTestModel,
				Created: openai.JSONUNIXTime(time.Unix(releaseDateUnix, 0)),
				Object:  "chat.completion",
				Usage: openai.Usage{
					PromptTokens:     8,
					CompletionTokens: 12,
					TotalTokens:      20,
					PromptTokensDetails: &openai.PromptTokensDetails{
						CachedTokens:        0,
						CacheCreationTokens: 0,
					},
					CompletionTokensDetails: &openai.CompletionTokensDetails{},
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index:        0,
						Message:      openai.ChatCompletionResponseChoiceMessage{Role: "assistant", Content: ptr.To("No model in response.")},
						FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.inputResponse)
			require.NoError(t, err, "Test setup failed: could not marshal input struct")

			translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
			// Seed the request model so the fallback case has a value to use.
			translator.(*openAIToAnthropicTranslatorV1ChatCompletion).requestModel = claudeTestModel
			hm, body, usedToken, responseModel, err := translator.ResponseBody(tt.respHeaders, bytes.NewBuffer(body), true, nil)

			require.NoError(t, err, "Translator returned an unexpected internal error")
			require.NotNil(t, hm)
			require.NotNil(t, body)

			require.Len(t, hm, 1)
			require.Equal(t, contentLengthHeaderName, hm[0].Key())
			require.Equal(t, strconv.Itoa(len(body)), hm[0].Value())

			// The response model should be the response's model (or the request model for the fallback case).
			if tt.inputResponse.Model != "" {
				require.Equal(t, tt.inputResponse.Model, responseModel)
			} else {
				require.Equal(t, claudeTestModel, responseModel)
			}

			var gotResp openai.ChatCompletionResponse
			err = json.Unmarshal(body, &gotResp)
			require.NoError(t, err)

			expectedTokenUsage := tokenUsageFrom(
				int32(tt.expectedOpenAIResponse.Usage.PromptTokens),                            //nolint:gosec
				int32(tt.expectedOpenAIResponse.Usage.PromptTokensDetails.CachedTokens),        //nolint:gosec
				int32(tt.expectedOpenAIResponse.Usage.PromptTokensDetails.CacheCreationTokens), //nolint:gosec
				int32(tt.expectedOpenAIResponse.Usage.CompletionTokens),                        //nolint:gosec
				int32(tt.expectedOpenAIResponse.Usage.TotalTokens),                             //nolint:gosec
				int32(tt.expectedOpenAIResponse.Usage.CompletionTokensDetails.ReasoningTokens), //nolint:gosec
			)
			require.Equal(t, expectedTokenUsage, usedToken)

			if diff := cmp.Diff(tt.expectedOpenAIResponse, gotResp, cmpopts.IgnoreFields(openai.ChatCompletionResponse{}, "Created")); diff != "" {
				t.Errorf("ResponseBody mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("streaming - Anthropic SSE to OpenAI chunks", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		streamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		_, _, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)

		anthropicSSE := `event: message_start
data: {"type": "message_start", "message": {"id": "msg_123", "usage": {"input_tokens": 15, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 10, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}

event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text":" world"}}

event: content_block_stop
data: {"type": "content_block_stop", "index": 0}

event: message_delta
data: {"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"input_tokens": 15, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 10, "output_tokens": 12}}

event: message_stop
data: {"type": "message_stop"}
`
		hm, body, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewBufferString(anthropicSSE), true, nil)
		require.NoError(t, err)
		require.NotNil(t, body)
		require.Equal(t, claudeTestModel, responseModel)

		// The event-stream content-type is set by ResponseHeaders, not ResponseBody.
		// ResponseBody for streaming returns no headers (the parser emits only body chunks).
		_ = hm

		// Verify ResponseHeaders returns the event-stream content type for streaming.
		hdr, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Len(t, hdr, 1)
		require.Equal(t, contentTypeHeaderName, hdr[0].Key())
		require.Equal(t, eventStreamContentType, hdr[0].Value())

		bodyStr := string(body)

		// Split the SSE output into individual `data: ` chunks and assert their
		// ORDER, rather than loose substring matches. Every emitted chunk is
		// framed as `data: <payload>\n\n`, so splitting on "data: " yields one
		// entry per chunk (the first entry is empty).
		rawChunks := strings.Split(bodyStr, "data: ")
		require.Greater(t, len(rawChunks), 1, "expected at least one data chunk")

		// Collect the non-empty, non-terminator chunks in order.
		var chunks []string
		doneCount := 0
		for _, c := range rawChunks {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if c == "[DONE]" {
				doneCount++
				continue
			}
			chunks = append(chunks, c)
		}

		// Exactly ONE [DONE] terminator — not zero, not two.
		require.Equal(t, 1, doneCount, "expected exactly one data: [DONE] terminator")

		// First emitted chunk carries the assistant role (set on the first
		// content delta by constructOpenAIChatCompletionChunk).
		require.Contains(t, chunks[0], `"role":"assistant"`)
		// Then the content deltas in order: "Hello" before " world".
		helloIdx := -1
		worldIdx := -1
		finishIdx := -1
		for i, c := range chunks {
			if strings.Contains(c, `"content":"Hello"`) {
				helloIdx = i
			}
			if strings.Contains(c, `"content":" world"`) {
				worldIdx = i
			}
			if strings.Contains(c, `"finish_reason":"stop"`) {
				finishIdx = i
			}
		}
		require.GreaterOrEqual(t, helloIdx, 0, "expected a chunk with content 'Hello'")
		require.GreaterOrEqual(t, worldIdx, 0, "expected a chunk with content ' world'")
		require.GreaterOrEqual(t, finishIdx, 0, "expected a chunk with finish_reason 'stop'")
		require.Less(t, helloIdx, worldIdx, "'Hello' chunk must precede ' world' chunk")
		require.Less(t, worldIdx, finishIdx, "content deltas must precede the finish_reason chunk")
		// The finish_reason chunk must precede the [DONE] terminator (it is the
		// last non-terminator chunk or followed only by the usage chunk).
		require.Less(t, finishIdx, len(chunks))

		// Token usage: input_tokens come from message_start, output_tokens from
		// message_delta. Both must be non-zero and match the SSE fixture.
		// InputTokens() sums input + cache_read + cache_creation per the
		// explicit-caching extraction (15 + 10 + 0 = 25).
		inputTokens, ok := tokenUsage.InputTokens()
		require.True(t, ok)
		require.NotZero(t, inputTokens)
		require.Equal(t, uint32(25), inputTokens)
		outputTokens, ok := tokenUsage.OutputTokens()
		require.True(t, ok)
		require.NotZero(t, outputTokens)
		require.Equal(t, uint32(12), outputTokens)
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_ResponseError(t *testing.T) {
	tests := []struct {
		name            string
		responseHeaders map[string]string
		inputBody       any
		expectedOutput  openai.Error
	}{
		{
			name: "non-json error response",
			responseHeaders: map[string]string{
				statusHeaderName:      "503",
				contentTypeHeaderName: "text/plain; charset=utf-8",
			},
			inputBody: "Service Unavailable",
			expectedOutput: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    anthropicBackendError,
					Code:    ptr.To("503"),
					Message: "Service Unavailable",
				},
			},
		},
		{
			name: "json error response",
			responseHeaders: map[string]string{
				statusHeaderName:      "400",
				contentTypeHeaderName: "application/json",
			},
			inputBody: &anthropic.ErrorResponse{
				Type: "error",
				Error: shared.ErrorObjectUnion{
					Type:    "invalid_request_error",
					Message: "Your max_tokens is too high.",
				},
			},
			expectedOutput: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    "invalid_request_error",
					Code:    ptr.To("400"),
					Message: "Your max_tokens is too high.",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reader io.Reader
			if bodyStr, ok := tt.inputBody.(string); ok {
				reader = bytes.NewBufferString(bodyStr)
			} else {
				bodyBytes, err := json.Marshal(tt.inputBody)
				require.NoError(t, err)
				reader = bytes.NewBuffer(bodyBytes)
			}

			o := &openAIToAnthropicTranslatorV1ChatCompletion{}
			hm, body, err := o.ResponseError(tt.responseHeaders, reader)

			require.NoError(t, err)
			require.NotNil(t, body)
			require.NotNil(t, hm)
			require.Len(t, hm, 2)
			require.Equal(t, contentTypeHeaderName, hm[0].Key())
			require.Equal(t, jsonContentType, hm[0].Value()) //nolint:testifylint
			require.Equal(t, contentLengthHeaderName, hm[1].Key())
			require.Equal(t, strconv.Itoa(len(body)), hm[1].Value())

			var gotError openai.Error
			err = json.Unmarshal(body, &gotError)
			require.NoError(t, err)

			if diff := cmp.Diff(tt.expectedOutput, gotError); diff != "" {
				t.Errorf("ResponseError() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_RedactBody(t *testing.T) {
	t.Run("SetRedactionConfig + RedactBody redacts content and tool-call args", func(t *testing.T) {
		o := &openAIToAnthropicTranslatorV1ChatCompletion{}
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		o.SetRedactionConfig(true, true, logger)

		anthropicResp := &anthropic.Message{
			ID:         "msg_01",
			Model:      claudeTestModel,
			Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
			Content:    []anthropic.ContentBlockUnion{{Type: "text", Text: "sensitive content"}},
			StopReason: anthropic.StopReasonToolUse,
			Usage:      anthropic.Usage{InputTokens: 5, OutputTokens: 5},
		}
		body, err := json.Marshal(anthropicResp)
		require.NoError(t, err)

		_, respBody, _, _, err := o.ResponseBody(nil, bytes.NewReader(body), true, nil)
		require.NoError(t, err)
		require.NotNil(t, respBody)

		// The redacted response should have been logged.
		logStr := logBuf.String()
		require.Contains(t, logStr, "response body processing")
		// The sensitive content must NOT appear verbatim in the log output.
		require.NotContains(t, logStr, "sensitive content")
	})

	t.Run("RedactBody direct - redacts content and tool calls", func(t *testing.T) {
		o := &openAIToAnthropicTranslatorV1ChatCompletion{}
		resp := &openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionResponseChoice{
				{
					Message: openai.ChatCompletionResponseChoiceMessage{
						Role:    "assistant",
						Content: ptr.To("top secret"),
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID:   ptr.To("call_1"),
								Type: openai.ChatCompletionMessageToolCallTypeFunction,
								Function: openai.ChatCompletionMessageToolCallFunctionParam{
									Name:      "get_weather",
									Arguments: `{"location":"secret base"}`,
								},
							},
						},
					},
				},
			},
		}
		redacted := o.RedactBody(resp)
		require.NotNil(t, redacted)
		// Original is untouched.
		require.Equal(t, "top secret", *resp.Choices[0].Message.Content)
		// Redacted content is a placeholder.
		require.NotEqual(t, "top secret", *redacted.Choices[0].Message.Content)
		require.Contains(t, *redacted.Choices[0].Message.Content, "[REDACTED")
		// Tool call name is preserved, arguments are redacted.
		require.Equal(t, "get_weather", redacted.Choices[0].Message.ToolCalls[0].Function.Name)
		require.NotEqual(t, `{"location":"secret base"}`, redacted.Choices[0].Message.ToolCalls[0].Function.Arguments)
	})

	t.Run("RedactBody nil", func(t *testing.T) {
		o := &openAIToAnthropicTranslatorV1ChatCompletion{}
		require.Nil(t, o.RedactBody(nil))
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_ResponseHeaders(t *testing.T) {
	t.Run("streaming returns event-stream content type", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		streamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		_, _, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)

		hm, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Len(t, hm, 1)
		require.Equal(t, contentTypeHeaderName, hm[0].Key())
		require.Equal(t, eventStreamContentType, hm[0].Value())
	})

	t.Run("non-streaming returns nil headers", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		nonStreamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
		}
		_, _, err := translator.RequestBody(nil, nonStreamReq, false)
		require.NoError(t, err)

		hm, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Nil(t, hm)
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_Adversarial(t *testing.T) {
	t.Run("empty content does not panic", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		anthropicResp := &anthropic.Message{
			ID:         "msg_empty",
			Model:      claudeTestModel,
			Role:       constant.Assistant(anthropic.MessageParamRoleAssistant),
			Content:    []anthropic.ContentBlockUnion{},
			StopReason: anthropic.StopReasonEndTurn,
			Usage:      anthropic.Usage{InputTokens: 1, OutputTokens: 1},
		}
		body, err := json.Marshal(anthropicResp)
		require.NoError(t, err)

		hm, respBody, _, _, err := translator.ResponseBody(nil, bytes.NewReader(body), true, nil)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, respBody)

		var got openai.ChatCompletionResponse
		require.NoError(t, json.Unmarshal(respBody, &got))
		require.Len(t, got.Choices, 1)
		require.Nil(t, got.Choices[0].Message.Content)
	})

	t.Run("tool_use without text yields finish_reason tool_calls and empty content", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		anthropicResp := &anthropic.Message{
			ID:    "msg_toolonly",
			Model: claudeTestModel,
			Role:  constant.Assistant(anthropic.MessageParamRoleAssistant),
			Content: []anthropic.ContentBlockUnion{
				{Type: "tool_use", ID: "toolu_01", Name: "get_weather", Input: []byte(`{"location":"NYC"}`)},
			},
			StopReason: anthropic.StopReasonToolUse,
			Usage:      anthropic.Usage{InputTokens: 5, OutputTokens: 5},
		}
		body, err := json.Marshal(anthropicResp)
		require.NoError(t, err)

		_, respBody, _, _, err := translator.ResponseBody(nil, bytes.NewReader(body), true, nil)
		require.NoError(t, err)

		var got openai.ChatCompletionResponse
		require.NoError(t, json.Unmarshal(respBody, &got))
		require.Len(t, got.Choices, 1)
		require.Equal(t, openai.ChatCompletionChoicesFinishReasonToolCalls, got.Choices[0].FinishReason)
		require.Len(t, got.Choices[0].Message.ToolCalls, 1)
		require.Equal(t, "get_weather", got.Choices[0].Message.ToolCalls[0].Function.Name)
	})

	t.Run("malformed SSE - truncated data line does not panic", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		streamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		_, _, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)

		// A truncated data line without a terminating \n\n.
		truncated := `event: content_block_delta
data: {"type": "content_block_delta", "index": 0, "delta":`

		require.NotPanics(t, func() {
			_, _, _, _, err := translator.ResponseBody(nil, bytes.NewBufferString(truncated), false, nil)
			// With endOfStream=false the incomplete block stays buffered; no error, no output yet.
			require.NoError(t, err)
		})
	})

	t.Run("malformed SSE - missing double newline buffers gracefully", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		streamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		_, _, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)

		// A complete-looking event but with only a single newline (no event boundary).
		noBoundary := "event: message_start\ndata: {\"type\": \"message_start\", \"message\": {\"id\": \"msg_1\"}}\n"

		// First call with endOfStream=false: the incomplete block stays buffered,
		// no complete event block yet, so no body content emitted.
		require.NotPanics(t, func() {
			hm, body, _, _, err := translator.ResponseBody(nil, bytes.NewBufferString(noBoundary), false, nil)
			require.NoError(t, err)
			_ = hm
			if body != nil {
				require.Empty(t, string(body))
			}
		})

		// Second call: complete the buffered event by appending the missing
		// newline that forms the `\n\n` boundary. With endOfStream=true the
		// previously-buffered event MUST now be emitted (not silently dropped),
		// proving the parser buffers correctly across calls.
		require.NotPanics(t, func() {
			_, body, _, _, err := translator.ResponseBody(nil, bytes.NewBufferString("\n"), true, nil)
			require.NoError(t, err)
			require.NotNil(t, body)
			// message_start does not emit an OpenAI chunk, but endOfStream=true
			// forces the final usage chunk and [DONE] terminator, which can only
			// happen if the buffered event was consumed rather than dropped.
			bodyStr := string(body)
			require.Contains(t, bodyStr, "data: [DONE]")
		})
	})

	t.Run("streaming with endOfStream flushes buffered partial event without panic", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		streamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		_, _, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)

		// Send a complete event followed by a partial one, then signal end of stream.
		partial := fmt.Sprintf("event: content_block_delta\ndata: {\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"hi\"}}\n\nevent: message_stop\ndata: %s",
			strings.Repeat("x", 10))

		require.NotPanics(t, func() {
			_, _, _, _, err := translator.ResponseBody(nil, bytes.NewBufferString(partial), true, nil)
			// endOfStream=true; the parser should not panic. The partial trailing block is dropped.
			_ = err
		})
	})

	t.Run("mid-stream error event is surfaced as an error", func(t *testing.T) {
		// The anthropicStreamParser handles an SSE `error` event by returning
		// an error (see anthropic_helper.go handleAnthropicStreamEvent "error"
		// case): `anthropic stream error: <type> - <message>`. This test feeds
		// an error event mid-stream (after a content_block_start but before
		// content_block_stop) and asserts that error is surfaced — not silently
		// swallowed or turned into a terminal chunk.
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		streamReq := &openai.ChatCompletionRequest{
			Model:     claudeTestModel,
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
			Stream:    true,
		}
		_, _, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)

		anthropicSSE := `event: message_start
data: {"type": "message_start", "message": {"id": "msg_err", "usage": {"input_tokens": 5, "output_tokens": 1}}}

event: content_block_start
data: {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}

event: error
data: {"type": "error", "error": {"type": "overloaded_error", "message": "Overloaded"}}

`
		_, _, _, _, err = translator.ResponseBody(nil, bytes.NewBufferString(anthropicSSE), true, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "anthropic stream error")
		require.Contains(t, err.Error(), "overloaded_error")
		require.Contains(t, err.Error(), "Overloaded")
	})
}
