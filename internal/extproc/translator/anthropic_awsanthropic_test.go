// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
)

func TestAnthropicToAWSAnthropicTranslator_RequestBody_ModelNameOverride(t *testing.T) {
	tests := []struct {
		name           string
		override       string
		inputModel     string
		expectedModel  string
		expectedInPath string
	}{
		{
			name:           "no override uses original model",
			override:       "",
			inputModel:     "anthropic.claude-3-haiku-20240307-v1:0",
			expectedModel:  "anthropic.claude-3-haiku-20240307-v1:0",
			expectedInPath: "anthropic.claude-3-haiku-20240307-v1:0",
		},
		{
			name:           "override replaces model in body and path",
			override:       "anthropic.claude-3-sonnet-20240229-v1:0",
			inputModel:     "anthropic.claude-3-haiku-20240307-v1:0",
			expectedModel:  "anthropic.claude-3-sonnet-20240229-v1:0",
			expectedInPath: "anthropic.claude-3-sonnet-20240229-v1:0",
		},
		{
			name:           "override with empty input model",
			override:       "anthropic.claude-3-opus-20240229-v1:0",
			inputModel:     "",
			expectedModel:  "anthropic.claude-3-opus-20240229-v1:0",
			expectedInPath: "anthropic.claude-3-opus-20240229-v1:0",
		},
		{
			name:           "model with ARN format",
			override:       "",
			inputModel:     "arn:aws:bedrock:eu-central-1:000000000:application-inference-profile/aaaaaaaaa",
			expectedModel:  "arn:aws:bedrock:eu-central-1:000000000:application-inference-profile/aaaaaaaaa",
			expectedInPath: "arn:aws:bedrock:eu-central-1:000000000:application-inference-profile%2Faaaaaaaaa",
		},
		{
			name:           "global model ID",
			override:       "",
			inputModel:     "global.anthropic.claude-sonnet-4-5-20250929-v1:0",
			expectedModel:  "global.anthropic.claude-sonnet-4-5-20250929-v1:0",
			expectedInPath: "global.anthropic.claude-sonnet-4-5-20250929-v1:0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewAnthropicToAWSAnthropicTranslator("bedrock-2023-05-31", tt.override)

			// Create the request using map structure.
			originalReq := &anthropicschema.MessagesRequest{
				"model": tt.inputModel,
				"messages": []anthropic.MessageParam{
					{
						Role: anthropic.MessageParamRoleUser,
						Content: []anthropic.ContentBlockParamUnion{
							anthropic.NewTextBlock("Hello"),
						},
					},
				},
			}

			rawBody, err := json.Marshal(originalReq)
			require.NoError(t, err)

			headerMutation, bodyMutation, err := translator.RequestBody(rawBody, originalReq, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.NotNil(t, bodyMutation)

			// Check path header contains expected model (URL encoded).
			// Use the last element as it takes precedence when multiple headers are set.
			pathHeader := headerMutation.SetHeaders[len(headerMutation.SetHeaders)-1]
			require.Equal(t, ":path", pathHeader.Header.Key)
			expectedPath := "/model/" + tt.expectedInPath + "/invoke"
			assert.Equal(t, expectedPath, string(pathHeader.Header.RawValue))

			// Check that model field is removed from body (since it's in the path).
			var modifiedReq map[string]any
			err = json.Unmarshal(bodyMutation.GetBody(), &modifiedReq)
			require.NoError(t, err)
			_, hasModel := modifiedReq["model"]
			assert.False(t, hasModel, "model field should be removed from request body")

			// Verify anthropic_version field is added (required by AWS Bedrock).
			version, hasVersion := modifiedReq["anthropic_version"]
			assert.True(t, hasVersion, "anthropic_version should be added for AWS Bedrock")
			assert.Equal(t, "bedrock-2023-05-31", version, "anthropic_version should match the configured version")
		})
	}
}

func TestAnthropicToAWSAnthropicTranslator_ComprehensiveMarshalling(t *testing.T) {
	translator := NewAnthropicToAWSAnthropicTranslator("bedrock-2023-05-31", "")

	// Create a comprehensive MessagesRequest with all possible fields using map structure.
	originalReq := &anthropicschema.MessagesRequest{
		"model": "anthropic.claude-3-opus-20240229-v1:0",
		"messages": []anthropic.MessageParam{
			{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock("Hello, how are you?"),
				},
			},
			{
				Role: anthropic.MessageParamRoleAssistant,
				Content: []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock("I'm doing well, thank you!"),
				},
			},
			{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock("Can you help me with the weather?"),
				},
			},
		},
		"max_tokens":     1024,
		"stream":         false,
		"temperature":    func() *float64 { v := 0.7; return &v }(),
		"top_p":          func() *float64 { v := 0.95; return &v }(),
		"top_k":          func() *int { v := 40; return &v }(),
		"stop_sequences": []string{"Human:", "Assistant:"},
		"system":         "You are a helpful weather assistant.",
		"tools": []anthropic.ToolParam{
			{
				Name:        "get_weather",
				Description: anthropic.String("Get current weather information"),
				InputSchema: anthropic.ToolInputSchemaParam{
					Type: "object",
					Properties: map[string]any{
						"location": map[string]any{
							"type":        "string",
							"description": "City name",
						},
					},
					Required: []string{"location"},
				},
			},
		},
		"tool_choice": anthropic.ToolChoiceUnionParam{
			OfAuto: &anthropic.ToolChoiceAutoParam{},
		},
	}

	rawBody, err := json.Marshal(originalReq)
	require.NoError(t, err)

	headerMutation, bodyMutation, err := translator.RequestBody(rawBody, originalReq, false)
	require.NoError(t, err)
	require.NotNil(t, headerMutation)
	require.NotNil(t, bodyMutation)

	var outputReq map[string]any
	err = json.Unmarshal(bodyMutation.GetBody(), &outputReq)
	require.NoError(t, err)

	require.NotContains(t, outputReq, "model", "model field should be removed for AWS Bedrock")

	// AWS Bedrock requires anthropic_version field.
	require.Contains(t, outputReq, "anthropic_version", "anthropic_version should be added for AWS Bedrock")
	require.Equal(t, "bedrock-2023-05-31", outputReq["anthropic_version"], "anthropic_version should match the configured version")

	messages, ok := outputReq["messages"].([]any)
	require.True(t, ok, "messages should be an array")
	require.Len(t, messages, 3, "should have 3 messages")

	require.Equal(t, float64(1024), outputReq["max_tokens"])
	require.Equal(t, false, outputReq["stream"])
	require.Equal(t, 0.7, outputReq["temperature"])
	require.Equal(t, 0.95, outputReq["top_p"])
	require.Equal(t, float64(40), outputReq["top_k"])
	require.Equal(t, "You are a helpful weather assistant.", outputReq["system"])

	stopSeq, ok := outputReq["stop_sequences"].([]any)
	require.True(t, ok, "stop_sequences should be an array")
	require.Len(t, stopSeq, 2)
	require.Equal(t, "Human:", stopSeq[0])
	require.Equal(t, "Assistant:", stopSeq[1])

	tools, ok := outputReq["tools"].([]any)
	require.True(t, ok, "tools should be an array")
	require.Len(t, tools, 1)

	toolChoice, ok := outputReq["tool_choice"].(map[string]any)
	require.True(t, ok, "tool_choice should be an object")
	require.NotEmpty(t, toolChoice)

	// Use the last element as it takes precedence when multiple headers are set.
	pathHeader := headerMutation.SetHeaders[len(headerMutation.SetHeaders)-1]
	require.Equal(t, ":path", pathHeader.Header.Key)
	expectedPath := "/model/anthropic.claude-3-opus-20240229-v1:0/invoke"
	require.Equal(t, expectedPath, string(pathHeader.Header.RawValue))
}

func TestAnthropicToAWSAnthropicTranslator_RequestBody_StreamingPaths(t *testing.T) {
	tests := []struct {
		name               string
		stream             any
		expectedPathSuffix string
	}{
		{
			name:               "non-streaming uses /invoke",
			stream:             false,
			expectedPathSuffix: "/invoke",
		},
		{
			name:               "streaming uses /invoke-stream",
			stream:             true,
			expectedPathSuffix: "/invoke-stream",
		},
		{
			name:               "missing stream defaults to /invoke",
			stream:             nil,
			expectedPathSuffix: "/invoke",
		},
		{
			name:               "non-boolean stream defaults to /invoke",
			stream:             "true",
			expectedPathSuffix: "/invoke",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewAnthropicToAWSAnthropicTranslator("bedrock-2023-05-31", "")

			parsedReq := &anthropicschema.MessagesRequest{
				"model": "anthropic.claude-3-sonnet-20240229-v1:0",
				"messages": []anthropic.MessageParam{
					{
						Role: anthropic.MessageParamRoleUser,
						Content: []anthropic.ContentBlockParamUnion{
							anthropic.NewTextBlock("Test"),
						},
					},
				},
			}
			if tt.stream != nil {
				if streamVal, ok := tt.stream.(bool); ok {
					(*parsedReq)["stream"] = streamVal
				}
			}

			rawBody, err := json.Marshal(parsedReq)
			require.NoError(t, err)

			headerMutation, _, err := translator.RequestBody(rawBody, parsedReq, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)

			// Check path contains expected suffix.
			// Use the last element as it takes precedence when multiple headers are set.
			pathHeader := headerMutation.SetHeaders[len(headerMutation.SetHeaders)-1]
			expectedPath := "/model/anthropic.claude-3-sonnet-20240229-v1:0" + tt.expectedPathSuffix
			assert.Equal(t, expectedPath, string(pathHeader.Header.RawValue))
		})
	}
}

func TestAnthropicToAWSAnthropicTranslator_RequestBody_FieldPassthrough(t *testing.T) {
	translator := NewAnthropicToAWSAnthropicTranslator("bedrock-2023-05-31", "")

	temp := 0.7
	topP := 0.95
	topK := 40
	parsedReq := &anthropicschema.MessagesRequest{
		"model": "anthropic.claude-3-sonnet-20240229-v1:0",
		"messages": []anthropic.MessageParam{
			{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock("Hello, world!"),
				},
			},
			{
				Role: anthropic.MessageParamRoleAssistant,
				Content: []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock("Hi there!"),
				},
			},
			{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock("How are you?"),
				},
			},
		},
		"max_tokens":     1000,
		"temperature":    &temp,
		"top_p":          &topP,
		"top_k":          &topK,
		"stop_sequences": []string{"Human:", "Assistant:"},
		"stream":         false,
		"system":         "You are a helpful assistant",
		"tools": []anthropic.ToolParam{
			{
				Name:        "get_weather",
				Description: anthropic.String("Get weather info"),
				InputSchema: anthropic.ToolInputSchemaParam{
					Type: "object",
					Properties: map[string]any{
						"location": map[string]any{"type": "string"},
					},
				},
			},
		},
		"tool_choice": map[string]any{"type": "auto"},
		"metadata":    map[string]any{"user.id": "test123"},
	}

	rawBody, err := json.Marshal(parsedReq)
	require.NoError(t, err)

	_, bodyMutation, err := translator.RequestBody(rawBody, parsedReq, false)
	require.NoError(t, err)
	require.NotNil(t, bodyMutation)

	var modifiedReq map[string]any
	err = json.Unmarshal(bodyMutation.GetBody(), &modifiedReq)
	require.NoError(t, err)

	// Messages should be preserved.
	require.Len(t, modifiedReq["messages"], 3)

	// Numeric fields get converted to float64 by JSON unmarshalling.
	require.Equal(t, float64(1000), modifiedReq["max_tokens"])
	require.Equal(t, 0.7, modifiedReq["temperature"])
	require.Equal(t, 0.95, modifiedReq["top_p"])
	require.Equal(t, float64(40), modifiedReq["top_k"])

	// Arrays become []interface{} by JSON unmarshalling.
	stopSeq, ok := modifiedReq["stop_sequences"].([]any)
	require.True(t, ok)
	require.Len(t, stopSeq, 2)
	require.Equal(t, "Human:", stopSeq[0])
	require.Equal(t, "Assistant:", stopSeq[1])

	// Boolean false values are now included in the map.
	require.Equal(t, false, modifiedReq["stream"])

	// String values are preserved.
	require.Equal(t, "You are a helpful assistant", modifiedReq["system"])

	// Complex objects should be preserved as maps.
	require.NotNil(t, modifiedReq["tools"])
	require.NotNil(t, modifiedReq["tool_choice"])
	require.NotNil(t, modifiedReq["metadata"])

	// Verify model field is removed from body (it's in the path instead).
	_, hasModel := modifiedReq["model"]
	require.False(t, hasModel, "model field should be removed from request body")

	// Verify anthropic_version is added for AWS Bedrock.
	version, hasVersion := modifiedReq["anthropic_version"]
	require.True(t, hasVersion, "anthropic_version should be added for AWS Bedrock")
	require.Equal(t, "bedrock-2023-05-31", version, "anthropic_version should match the configured version")
}

func TestAnthropicToAWSAnthropicTranslator_URLEncoding(t *testing.T) {
	tests := []struct {
		name         string
		modelID      string
		expectedPath string
	}{
		{
			name:         "simple model ID with colon",
			modelID:      "anthropic.claude-3-sonnet-20240229-v1:0",
			expectedPath: "/model/anthropic.claude-3-sonnet-20240229-v1:0/invoke",
		},
		{
			name:         "full ARN with multiple special characters",
			modelID:      "arn:aws:bedrock:us-east-1:123456789012:foundation-model/anthropic.claude-3-sonnet-20240229-v1:0",
			expectedPath: "/model/arn:aws:bedrock:us-east-1:123456789012:foundation-model%2Fanthropic.claude-3-sonnet-20240229-v1:0/invoke",
		},
		{
			name:         "global model prefix",
			modelID:      "global.anthropic.claude-sonnet-4-5-20250929-v1:0",
			expectedPath: "/model/global.anthropic.claude-sonnet-4-5-20250929-v1:0/invoke",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewAnthropicToAWSAnthropicTranslator("bedrock-2023-05-31", "")

			originalReq := &anthropicschema.MessagesRequest{
				"model": tt.modelID,
				"messages": []anthropic.MessageParam{
					{
						Role: anthropic.MessageParamRoleUser,
						Content: []anthropic.ContentBlockParamUnion{
							anthropic.NewTextBlock("Test"),
						},
					},
				},
			}

			rawBody, err := json.Marshal(originalReq)
			require.NoError(t, err)

			headerMutation, _, err := translator.RequestBody(rawBody, originalReq, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)

			// Use the last element as it takes precedence when multiple headers are set.
			pathHeader := headerMutation.SetHeaders[len(headerMutation.SetHeaders)-1]
			assert.Equal(t, tt.expectedPath, string(pathHeader.Header.RawValue))
		})
	}
}
