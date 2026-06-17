// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/base64"
	stdjson "encoding/json" // nolint: depguard
	"fmt"
	"log/slog"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"k8s.io/utils/ptr"

	anthropicSchema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestResponseModel_Anthropic(t *testing.T) {
	modelName := "claude-3-opus-20240229"
	translator := NewChatCompletionOpenAIToAnthropicTranslator("", modelName)

	req := &openai.ChatCompletionRequest{
		Model:     "claude-3-opus",
		MaxTokens: ptr.To(int64(100)),
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
					Role:    openai.ChatMessageRoleUser,
				},
			},
		},
	}
	reqBody, _ := json.Marshal(req)
	_, _, err := translator.RequestBody(reqBody, req, false)
	require.NoError(t, err)

	anthropicResponse := anthropic.Message{
		ID:    "msg_01XYZ",
		Type:  constant.ValueOf[constant.Message](),
		Role:  constant.ValueOf[constant.Assistant](),
		Model: modelName,
		Content: []anthropic.ContentBlockUnion{
			{
				Type: "text",
				Text: "Hello!",
			},
		},
		StopReason: anthropic.StopReasonEndTurn,
		Usage: anthropic.Usage{
			InputTokens:  10,
			OutputTokens: 5,
		},
	}

	body, err := json.Marshal(anthropicResponse)
	require.NoError(t, err)

	_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(body), true, nil)
	require.NoError(t, err)
	require.Equal(t, modelName, responseModel)
	inputTokens, ok := tokenUsage.InputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(10), inputTokens)
	outputTokens, ok := tokenUsage.OutputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(5), outputTokens)
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	openAIReq := &openai.ChatCompletionRequest{
		Model: "claude-3-opus-20240229",
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ContentUnion{Value: "You are a helpful assistant."},
					Role:    openai.ChatMessageRoleSystem,
				},
			},
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{Value: "Hello!"},
					Role:    openai.ChatMessageRoleUser,
				},
			},
		},
		MaxTokens:   ptr.To(int64(1024)),
		Temperature: ptr.To(0.7),
	}

	t.Run("Direct Anthropic API Configuration", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		hm, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, body)

		pathHeader := hm[0]
		require.Equal(t, pathHeaderName, pathHeader.Key())
		require.Equal(t, "/v1/messages", pathHeader.Value())

		require.NotNil(t, body)
		require.Equal(t, openAIReq.Model, gjson.GetBytes(body, "model").String())
		require.Equal(t, anthropicVersionHeaderName, hm[1].Key())
		require.Equal(t, anthropicDefaultVersion, hm[1].Value())
		require.False(t, gjson.GetBytes(body, "anthropic_version").Exists())
	})

	t.Run("Model Name Override", func(t *testing.T) {
		overrideModelName := "claude-3-5-sonnet-20241022"
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", overrideModelName)

		hm, body, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)

		require.Equal(t, overrideModelName, gjson.GetBytes(body, "model").String())

		pathHeader := hm[0]
		require.Equal(t, "/v1/messages", pathHeader.Value())
	})

	t.Run("Custom Prefix", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("gateway/v1", "")

		hm, _, err := translator.RequestBody(nil, openAIReq, false)
		require.NoError(t, err)

		pathHeader := hm[0]
		require.Equal(t, pathHeaderName, pathHeader.Key())
		require.Equal(t, "/gateway/v1/messages", pathHeader.Value())
	})

	t.Run("Image Content Request", func(t *testing.T) {
		imageData := base64.StdEncoding.EncodeToString([]byte("fake-image-data"))
		imageReq := &openai.ChatCompletionRequest{
			Model:     "claude-3-opus-20240229",
			MaxTokens: ptr.To(int64(1024)),
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{
							Value: []openai.ChatCompletionContentPartUserUnionParam{
								{
									OfText: &openai.ChatCompletionContentPartTextParam{
										Type: "text",
										Text: "What's in this image?",
									},
								},
								{
									OfImageURL: &openai.ChatCompletionContentPartImageParam{
										Type: openai.ChatCompletionContentPartImageTypeImageURL,
										ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
											URL: fmt.Sprintf("data:image/jpeg;base64,%s", imageData),
										},
									},
								},
							},
						},
					},
				},
			},
		}

		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, imageReq, false)
		require.NoError(t, err)

		messages := gjson.GetBytes(body, "messages").Array()
		require.Len(t, messages, 1)

		content := messages[0].Get("content").Array()
		require.Len(t, content, 2)

		require.Equal(t, "text", content[0].Get("type").String())
		require.Equal(t, "What's in this image?", content[0].Get("text").String())

		require.Equal(t, "image", content[1].Get("type").String())
		require.Equal(t, "image/jpeg", content[1].Get("source.media_type").String())
		require.Equal(t, imageData, content[1].Get("source.data").String())
	})

	t.Run("Streaming Request", func(t *testing.T) {
		streamReq := &openai.ChatCompletionRequest{
			Model:  "claude-3-opus-20240229",
			Stream: true,
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
						Role:    openai.ChatMessageRoleUser,
					},
				},
			},
			MaxTokens: ptr.To(int64(100)),
		}

		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "").(*openAIToAnthropicTranslatorV1ChatCompletion)
		_, body, err := translator.RequestBody(nil, streamReq, false)
		require.NoError(t, err)

		require.NotNil(t, translator.streamParser)

		require.True(t, gjson.GetBytes(body, "stream").Bool())
	})

	t.Run("Tool Use Request", func(t *testing.T) {
		toolReq := &openai.ChatCompletionRequest{
			Model: "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "What's the weather in NYC?"},
						Role:    openai.ChatMessageRoleUser,
					},
				},
			},
			MaxTokens: ptr.To(int64(1024)),
			Tools: []openai.Tool{
				{
					Type: openai.ToolTypeFunction,
					Function: &openai.FunctionDefinition{
						Name:        "get_weather",
						Description: "Get the current weather",
						Parameters: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"location": map[string]any{
									"type":        "string",
									"description": "The city name",
								},
							},
							"required": []string{"location"},
						},
					},
				},
			},
		}

		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		_, body, err := translator.RequestBody(nil, toolReq, false)
		require.NoError(t, err)

		tools := gjson.GetBytes(body, "tools").Array()
		require.Len(t, tools, 1)
		require.Equal(t, "get_weather", tools[0].Get("name").String())
		require.Equal(t, "Get the current weather", tools[0].Get("description").String())
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	t.Run("Non-Streaming Response", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "claude-3-opus-20240229")

		req := &openai.ChatCompletionRequest{
			Model:     "claude-3-opus-20240229",
			MaxTokens: ptr.To(int64(100)),
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
						Role:    openai.ChatMessageRoleUser,
					},
				},
			},
		}
		_, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		anthropicResp := anthropic.Message{
			ID:    "msg_01XYZ",
			Type:  constant.ValueOf[constant.Message](),
			Role:  constant.ValueOf[constant.Assistant](),
			Model: "claude-3-opus-20240229",
			Content: []anthropic.ContentBlockUnion{
				{
					Type: "text",
					Text: "Hello! How can I help you today?",
				},
			},
			StopReason: anthropic.StopReasonEndTurn,
			Usage: anthropic.Usage{
				InputTokens:  10,
				OutputTokens: 12,
			},
		}

		body, err := json.Marshal(anthropicResp)
		require.NoError(t, err)

		headers, newBody, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(body), true, nil)
		require.NoError(t, err)
		require.NotNil(t, newBody)
		require.Equal(t, "claude-3-opus-20240229", responseModel)

		var openAIResp openai.ChatCompletionResponse
		err = json.Unmarshal(newBody, &openAIResp)
		require.NoError(t, err)

		require.Equal(t, "msg_01XYZ", openAIResp.ID)
		require.Equal(t, "claude-3-opus-20240229", openAIResp.Model)
		require.Len(t, openAIResp.Choices, 1)
		require.Equal(t, "Hello! How can I help you today?", *openAIResp.Choices[0].Message.Content)
		require.Equal(t, openai.ChatCompletionChoicesFinishReasonStop, openAIResp.Choices[0].FinishReason)

		inputTokens, ok := tokenUsage.InputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(10), inputTokens)
		outputTokens, ok := tokenUsage.OutputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(12), outputTokens)

		require.Len(t, headers, 1)
		require.Equal(t, contentLengthHeaderName, headers[0].Key())
	})

	t.Run("Tool Use Response", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "claude-3-opus-20240229")

		req := &openai.ChatCompletionRequest{
			Model:     "claude-3-opus-20240229",
			MaxTokens: ptr.To(int64(100)),
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "What's the weather?"},
						Role:    openai.ChatMessageRoleUser,
					},
				},
			},
		}
		_, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		anthropicResp := anthropic.Message{
			ID:    "msg_tool",
			Type:  constant.ValueOf[constant.Message](),
			Role:  constant.ValueOf[constant.Assistant](),
			Model: "claude-3-opus-20240229",
			Content: []anthropic.ContentBlockUnion{
				{
					Type:  "tool_use",
					ID:    "toolu_123",
					Name:  "get_weather",
					Input: stdjson.RawMessage(`{"location": "New York"}`),
				},
			},
			StopReason: anthropic.StopReasonToolUse,
			Usage: anthropic.Usage{
				InputTokens:  15,
				OutputTokens: 20,
			},
		}

		body, err := json.Marshal(anthropicResp)
		require.NoError(t, err)

		_, newBody, _, _, err := translator.ResponseBody(nil, bytes.NewReader(body), true, nil)
		require.NoError(t, err)

		var openAIResp openai.ChatCompletionResponse
		err = json.Unmarshal(newBody, &openAIResp)
		require.NoError(t, err)

		require.Len(t, openAIResp.Choices, 1)
		require.Equal(t, openai.ChatCompletionChoicesFinishReasonToolCalls, openAIResp.Choices[0].FinishReason)
		require.Len(t, openAIResp.Choices[0].Message.ToolCalls, 1)
		require.NotNil(t, openAIResp.Choices[0].Message.ToolCalls[0].ID)
		require.Equal(t, "toolu_123", *openAIResp.Choices[0].Message.ToolCalls[0].ID)
		require.Equal(t, "get_weather", openAIResp.Choices[0].Message.ToolCalls[0].Function.Name)
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_ResponseError(t *testing.T) {
	translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")

	t.Run("JSON Error Response", func(t *testing.T) {
		anthropicError := anthropicSchema.ErrorResponse{
			Type: "error",
			Error: anthropicSchema.ErrorResponseMessage{
				Type:    "invalid_request_error",
				Message: "Invalid API key",
			},
			RequestID: "req_123",
		}

		body, err := json.Marshal(anthropicError)
		require.NoError(t, err)

		headers := map[string]string{
			statusHeaderName:      "401",
			contentTypeHeaderName: "application/json",
		}

		newHeaders, newBody, err := translator.ResponseError(headers, bytes.NewReader(body))
		require.NoError(t, err)
		require.NotNil(t, newBody)

		var openAIErr openai.Error
		err = json.Unmarshal(newBody, &openAIErr)
		require.NoError(t, err)

		require.Equal(t, "error", openAIErr.Type)
		require.Equal(t, "invalid_request_error", openAIErr.Error.Type)
		require.Equal(t, "Invalid API key", openAIErr.Error.Message)
		require.Equal(t, "401", *openAIErr.Error.Code)

		require.Len(t, newHeaders, 2)
		require.Equal(t, contentTypeHeaderName, newHeaders[0].Key())
		require.Equal(t, jsonContentType, newHeaders[0].Value()) //nolint:testifylint
	})

	t.Run("Non-JSON Error Response", func(t *testing.T) {
		errorBody := "Service unavailable"
		headers := map[string]string{
			statusHeaderName:      "503",
			contentTypeHeaderName: "text/plain",
		}

		newHeaders, newBody, err := translator.ResponseError(headers, bytes.NewReader([]byte(errorBody)))
		require.NoError(t, err)
		require.NotNil(t, newBody)

		var openAIErr openai.Error
		err = json.Unmarshal(newBody, &openAIErr)
		require.NoError(t, err)

		require.Equal(t, "error", openAIErr.Type)
		require.Equal(t, anthropicBackendError, openAIErr.Error.Type)
		require.Equal(t, "Service unavailable", openAIErr.Error.Message)
		require.Equal(t, "503", *openAIErr.Error.Code)

		require.Len(t, newHeaders, 2)
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_ResponseHeaders(t *testing.T) {
	t.Run("Non-Streaming", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		headers, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Nil(t, headers)
	})

	t.Run("Streaming", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "").(*openAIToAnthropicTranslatorV1ChatCompletion)

		req := &openai.ChatCompletionRequest{
			Model:     "claude-3-opus-20240229",
			Stream:    true,
			MaxTokens: ptr.To(int64(100)),
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
						Role:    openai.ChatMessageRoleUser,
					},
				},
			},
		}
		_, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		headers, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Len(t, headers, 1)
		require.Equal(t, contentTypeHeaderName, headers[0].Key())
		require.Equal(t, eventStreamContentType, headers[0].Value())
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_Redaction(t *testing.T) {
	translator := NewChatCompletionOpenAIToAnthropicTranslator("", "").(*openAIToAnthropicTranslatorV1ChatCompletion)
	logger := slog.Default()

	t.Run("SetRedactionConfig", func(t *testing.T) {
		translator.SetRedactionConfig(true, true, logger)
		require.True(t, translator.debugLogEnabled)
		require.True(t, translator.enableRedaction)
		require.NotNil(t, translator.logger)
	})

	t.Run("RedactBody", func(t *testing.T) {
		response := &openai.ChatCompletionResponse{
			ID:    "msg_123",
			Model: "claude-3-opus-20240229",
			Choices: []openai.ChatCompletionResponseChoice{
				{
					Index: 0,
					Message: openai.ChatCompletionResponseChoiceMessage{
						Role:    openai.ChatMessageRoleAssistant,
						Content: ptr.To("This is sensitive content"),
					},
					FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
				},
			},
		}

		redacted := translator.RedactBody(response)
		require.NotNil(t, redacted)
		require.Equal(t, "msg_123", redacted.ID) // ID not redacted
		require.NotEqual(t, "This is sensitive content", *redacted.Choices[0].Message.Content)
		require.Contains(t, *redacted.Choices[0].Message.Content, "[REDACTED")
	})

	t.Run("RedactBody with ToolCalls", func(t *testing.T) {
		response := &openai.ChatCompletionResponse{
			ID:    "msg_456",
			Model: "claude-3-opus-20240229",
			Choices: []openai.ChatCompletionResponseChoice{
				{
					Index: 0,
					Message: openai.ChatCompletionResponseChoiceMessage{
						Role: openai.ChatMessageRoleAssistant,
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								ID:   ptr.To("call_123"),
								Type: openai.ChatCompletionMessageToolCallTypeFunction,
								Function: openai.ChatCompletionMessageToolCallFunctionParam{
									Name:      "get_weather",
									Arguments: `{"location": "New York"}`,
								},
							},
						},
					},
					FinishReason: openai.ChatCompletionChoicesFinishReasonToolCalls,
				},
			},
		}

		redacted := translator.RedactBody(response)
		require.NotNil(t, redacted)
		require.Len(t, redacted.Choices[0].Message.ToolCalls, 1)
		require.Equal(t, "get_weather", redacted.Choices[0].Message.ToolCalls[0].Function.Name)
		require.NotEqual(t, `{"location": "New York"}`, redacted.Choices[0].Message.ToolCalls[0].Function.Arguments)
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_Streaming(t *testing.T) {
	translator := NewChatCompletionOpenAIToAnthropicTranslator("", "claude-3-opus-20240229").(*openAIToAnthropicTranslatorV1ChatCompletion)

	req := &openai.ChatCompletionRequest{
		Model:     "claude-3-opus-20240229",
		Stream:    true,
		MaxTokens: ptr.To(int64(100)),
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
					Role:    openai.ChatMessageRoleUser,
				},
			},
		},
	}
	_, _, err := translator.RequestBody(nil, req, false)
	require.NoError(t, err)
	require.NotNil(t, translator.streamParser)

	t.Run("Message Start Event", func(t *testing.T) {
		sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-3-opus-20240229","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

`
		_, body, _, _, err := translator.ResponseBody(nil, bytes.NewReader([]byte(sseData)), false, nil)
		require.NoError(t, err)
		// message_start doesn't produce an OpenAI chunk (it just initializes state)
		// so body should be empty
		require.Empty(t, body)
	})

	t.Run("Content Block Delta", func(t *testing.T) {
		sseData := `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

`
		_, body, _, _, err := translator.ResponseBody(nil, bytes.NewReader([]byte(sseData)), false, nil)
		require.NoError(t, err)
		require.NotNil(t, body)
		require.Contains(t, string(body), "Hello")
	})
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_StreamingResponseModel(t *testing.T) {
	translator := NewChatCompletionOpenAIToAnthropicTranslator("", "").(*openAIToAnthropicTranslatorV1ChatCompletion)
	req := &openai.ChatCompletionRequest{
		Model:     "claude-alias",
		Stream:    true,
		MaxTokens: ptr.To(int64(100)),
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
					Role:    openai.ChatMessageRoleUser,
				},
			},
		},
	}
	_, _, err := translator.RequestBody(nil, req, false)
	require.NoError(t, err)

	messageStart := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-3-opus-20240229","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

`
	_, body, _, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(messageStart)), false, nil)
	require.NoError(t, err)
	require.Empty(t, body)
	require.Equal(t, "claude-3-opus-20240229", responseModel)

	contentDelta := `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

`
	_, body, _, responseModel, err = translator.ResponseBody(nil, bytes.NewReader([]byte(contentDelta)), false, nil)
	require.NoError(t, err)
	require.Contains(t, string(body), `"model":"claude-3-opus-20240229"`)
	require.Equal(t, "claude-3-opus-20240229", responseModel)

	messageStop := `event: message_stop
data: {"type":"message_stop"}

`
	_, body, _, responseModel, err = translator.ResponseBody(nil, bytes.NewReader([]byte(messageStop)), true, nil)
	require.NoError(t, err)
	require.Contains(t, string(body), `"model":"claude-3-opus-20240229"`)
	require.Equal(t, "claude-3-opus-20240229", responseModel)
}

func TestOpenAIToAnthropicTranslatorV1ChatCompletion_EdgeCases(t *testing.T) {
	t.Run("Empty Messages", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		req := &openai.ChatCompletionRequest{
			Model:     "claude-3-opus-20240229",
			Messages:  []openai.ChatCompletionMessageParamUnion{},
			MaxTokens: ptr.To(int64(100)),
		}

		// Should handle empty messages gracefully (Anthropic will reject, but translator shouldn't error)
		_, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
	})

	t.Run("Nil Response", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "").(*openAIToAnthropicTranslatorV1ChatCompletion)
		redacted := translator.RedactBody(nil)
		require.Nil(t, redacted)
	})

	t.Run("Temperature Out of Range", func(t *testing.T) {
		translator := NewChatCompletionOpenAIToAnthropicTranslator("", "")
		req := &openai.ChatCompletionRequest{
			Model: "claude-3-opus-20240229",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
						Role:    openai.ChatMessageRoleUser,
					},
				},
			},
			MaxTokens:   ptr.To(int64(100)),
			Temperature: ptr.To(2.0),
		}

		_, _, err := translator.RequestBody(nil, req, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "temperature")
	})
}

func TestAnthropicDefaultVersion(t *testing.T) {
	require.Equal(t, "2023-06-01", anthropicDefaultVersion)
}
