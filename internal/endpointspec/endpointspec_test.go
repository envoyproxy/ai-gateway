// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package endpointspec

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	anthropic "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/redaction"
)

func TestChatCompletionsEndpointSpec_ParseBody(t *testing.T) {
	spec := ChatCompletionsEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("not-json"), false)
		require.ErrorContains(t, err, "malformed request")
	})

	t.Run("streaming_without_include_usage", func(t *testing.T) {
		req := openai.ChatCompletionRequest{Model: "gpt-4o", Stream: true}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, true)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.NotNil(t, parsed.StreamOptions)
		require.True(t, parsed.StreamOptions.IncludeUsage)
		require.NotNil(t, mutated)

		var mutatedReq openai.ChatCompletionRequest
		require.NoError(t, json.Unmarshal(mutated, &mutatedReq))
		require.NotNil(t, mutatedReq.StreamOptions)
		require.True(t, mutatedReq.StreamOptions.IncludeUsage)
	})

	t.Run("streaming_with_include_usage_already_true", func(t *testing.T) {
		req := openai.ChatCompletionRequest{
			Model:         "gpt-4.1",
			Stream:        true,
			StreamOptions: &openai.StreamOptions{IncludeUsage: true},
		}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		_, parsed, _, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.NotNil(t, parsed)
		require.True(t, parsed.StreamOptions.IncludeUsage)
		require.Nil(t, mutated)
	})

	t.Run("non_streaming", func(t *testing.T) {
		req := openai.ChatCompletionRequest{Model: "gpt-4-mini", Stream: false}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4-mini", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestChatCompletionsEndpointSpec_GetTranslator(t *testing.T) {
	spec := ChatCompletionsEndpointSpec{}
	supported := []filterapi.VersionedAPISchema{
		{Name: filterapi.APISchemaOpenAI, Prefix: "v1"},
		{Name: filterapi.APISchemaAWSBedrock},
		{Name: filterapi.APISchemaAWSAnthropic},
		{Name: filterapi.APISchemaAzureOpenAI, Version: "2024-02-01"},
		{Name: filterapi.APISchemaGCPVertexAI},
		{Name: filterapi.APISchemaGCPAnthropic, Version: "2024-05-01"},
	}

	for _, schema := range supported {
		s := schema
		t.Run("supported_"+string(s.Name), func(t *testing.T) {
			t.Parallel()
			translator, err := spec.GetTranslator(s, "override")
			require.NoError(t, err)
			require.NotNil(t, translator)
		})
	}

	t.Run("unsupported", func(t *testing.T) {
		_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: "Unknown"}, "override")
		require.ErrorContains(t, err, "unsupported API schema")
	})
}

func TestCompletionsEndpointSpec_ParseBody(t *testing.T) {
	spec := CompletionsEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{bad"), false)
		require.ErrorContains(t, err, "malformed request")
	})

	t.Run("streaming", func(t *testing.T) {
		req := openai.CompletionRequest{Model: "text-davinci-003", Stream: true, Prompt: openai.PromptUnion{Value: "say hi"}}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "text-davinci-003", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestCompletionsEndpointSpec_GetTranslator(t *testing.T) {
	spec := CompletionsEndpointSpec{}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.NoError(t, err)

	_, err = spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestEmbeddingsEndpointSpec_ParseBody(t *testing.T) {
	spec := EmbeddingsEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{"), false)
		require.ErrorContains(t, err, "malformed request")
	})

	t.Run("success", func(t *testing.T) {
		req := openai.EmbeddingRequest{Model: "text-embedding-3-large", Input: openai.EmbeddingRequestInput{Value: "input"}}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "text-embedding-3-large", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestEmbeddingsEndpointSpec_GetTranslator(t *testing.T) {
	spec := EmbeddingsEndpointSpec{}

	supported := []filterapi.VersionedAPISchema{
		{Name: filterapi.APISchemaOpenAI},
		{Name: filterapi.APISchemaAzureOpenAI},
		{Name: filterapi.APISchemaGCPVertexAI},
		{Name: filterapi.APISchemaAWSBedrock},
	}
	for _, schema := range supported {
		s := schema
		t.Run("supported_"+string(s.Name), func(t *testing.T) {
			t.Parallel()
			tr, err := spec.GetTranslator(s, "override")
			require.NoError(t, err)
			require.NotNil(t, tr)
		})
	}

	t.Run("unsupported", func(t *testing.T) {
		_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere}, "override")
		require.ErrorContains(t, err, "unsupported API schema")
	})
}

func TestImageGenerationEndpointSpec_ParseBody(t *testing.T) {
	spec := ImageGenerationEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{"), false)
		require.ErrorContains(t, err, "malformed request")
	})

	t.Run("success", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"model": "gpt-image-1", "prompt": "cat"})
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-image-1", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestImageGenerationEndpointSpec_GetTranslator(t *testing.T) {
	spec := ImageGenerationEndpointSpec{}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.NoError(t, err)

	_, err = spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestMessagesEndpointSpec_ParseBody(t *testing.T) {
	spec := MessagesEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("["), false)
		require.ErrorContains(t, err, "malformed request")
	})

	t.Run("missing model", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"stream": true})
		require.NoError(t, err)

		_, _, _, _, err = spec.ParseBody(body, false)
		require.ErrorContains(t, err, "model field is required")
	})

	t.Run("success", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"model": "claude-3", "stream": true})
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "claude-3", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestMessagesEndpointSpec_GetTranslator(t *testing.T) {
	spec := MessagesEndpointSpec{}
	for _, schema := range []filterapi.VersionedAPISchema{
		{Name: filterapi.APISchemaGCPAnthropic},
		{Name: filterapi.APISchemaAWSAnthropic},
		{Name: filterapi.APISchemaAnthropic},
		{Name: filterapi.APISchemaOpenAI},     // This is for OpenAI-schema backends like vLLM that support the /v1/messages endpoint
		{Name: filterapi.APISchemaAWSBedrock}, // This is for AWS Bedrock translation from the /v1/messages endpoint
	} {
		translator, err := spec.GetTranslator(schema, "override")
		require.NoError(t, err)
		require.NotNil(t, translator)
	}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere}, "override")
	require.ErrorContains(t, err, "only supports")
}

func TestRerankEndpointSpec_ParseBody(t *testing.T) {
	spec := RerankEndpointSpec{}
	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{"), false)
		require.ErrorContains(t, err, "malformed request")
	})

	t.Run("success", func(t *testing.T) {
		req := cohereschema.RerankV2Request{Model: "rerank-v3.5", Query: "foo", Documents: []string{"bar"}}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "rerank-v3.5", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})
}

func TestRerankEndpointSpec_GetTranslator(t *testing.T) {
	spec := RerankEndpointSpec{}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaCohere}, "override")
	require.NoError(t, err)

	_, err = spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestResponsesEndpointSpec_ParseBody(t *testing.T) {
	spec := ResponsesEndpointSpec{}
	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{"), false)
		require.ErrorContains(t, err, "malformed request")
	})

	t.Run("success", func(t *testing.T) {
		req := openai.ResponseRequest{Model: "gpt-4o", Input: openai.ResponseNewParamsInputUnion{
			OfString: ptr.To("Hi"),
		}, Stream: true}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})

	t.Run("array_input_without_type_field", func(t *testing.T) {
		body := []byte(`{
			"model": "gpt-4.7",
			"input": [{"role": "user", "content": "Hello"}],
			"max_tokens": 50
		}`)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4.7", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.NotNil(t, parsed.Input.OfInputItemList)
		require.Len(t, parsed.Input.OfInputItemList, 1)
		require.NotNil(t, parsed.Input.OfInputItemList[0].OfMessage)
		require.Equal(t, "user", parsed.Input.OfInputItemList[0].OfMessage.Role)
		require.Nil(t, mutated)
	})

	t.Run("multi_turn_input_with_assistant_output_without_type_field", func(t *testing.T) {
		body := []byte(`{
			"model": "gpt-4.7",
			"input": [
				{"role": "user", "content": "Hello"},
				{"role": "assistant", "content": [{"type": "output_text", "text": "Hi there"}]},
				{"role": "user", "content": "Continue"}
			]
		}`)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4.7", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.NotNil(t, parsed.Input.OfInputItemList)
		require.Len(t, parsed.Input.OfInputItemList, 3)
		require.NotNil(t, parsed.Input.OfInputItemList[1].OfOutputMessage)
		require.Equal(t, "assistant", parsed.Input.OfInputItemList[1].OfOutputMessage.Role)
		require.Nil(t, mutated)
	})
}

func TestResponsesEndpointSpec_GetTranslator(t *testing.T) {
	spec := ResponsesEndpointSpec{}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.NoError(t, err)

	_, err = spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI}, "override")
	require.ErrorContains(t, err, "unsupported API schema")
}

func TestChatCompletionsEndpointSpec_RedactSensitiveInfoFromRequest(t *testing.T) {
	spec := ChatCompletionsEndpointSpec{}

	t.Run("redact_simple_user_message", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    "user",
						Content: openai.StringOrUserRoleContentUnion{Value: "Hello, this is sensitive data"},
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify the message content is redacted
		require.Len(t, redacted.Messages, 1)
		require.NotNil(t, redacted.Messages[0].OfUser)

		redactedContent := redacted.Messages[0].OfUser.Content.Value.(string)
		require.Contains(t, redactedContent, "[REDACTED LENGTH=")
		require.Contains(t, redactedContent, "HASH=")
	})

	t.Run("redact_user_message_with_image", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: "user",
						Content: openai.StringOrUserRoleContentUnion{
							Value: []openai.ChatCompletionContentPartUserUnionParam{
								{
									OfText: &openai.ChatCompletionContentPartTextParam{
										Text: "Describe this image",
										Type: "text",
									},
								},
								{
									OfImageURL: &openai.ChatCompletionContentPartImageParam{
										ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
											URL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
										},
										Type: "image_url",
									},
								},
							},
						},
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify both text and image are redacted
		require.Len(t, redacted.Messages, 1)
		require.NotNil(t, redacted.Messages[0].OfUser)

		parts := redacted.Messages[0].OfUser.Content.Value.([]openai.ChatCompletionContentPartUserUnionParam)
		require.Len(t, parts, 2)

		// Check text redaction
		require.NotNil(t, parts[0].OfText)
		require.Contains(t, parts[0].OfText.Text, "[REDACTED LENGTH=")

		// Check image redaction
		require.NotNil(t, parts[1].OfImageURL)
		require.Contains(t, parts[1].OfImageURL.ImageURL.URL, "[REDACTED LENGTH=")
	})

	t.Run("redact_assistant_message_with_tool_calls", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role:    "assistant",
						Content: openai.StringOrAssistantRoleContentUnion{Value: "I'll call the function"},
						ToolCalls: []openai.ChatCompletionMessageToolCallParam{
							{
								Function: openai.ChatCompletionMessageToolCallFunctionParam{
									Name:      "get_weather",
									Arguments: `{"location": "San Francisco"}`,
								},
								Type: "function",
							},
						},
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify tool call arguments are redacted but function name is kept
		require.Len(t, redacted.Messages, 1)
		require.NotNil(t, redacted.Messages[0].OfAssistant)
		require.Len(t, redacted.Messages[0].OfAssistant.ToolCalls, 1)
		require.Equal(t, "get_weather", redacted.Messages[0].OfAssistant.ToolCalls[0].Function.Name)
		require.Contains(t, redacted.Messages[0].OfAssistant.ToolCalls[0].Function.Arguments, "[REDACTED LENGTH=")
	})

	t.Run("redact_tools", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    "user",
						Content: openai.StringOrUserRoleContentUnion{Value: "What's the weather?"},
					},
				},
			},
			Tools: []openai.Tool{
				{
					Type: "function",
					Function: &openai.FunctionDefinition{
						Name:        "get_weather",
						Description: "Get the current weather in a given location",
						Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{"location": map[string]interface{}{"type": "string"}}},
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Tool definitions are developer-authored schema metadata — kept as-is
		require.Len(t, redacted.Tools, 1)
		require.NotNil(t, redacted.Tools[0].Function)
		require.Equal(t, "get_weather", redacted.Tools[0].Function.Name)
		require.Equal(t, "Get the current weather in a given location", redacted.Tools[0].Function.Description)
		require.Equal(t, req.Tools[0].Function.Parameters, redacted.Tools[0].Function.Parameters)
	})

	t.Run("empty_request", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model:    "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)
		require.Equal(t, "gpt-4o", redacted.Model)
		require.Empty(t, redacted.Messages)
	})

	t.Run("redact_response_format_json_schema", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    "user",
						Content: openai.StringOrUserRoleContentUnion{Value: "Generate a response"},
					},
				},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormatUnion{
				OfJSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
					Type: "json_schema",
					JSONSchema: openai.ChatCompletionResponseFormatJSONSchemaJSONSchema{
						Name:        "math_response",
						Description: "A response containing mathematical steps",
						Schema:      []byte(`{"type":"object","properties":{"steps":{"type":"array"}},"required":["steps"]}`),
						Strict:      true,
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Response format schema is developer-authored — kept as-is
		require.NotNil(t, redacted.ResponseFormat)
		require.NotNil(t, redacted.ResponseFormat.OfJSONSchema)
		require.Equal(t, "math_response", redacted.ResponseFormat.OfJSONSchema.JSONSchema.Name)
		require.Equal(t, "A response containing mathematical steps", redacted.ResponseFormat.OfJSONSchema.JSONSchema.Description)
		require.Equal(t, req.ResponseFormat.OfJSONSchema.JSONSchema.Schema, redacted.ResponseFormat.OfJSONSchema.JSONSchema.Schema)
	})

	t.Run("redact_guided_json", func(t *testing.T) {
		guidedSchema := []byte(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`)
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    "user",
						Content: openai.StringOrUserRoleContentUnion{Value: "Answer the question"},
					},
				},
			},
			GuidedJSON: guidedSchema,
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// guided_json is developer-authored schema — kept as-is
		require.Equal(t, string(guidedSchema), string(redacted.GuidedJSON))
	})

	t.Run("redact_various_message_types", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfSystem: &openai.ChatCompletionSystemMessageParam{
						Role:    "system",
						Content: openai.ContentUnion{Value: "System message with sensitive data"},
					},
				},
				{
					OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
						Role:    "developer",
						Content: openai.ContentUnion{Value: "Developer instructions"},
					},
				},
				{
					OfTool: &openai.ChatCompletionToolMessageParam{
						Role:       "tool",
						Content:    openai.ContentUnion{Value: "Tool result: API_KEY_12345"},
						ToolCallID: "call_123",
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify all message types are redacted
		require.Len(t, redacted.Messages, 3)

		// System message
		require.NotNil(t, redacted.Messages[0].OfSystem)
		systemContent := redacted.Messages[0].OfSystem.Content.Value.(string)
		require.Contains(t, systemContent, "[REDACTED LENGTH=")

		// Developer message
		require.NotNil(t, redacted.Messages[1].OfDeveloper)
		devContent := redacted.Messages[1].OfDeveloper.Content.Value.(string)
		require.Contains(t, devContent, "[REDACTED LENGTH=")

		// Tool message
		require.NotNil(t, redacted.Messages[2].OfTool)
		toolContent := redacted.Messages[2].OfTool.Content.Value.(string)
		require.Contains(t, toolContent, "[REDACTED LENGTH=")
	})

	t.Run("redact_content_union_with_text_parts", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfSystem: &openai.ChatCompletionSystemMessageParam{
						Role: "system",
						Content: openai.ContentUnion{
							Value: []openai.ChatCompletionContentPartTextParam{
								{Type: "text", Text: "First part with sensitive data"},
								{Type: "text", Text: "Second part with more sensitive data"},
							},
						},
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify content parts are redacted
		require.Len(t, redacted.Messages, 1)
		require.NotNil(t, redacted.Messages[0].OfSystem)
		parts := redacted.Messages[0].OfSystem.Content.Value.([]openai.ChatCompletionContentPartTextParam)
		require.Len(t, parts, 2)
		require.Contains(t, parts[0].Text, "[REDACTED LENGTH=")
		require.Contains(t, parts[1].Text, "[REDACTED LENGTH=")
	})

	t.Run("redact_assistant_message_with_content_array", func(t *testing.T) {
		textContent := "Assistant response text"
		refusalContent := "I cannot help with that"
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role: "assistant",
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: []openai.ChatCompletionAssistantMessageParamContent{
								{Type: "text", Text: &textContent},
								{Type: "refusal", Refusal: &refusalContent},
							},
						},
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify content array is redacted
		require.Len(t, redacted.Messages, 1)
		require.NotNil(t, redacted.Messages[0].OfAssistant)
		parts := redacted.Messages[0].OfAssistant.Content.Value.([]openai.ChatCompletionAssistantMessageParamContent)
		require.Len(t, parts, 2)
		require.NotNil(t, parts[0].Text)
		require.Contains(t, *parts[0].Text, "[REDACTED LENGTH=")
		require.NotNil(t, parts[1].Refusal)
		require.Contains(t, *parts[1].Refusal, "[REDACTED LENGTH=")
	})

	t.Run("redact_assistant_message_with_single_content_object", func(t *testing.T) {
		textContent := "Single content object"
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role: "assistant",
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: openai.ChatCompletionAssistantMessageParamContent{
								Type: "text",
								Text: &textContent,
							},
						},
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify single content object is redacted
		require.Len(t, redacted.Messages, 1)
		require.NotNil(t, redacted.Messages[0].OfAssistant)
		part := redacted.Messages[0].OfAssistant.Content.Value.(openai.ChatCompletionAssistantMessageParamContent)
		require.NotNil(t, part.Text)
		require.Contains(t, *part.Text, "[REDACTED LENGTH=")
	})

	t.Run("redact_user_content_with_audio", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: "user",
						Content: openai.StringOrUserRoleContentUnion{
							Value: []openai.ChatCompletionContentPartUserUnionParam{
								{
									OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{
										Type: "input_audio",
										InputAudio: openai.ChatCompletionContentPartInputAudioInputAudioParam{
											Data:   "base64encodedaudiodata==",
											Format: "wav",
										},
									},
								},
							},
						},
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify audio data is redacted
		require.Len(t, redacted.Messages, 1)
		require.NotNil(t, redacted.Messages[0].OfUser)
		parts := redacted.Messages[0].OfUser.Content.Value.([]openai.ChatCompletionContentPartUserUnionParam)
		require.Len(t, parts, 1)
		require.NotNil(t, parts[0].OfInputAudio)
		require.Contains(t, parts[0].OfInputAudio.InputAudio.Data, "[REDACTED LENGTH=")
	})

	t.Run("redact_user_content_with_file", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: "user",
						Content: openai.StringOrUserRoleContentUnion{
							Value: []openai.ChatCompletionContentPartUserUnionParam{
								{
									OfFile: &openai.ChatCompletionContentPartFileParam{
										Type: "file",
										File: openai.ChatCompletionContentPartFileFileParam{
											FileData: "base64encodedfiledata==",
											Filename: "document.pdf",
										},
									},
								},
							},
						},
					},
				},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify file data is redacted
		require.Len(t, redacted.Messages, 1)
		require.NotNil(t, redacted.Messages[0].OfUser)
		parts := redacted.Messages[0].OfUser.Content.Value.([]openai.ChatCompletionContentPartUserUnionParam)
		require.Len(t, parts, 1)
		require.NotNil(t, parts[0].OfFile)
		require.Contains(t, parts[0].OfFile.File.FileData, "[REDACTED LENGTH=")
	})

	t.Run("redact_prediction_content", func(t *testing.T) {
		req := &openai.ChatCompletionRequest{
			Model: "gpt-4o",
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role:    "user",
						Content: openai.StringOrUserRoleContentUnion{Value: "Test message"},
					},
				},
			},
			PredictionContent: &openai.PredictionContent{
				Type:    openai.PredictionContentTypeContent,
				Content: openai.ContentUnion{Value: "Predicted content with sensitive information"},
			},
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)

		// Verify prediction content is redacted
		require.NotNil(t, redacted.PredictionContent)
		redactedContent := redacted.PredictionContent.Content.Value.(string)
		require.Contains(t, redactedContent, "[REDACTED LENGTH=")
		require.Contains(t, redactedContent, "HASH=")
	})
}

func TestRedactString(t *testing.T) {
	t.Run("redact_non_empty_string", func(t *testing.T) {
		result := redaction.RedactString("sensitive data")
		require.Contains(t, result, "[REDACTED LENGTH=14")
		require.Contains(t, result, "HASH=")
	})
}

func TestSpeechEndpointSpec_ParseBody(t *testing.T) {
	spec := SpeechEndpointSpec{}

	t.Run("invalid json", func(t *testing.T) {
		_, _, _, _, err := spec.ParseBody([]byte("{"), false)
		require.ErrorContains(t, err, "failed to unmarshal speech request")
	})

	t.Run("binary_mode", func(t *testing.T) {
		req := openai.SpeechRequest{
			Model: "tts-1",
			Input: "Hello world",
			Voice: "alloy",
		}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "tts-1", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Nil(t, mutated)
	})

	t.Run("sse_streaming_mode", func(t *testing.T) {
		sseFormat := "sse"
		req := openai.SpeechRequest{
			Model:        "gpt-4o-mini-tts",
			Input:        "Hello streaming",
			Voice:        "nova",
			StreamFormat: &sseFormat,
		}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o-mini-tts", model)
		require.True(t, stream)
		require.NotNil(t, parsed)
		require.Equal(t, "sse", *parsed.StreamFormat)
		require.Nil(t, mutated)
	})

	t.Run("audio_format_mode", func(t *testing.T) {
		audioFormat := "audio"
		req := openai.SpeechRequest{
			Model:        "tts-1-hd",
			Input:        "Test",
			Voice:        "alloy",
			StreamFormat: &audioFormat,
		}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		_, _, stream, _, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.False(t, stream, "audio format should not be treated as streaming")
	})

	t.Run("with_all_optional_params", func(t *testing.T) {
		instructions := "Speak clearly"
		responseFormat := "mp3"
		speed := 1.5
		req := openai.SpeechRequest{
			Model:          "tts-1",
			Input:          "Test with options",
			Voice:          "shimmer",
			Instructions:   &instructions,
			ResponseFormat: &responseFormat,
			Speed:          &speed,
		}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		model, parsed, stream, mutated, err := spec.ParseBody(body, false)
		require.NoError(t, err)
		require.Equal(t, "tts-1", model)
		require.False(t, stream)
		require.NotNil(t, parsed)
		require.Equal(t, "Speak clearly", *parsed.Instructions)
		require.Equal(t, "mp3", *parsed.ResponseFormat)
		require.Equal(t, 1.5, *parsed.Speed)
		require.Nil(t, mutated)
	})
}

func TestSpeechEndpointSpec_GetTranslator(t *testing.T) {
	spec := SpeechEndpointSpec{}

	_, err := spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, "override")
	require.NoError(t, err)

	_, err = spec.GetTranslator(filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI}, "override")
	require.ErrorContains(t, err, "unsupported API schema for speech")
}

func TestSpeechEndpointSpec_RedactSensitiveInfoFromRequest(t *testing.T) {
	spec := SpeechEndpointSpec{}

	t.Run("redact_input_text", func(t *testing.T) {
		req := &openai.SpeechRequest{
			Model: "tts-1",
			Input: "This is sensitive text that should be redacted",
			Voice: "alloy",
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)
		require.Contains(t, redacted.Input, "[REDACTED LENGTH=")
		require.Equal(t, "tts-1", redacted.Model)
		require.Equal(t, "alloy", redacted.Voice)
	})

	t.Run("redact_instructions", func(t *testing.T) {
		instructions := "Speak with a British accent and emphasize certain words"
		req := &openai.SpeechRequest{
			Model:        "gpt-4o-mini-tts",
			Input:        "Hello world",
			Voice:        "nova",
			Instructions: &instructions,
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)
		require.Contains(t, redacted.Input, "[REDACTED LENGTH=")
		require.NotNil(t, redacted.Instructions)
		require.Contains(t, *redacted.Instructions, "[REDACTED LENGTH=")
	})

	t.Run("no_instructions", func(t *testing.T) {
		req := &openai.SpeechRequest{
			Model: "tts-1-hd",
			Input: "Test input",
			Voice: "echo",
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)
		require.Contains(t, redacted.Input, "[REDACTED LENGTH=")
		require.Nil(t, redacted.Instructions)
	})

	t.Run("preserve_other_fields", func(t *testing.T) {
		responseFormat := "wav"
		speed := 1.5
		streamFormat := "sse"
		req := &openai.SpeechRequest{
			Model:          "gpt-4o-mini-tts",
			Input:          "Sensitive content",
			Voice:          "shimmer",
			ResponseFormat: &responseFormat,
			Speed:          &speed,
			StreamFormat:   &streamFormat,
		}

		redacted, err := spec.RedactSensitiveInfoFromRequest(req)
		require.NoError(t, err)
		require.NotNil(t, redacted)
		require.Contains(t, redacted.Input, "[REDACTED LENGTH=")
		require.Equal(t, "shimmer", redacted.Voice)
		require.NotNil(t, redacted.ResponseFormat)
		require.Equal(t, "wav", *redacted.ResponseFormat)
		require.NotNil(t, redacted.Speed)
		require.Equal(t, 1.5, *redacted.Speed)
		require.NotNil(t, redacted.StreamFormat)
		require.Equal(t, "sse", *redacted.StreamFormat)
	})
}

func TestMessagesEndpointSpec_EarlyTranslate(t *testing.T) {
	spec := MessagesEndpointSpec{}

	req := &anthropic.MessagesRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: 100,
		Messages: []anthropic.MessageParam{
			{Role: anthropic.MessageRoleUser, Content: anthropic.MessageContent{Text: "hello"}},
		},
	}

	headers, body, err := spec.EarlyTranslate(req)
	require.NoError(t, err)
	require.NotNil(t, body)

	// Verify headers contain :path rewritten to chat/completions and matching content-length.
	var pathVal, contentLengthVal string
	for _, h := range headers {
		switch h.Key() {
		case ":path":
			pathVal = h.Value()
		case "content-length":
			contentLengthVal = h.Value()
		}
	}
	require.Equal(t, "/v1/chat/completions", pathVal)
	require.Equal(t, fmt.Sprintf("%d", len(body)), contentLengthVal)

	// Body must be valid OpenAI chat-completions JSON with the model preserved.
	var openaiReq openai.ChatCompletionRequest
	require.NoError(t, json.Unmarshal(body, &openaiReq))
	require.Equal(t, "claude-3-5-sonnet-20241022", openaiReq.Model)
}

func TestEarlyTranslate_NoOpSpecs(t *testing.T) {
	t.Run("chat_completions", func(t *testing.T) {
		headers, body, err := ChatCompletionsEndpointSpec{}.EarlyTranslate(&openai.ChatCompletionRequest{})
		require.NoError(t, err)
		require.Nil(t, headers)
		require.Nil(t, body)
	})
	t.Run("completions", func(t *testing.T) {
		headers, body, err := CompletionsEndpointSpec{}.EarlyTranslate(&openai.CompletionRequest{})
		require.NoError(t, err)
		require.Nil(t, headers)
		require.Nil(t, body)
	})
	t.Run("embeddings", func(t *testing.T) {
		headers, body, err := EmbeddingsEndpointSpec{}.EarlyTranslate(&openai.EmbeddingRequest{})
		require.NoError(t, err)
		require.Nil(t, headers)
		require.Nil(t, body)
	})
	t.Run("image_generation", func(t *testing.T) {
		headers, body, err := ImageGenerationEndpointSpec{}.EarlyTranslate(&openai.ImageGenerationRequest{})
		require.NoError(t, err)
		require.Nil(t, headers)
		require.Nil(t, body)
	})
	t.Run("responses", func(t *testing.T) {
		headers, body, err := ResponsesEndpointSpec{}.EarlyTranslate(&openai.ResponseRequest{})
		require.NoError(t, err)
		require.Nil(t, headers)
		require.Nil(t, body)
	})
	t.Run("rerank", func(t *testing.T) {
		headers, body, err := RerankEndpointSpec{}.EarlyTranslate(&cohereschema.RerankV2Request{})
		require.NoError(t, err)
		require.Nil(t, headers)
		require.Nil(t, body)
	})
}
