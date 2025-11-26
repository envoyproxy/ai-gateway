// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

func TestAnthropicToGCPAnthropicTranslator_RequestBody_ModelNameOverride(t *testing.T) {
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
			inputModel:     "claude-3-haiku-20240307",
			expectedModel:  "claude-3-haiku-20240307",
			expectedInPath: "claude-3-haiku-20240307",
		},
		{
			name:           "override replaces model in body and path",
			override:       "claude-3-sonnet-override",
			inputModel:     "claude-3-haiku-20240307",
			expectedModel:  "claude-3-sonnet-override",
			expectedInPath: "claude-3-sonnet-override",
		},
		{
			name:           "override with empty input model",
			override:       "claude-3-opus-20240229",
			inputModel:     "",
			expectedModel:  "claude-3-opus-20240229",
			expectedInPath: "claude-3-opus-20240229",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewAnthropicToGCPAnthropicTranslator("2023-06-01", tt.override)

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

			headerMutation, bodyMutation, err := translator.RequestBody(nil, originalReq, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.NotNil(t, bodyMutation)

			// Check path header contains expected model.
			pathHeader := headerMutation[0]
			require.Equal(t, pathHeaderName, pathHeader.Key())
			expectedPath := "publishers/anthropic/models/" + tt.expectedInPath + ":rawPredict"
			assert.Equal(t, expectedPath, pathHeader.Value())

			// Check that model field is removed from body (since it's in the path).
			var modifiedReq map[string]any
			err = json.Unmarshal(bodyMutation, &modifiedReq)
			require.NoError(t, err)
			_, hasModel := modifiedReq["model"]
			assert.False(t, hasModel, "model field should be removed from request body")
		})
	}
}

func TestAnthropicToGCPAnthropicTranslator_ComprehensiveMarshalling(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("2023-06-01", "")

	// Create a comprehensive MessagesRequest with all possible fields using map structure.
	originalReq := &anthropicschema.MessagesRequest{
		"model": "claude-3-opus-20240229",
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

	headerMutation, bodyMutation, err := translator.RequestBody(nil, originalReq, false)
	require.NoError(t, err)
	require.NotNil(t, headerMutation)
	require.NotNil(t, bodyMutation)

	var outputReq map[string]any
	err = json.Unmarshal(bodyMutation, &outputReq)
	require.NoError(t, err)

	require.NotContains(t, outputReq, "model", "model field should be removed for GCP")

	require.Contains(t, outputReq, "anthropic_version", "should add anthropic_version for GCP")
	require.Equal(t, "2023-06-01", outputReq["anthropic_version"])

	messages, ok := outputReq["messages"].([]any)
	require.True(t, ok, "messages should be an array")
	require.Len(t, messages, 3, "should have 3 messages")

	require.Equal(t, float64(1024), outputReq["max_tokens"])
	// stream: false is now included in the map
	require.Equal(t, false, outputReq["stream"])
	require.Equal(t, 0.7, outputReq["temperature"])
	require.Equal(t, 0.95, outputReq["top_p"])
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

	pathHeader := headerMutation[0]
	require.Equal(t, ":path", pathHeader.Key())
	expectedPath := "publishers/anthropic/models/claude-3-opus-20240229:rawPredict"
	require.Equal(t, expectedPath, pathHeader.Value())
}

func TestAnthropicToGCPAnthropicTranslator_BackendVersionHandling(t *testing.T) {
	tests := []struct {
		name            string
		backendVersion  string
		expectedVersion string
		shouldError     bool
	}{
		{
			name:            "no version configured should error",
			backendVersion:  "",
			expectedVersion: "",
			shouldError:     true,
		},
		{
			name:            "backend version only",
			backendVersion:  "2023-06-01",
			expectedVersion: "2023-06-01",
			shouldError:     false,
		},
		{
			name:            "custom backend version",
			backendVersion:  "2024-01-01",
			expectedVersion: "2024-01-01",
			shouldError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewAnthropicToGCPAnthropicTranslator(tt.backendVersion, "")

			originalReq := &anthropicschema.MessagesRequest{
				"model": "claude-3-sonnet-20240229",
				"messages": []anthropic.MessageParam{
					{
						Role: anthropic.MessageParamRoleUser,
						Content: []anthropic.ContentBlockParamUnion{
							anthropic.NewTextBlock("Hello"),
						},
					},
				},
				"max_tokens": 100,
			}

			_, bodyMutation, err := translator.RequestBody(nil, originalReq, false)

			if tt.shouldError {
				require.Error(t, err)
				require.Contains(t, err.Error(), "anthropic_version is required for GCP Vertex AI")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, bodyMutation)

			var outputReq map[string]any
			err = json.Unmarshal(bodyMutation, &outputReq)
			require.NoError(t, err)

			require.Contains(t, outputReq, "anthropic_version")
			require.Equal(t, tt.expectedVersion, outputReq["anthropic_version"])
		})
	}
}

func TestAnthropicToGCPAnthropicTranslator_RequestBody_StreamingPaths(t *testing.T) {
	tests := []struct {
		name              string
		stream            any
		expectedSpecifier string
	}{
		{
			name:              "non-streaming uses rawPredict",
			stream:            false,
			expectedSpecifier: "rawPredict",
		},
		{
			name:              "streaming uses streamRawPredict",
			stream:            true,
			expectedSpecifier: "streamRawPredict",
		},
		{
			name:              "missing stream defaults to rawPredict",
			stream:            nil,
			expectedSpecifier: "rawPredict",
		},
		{
			name:              "non-boolean stream defaults to rawPredict",
			stream:            "true",
			expectedSpecifier: "rawPredict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewAnthropicToGCPAnthropicTranslator("2023-06-01", "")

			reqBody := map[string]any{
				"model":    "claude-3-sonnet-20240229",
				"messages": []map[string]any{{"role": "user", "content": "Test"}},
			}

			if tt.stream != nil {
				reqBody["stream"] = tt.stream
			}

			parsedReq := &anthropicschema.MessagesRequest{
				"model": "claude-3-sonnet-20240229",
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

			headerMutation, _, err := translator.RequestBody(nil, parsedReq, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)

			// Check path contains expected specifier.
			pathHeader := headerMutation[0]
			expectedPath := "publishers/anthropic/models/claude-3-sonnet-20240229:" + tt.expectedSpecifier
			assert.Equal(t, pathHeaderName, pathHeader.Key())
			assert.Equal(t, expectedPath, pathHeader.Value())
		})
	}
}

func TestAnthropicToGCPAnthropicTranslator_RequestBody_FieldPassthrough(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("2023-06-01", "")

	temp := 0.7
	topP := 0.95
	topK := 40
	parsedReq := &anthropicschema.MessagesRequest{
		"model": "claude-3-sonnet-20240229",
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

	_, bodyMutation, err := translator.RequestBody(nil, parsedReq, false)
	require.NoError(t, err)
	require.NotNil(t, bodyMutation)

	var modifiedReq map[string]any
	err = json.Unmarshal(bodyMutation, &modifiedReq)
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

	// Verify anthropic_version is added from the backend configuration.
	require.Equal(t, "2023-06-01", modifiedReq["anthropic_version"])
}

func TestAnthropicToGCPAnthropicTranslator_ResponseHeaders(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("2023-06-01", "")

	tests := []struct {
		name    string
		headers map[string]string
	}{
		{
			name:    "empty headers",
			headers: map[string]string{},
		},
		{
			name: "various headers",
			headers: map[string]string{
				"content-type":  "application/json",
				"authorization": "Bearer token",
				"custom-header": "value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headerMutation, err := translator.ResponseHeaders(tt.headers)
			require.NoError(t, err)
			assert.Nil(t, headerMutation, "ResponseHeaders should return nil for passthrough")
		})
	}
}

func TestAnthropicToGCPAnthropicTranslator_ResponseBody_ZeroTokenUsage(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("2023-06-01", "")

	// Test response with zero token usage.
	respBody := anthropic.Message{
		ID:      "msg_zero",
		Type:    "message",
		Role:    "assistant",
		Content: []anthropic.ContentBlockUnion{{Type: "text", Text: ""}},
		Model:   "claude-3-sonnet-20240229",
		Usage: anthropic.Usage{
			InputTokens:  0,
			OutputTokens: 0,
		},
	}

	bodyBytes, err := json.Marshal(respBody)
	require.NoError(t, err)

	bodyReader := bytes.NewReader(bodyBytes)
	respHeaders := map[string]string{"content-type": "application/json"}

	_, _, tokenUsage, _, err := translator.ResponseBody(respHeaders, bodyReader, true, nil)
	require.NoError(t, err)

	expected := tokenUsageFrom(0, 0, 0, 0)
	assert.Equal(t, expected, tokenUsage)
}

func TestAnthropicToGCPAnthropicTranslator_ResponseBody_StreamingTokenUsage(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("2023-06-01", "")
	translator.(*anthropicToGCPAnthropicTranslator).stream = true

	tests := []struct {
		name          string
		chunk         string
		endOfStream   bool
		expectedUsage metrics.TokenUsage
	}{
		{
			name:          "regular streaming chunk without usage",
			chunk:         "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" to me.\"}}\n\n",
			endOfStream:   false,
			expectedUsage: tokenUsageFrom(-1, -1, -1, -1),
		},
		{
			name:          "message_delta chunk with token usage",
			chunk:         "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":84}}\n\n",
			endOfStream:   false,
			expectedUsage: tokenUsageFrom(0, 0, 84, 84),
		},
		{
			name:          "message_stop chunk without usage",
			chunk:         "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			endOfStream:   false,
			expectedUsage: tokenUsageFrom(-1, -1, -1, -1),
		},
		{
			name:          "invalid json chunk",
			chunk:         "event: invalid\ndata: {\"invalid\": \"json\"}\n\n",
			endOfStream:   false,
			expectedUsage: tokenUsageFrom(-1, -1, -1, -1),
		},
		{
			name:          "message_delta with decimal output_tokens",
			chunk:         "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":42.0}}\n\n",
			endOfStream:   false,
			expectedUsage: tokenUsageFrom(0, 0, 42, 42),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyReader := bytes.NewReader([]byte(tt.chunk))
			respHeaders := map[string]string{"content-type": "application/json"}

			headerMutation, bodyMutation, tokenUsage, _, err := translator.ResponseBody(respHeaders, bodyReader, tt.endOfStream, nil)

			require.NoError(t, err)
			require.Nil(t, headerMutation)
			require.Nil(t, bodyMutation)
			require.Equal(t, tt.expectedUsage, tokenUsage)
		})
	}
}

func TestAnthropicToGCPAnthropicTranslator_ResponseBody_StreamingEdgeCases(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("2023-06-01", "")
	translator.(*anthropicToGCPAnthropicTranslator).stream = true

	tests := []struct {
		name          string
		chunk         string
		expectedUsage metrics.TokenUsage
	}{
		{
			name:          "message_start without message field",
			chunk:         "event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
			expectedUsage: tokenUsageFrom(0, 0, 0, 0),
		},
		{
			name:          "message_start without usage field",
			chunk:         "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\"}}\n\n",
			expectedUsage: tokenUsageFrom(0, 0, 0, 0),
		},
		{
			name:          "message_delta without usage field",
			chunk:         "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
			expectedUsage: tokenUsageFrom(0, 0, 0, 0),
		},
		{
			name:          "invalid json in data",
			chunk:         "event: message_start\ndata: {invalid json}\n\n",
			expectedUsage: tokenUsageFrom(-1, -1, -1, -1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyReader := bytes.NewReader([]byte(tt.chunk))
			respHeaders := map[string]string{"content-type": "application/json"}

			headerMutation, bodyMutation, tokenUsage, _, err := translator.ResponseBody(respHeaders, bodyReader, false, nil)

			require.NoError(t, err)
			require.Nil(t, headerMutation)
			require.Nil(t, bodyMutation)
			require.Equal(t, tt.expectedUsage, tokenUsage)
		})
	}
}

func tokenUsageFrom(in, cachedInput, out, total int32) metrics.TokenUsage {
	var usage metrics.TokenUsage
	if in >= 0 {
		usage.SetInputTokens(uint32(in))
	}
	if cachedInput >= 0 {
		usage.SetCachedInputTokens(uint32(cachedInput))
	}
	if out >= 0 {
		usage.SetOutputTokens(uint32(out))
	}
	if total >= 0 {
		usage.SetTotalTokens(uint32(total))
	}
	return usage
}
