// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicVertex "github.com/anthropics/anthropic-sdk-go/vertex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			translator := NewAnthropicToGCPAnthropicTranslator(tt.override)

			reqBody := map[string]interface{}{
				"model":    tt.inputModel,
				"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
			}
			rawBody, err := json.Marshal(reqBody)
			require.NoError(t, err)

			headerMutation, bodyMutation, err := translator.RequestBody(rawBody, nil, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.NotNil(t, bodyMutation)

			// Check path header contains expected model.
			pathHeader := headerMutation.SetHeaders[0]
			require.Equal(t, ":path", pathHeader.Header.Key)
			expectedPath := "publishers/anthropic/models/" + tt.expectedInPath + ":rawPredict"
			assert.Equal(t, expectedPath, string(pathHeader.Header.RawValue))

			// Check that model field is removed from body (since it's in the path).
			var modifiedReq map[string]interface{}
			err = json.Unmarshal(bodyMutation.GetBody(), &modifiedReq)
			require.NoError(t, err)
			_, hasModel := modifiedReq["model"]
			assert.False(t, hasModel, "model field should be removed from request body")
		})
	}
}

func TestAnthropicToGCPAnthropicTranslator_RequestBody_AnthropicVersionField(t *testing.T) {
	tests := []struct {
		name                     string
		inputAnthropicVersion    interface{}
		expectedAnthropicVersion string
		expectVersionAdded       bool
	}{
		{
			name:                     "adds default version when missing",
			inputAnthropicVersion:    nil,
			expectedAnthropicVersion: anthropicVertex.DefaultVersion,
			expectVersionAdded:       true,
		},
		{
			name:                     "preserves existing version",
			inputAnthropicVersion:    "vertex-2023-10-16",
			expectedAnthropicVersion: "vertex-2023-10-16",
			expectVersionAdded:       false,
		},
		{
			name:                     "preserves custom version",
			inputAnthropicVersion:    "custom-version-1.0",
			expectedAnthropicVersion: "custom-version-1.0",
			expectVersionAdded:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewAnthropicToGCPAnthropicTranslator("")

			reqBody := map[string]interface{}{
				"model":    "claude-3-sonnet-20240229",
				"messages": []map[string]interface{}{{"role": "user", "content": "Test"}},
			}

			if tt.inputAnthropicVersion != nil {
				reqBody["anthropic_version"] = tt.inputAnthropicVersion
			}

			rawBody, err := json.Marshal(reqBody)
			require.NoError(t, err)

			_, bodyMutation, err := translator.RequestBody(rawBody, nil, false)
			require.NoError(t, err)
			require.NotNil(t, bodyMutation)

			// Check anthropic_version in body.
			var modifiedReq map[string]interface{}
			err = json.Unmarshal(bodyMutation.GetBody(), &modifiedReq)
			require.NoError(t, err)

			version, exists := modifiedReq["anthropic_version"]
			assert.True(t, exists, "anthropic_version should always be present")
			assert.Equal(t, tt.expectedAnthropicVersion, version)
		})
	}
}

func TestAnthropicToGCPAnthropicTranslator_RequestBody_StreamingPaths(t *testing.T) {
	tests := []struct {
		name              string
		stream            interface{}
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
			translator := NewAnthropicToGCPAnthropicTranslator("")

			reqBody := map[string]interface{}{
				"model":    "claude-3-sonnet-20240229",
				"messages": []map[string]interface{}{{"role": "user", "content": "Test"}},
			}

			if tt.stream != nil {
				reqBody["stream"] = tt.stream
			}

			rawBody, err := json.Marshal(reqBody)
			require.NoError(t, err)

			headerMutation, _, err := translator.RequestBody(rawBody, nil, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)

			// Check path contains expected specifier.
			pathHeader := headerMutation.SetHeaders[0]
			expectedPath := "publishers/anthropic/models/claude-3-sonnet-20240229:" + tt.expectedSpecifier
			assert.Equal(t, expectedPath, string(pathHeader.Header.RawValue))
		})
	}
}

func TestAnthropicToGCPAnthropicTranslator_RequestBody_FieldPassthrough(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("")

	// Test that all fields are passed through unchanged (except model and anthropic_version).
	reqBody := map[string]interface{}{
		"model": "claude-3-sonnet-20240229",
		"messages": []map[string]interface{}{
			{"role": "user", "content": "Hello, world!"},
			{"role": "assistant", "content": "Hi there!"},
			{"role": "user", "content": "How are you?"},
		},
		"max_tokens":     1000,
		"temperature":    0.7,
		"top_p":          0.95,
		"top_k":          40,
		"stop_sequences": []string{"Human:", "Assistant:"},
		"stream":         false,
		"system":         "You are a helpful assistant",
		"tools": []map[string]interface{}{
			{
				"name":        "get_weather",
				"description": "Get weather info",
				"input_schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		"tool_choice": map[string]interface{}{"type": "auto"},
		"metadata":    map[string]interface{}{"user_id": "test123"},
		// Custom fields should also pass through.
		"custom_field":   "custom_value",
		"another_custom": 123,
	}

	rawBody, err := json.Marshal(reqBody)
	require.NoError(t, err)

	_, bodyMutation, err := translator.RequestBody(rawBody, nil, false)
	require.NoError(t, err)
	require.NotNil(t, bodyMutation)

	var modifiedReq map[string]interface{}
	err = json.Unmarshal(bodyMutation.GetBody(), &modifiedReq)
	require.NoError(t, err)

	// Messages should be preserved.
	assert.Len(t, modifiedReq["messages"], 3)

	// Numeric fields get converted to float64 by JSON unmarshalling.
	assert.Equal(t, float64(1000), modifiedReq["max_tokens"])
	assert.Equal(t, 0.7, modifiedReq["temperature"])
	assert.Equal(t, 0.95, modifiedReq["top_p"])
	assert.Equal(t, float64(40), modifiedReq["top_k"])
	assert.Equal(t, float64(123), modifiedReq["another_custom"])

	// Arrays become []interface{} by JSON unmarshalling.
	stopSeq, ok := modifiedReq["stop_sequences"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, stopSeq, 2)
	assert.Equal(t, "Human:", stopSeq[0])
	assert.Equal(t, "Assistant:", stopSeq[1])

	// Boolean values are preserved.
	assert.Equal(t, false, modifiedReq["stream"])

	// String values are preserved.
	assert.Equal(t, reqBody["system"], modifiedReq["system"])
	assert.Equal(t, reqBody["custom_field"], modifiedReq["custom_field"])

	// Complex objects should be preserved as maps.
	assert.NotNil(t, modifiedReq["tools"])
	assert.NotNil(t, modifiedReq["tool_choice"])
	assert.NotNil(t, modifiedReq["metadata"])

	// Verify model field is removed from body (it's in the path instead).
	_, hasModel := modifiedReq["model"]
	assert.False(t, hasModel, "model field should be removed from request body")

	// Verify anthropic_version is added.
	assert.Equal(t, anthropicVertex.DefaultVersion, modifiedReq["anthropic_version"])
}

func TestAnthropicToGCPAnthropicTranslator_RequestBody_ModelFallback(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("")

	reqBody := map[string]interface{}{
		"messages": []map[string]interface{}{{"role": "user", "content": "Test"}},
		// No model field.
	}

	rawBody, err := json.Marshal(reqBody)
	require.NoError(t, err)

	headerMutation, _, err := translator.RequestBody(rawBody, nil, false)
	require.NoError(t, err)
	require.NotNil(t, headerMutation)

	// Should use default fallback model in path.
	pathHeader := headerMutation.SetHeaders[0]
	expectedPath := "publishers/anthropic/models/claude-3-5-sonnet-20241022:rawPredict"
	assert.Equal(t, expectedPath, string(pathHeader.Header.RawValue))
}

func TestAnthropicToGCPAnthropicTranslator_RequestBody_InvalidJSON(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("")

	invalidJSON := []byte(`{"invalid": json"}`)

	_, _, err := translator.RequestBody(invalidJSON, nil, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal Anthropic request")
}

func TestAnthropicToGCPAnthropicTranslator_ResponseHeaders(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("")

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

func TestAnthropicToGCPAnthropicTranslator_ResponseBody_TokenUsageExtraction(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("")

	tests := []struct {
		name               string
		endOfStream        bool
		responseBody       interface{}
		expectedTokenUsage LLMTokenUsage
		expectError        bool
	}{
		{
			name:        "early stream chunk returns empty usage",
			endOfStream: false,
			responseBody: map[string]interface{}{
				"type": "content_block_delta",
			},
			expectedTokenUsage: LLMTokenUsage{},
			expectError:        false,
		},
		{
			name:        "end of stream with valid anthropic response",
			endOfStream: true,
			responseBody: anthropic.Message{
				ID:      "msg_123",
				Type:    "message",
				Role:    "assistant",
				Content: []anthropic.ContentBlockUnion{{Type: "text", Text: "Hello!"}},
				Model:   "claude-3-sonnet-20240229",
				Usage: anthropic.Usage{
					InputTokens:  100,
					OutputTokens: 50,
				},
			},
			expectedTokenUsage: LLMTokenUsage{
				InputTokens:  100,
				OutputTokens: 50,
				TotalTokens:  150,
			},
			expectError: false,
		},
		{
			name:        "end of stream with different token counts",
			endOfStream: true,
			responseBody: anthropic.Message{
				ID:      "msg_456",
				Type:    "message",
				Role:    "assistant",
				Content: []anthropic.ContentBlockUnion{{Type: "text", Text: "Longer response"}},
				Model:   "claude-3-opus-20240229",
				Usage: anthropic.Usage{
					InputTokens:  500,
					OutputTokens: 250,
				},
			},
			expectedTokenUsage: LLMTokenUsage{
				InputTokens:  500,
				OutputTokens: 250,
				TotalTokens:  750,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal response body.
			respBody, err := json.Marshal(tt.responseBody)
			require.NoError(t, err)

			bodyReader := bytes.NewReader(respBody)
			respHeaders := map[string]string{"content-type": "application/json"}

			headerMutation, bodyMutation, tokenUsage, err := translator.ResponseBody(respHeaders, bodyReader, tt.endOfStream)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedTokenUsage, tokenUsage)

			if tt.endOfStream {
				// For end of stream, should have mutations.
				assert.NotNil(t, headerMutation)
				assert.NotNil(t, bodyMutation)

				// Body should be passed through unchanged.
				assert.Equal(t, respBody, bodyMutation.GetBody())

				// Content-Length header should be set.
				contentLengthSet := false
				for _, header := range headerMutation.SetHeaders {
					if header.Header.Key == "content-length" {
						contentLengthSet = true
						break
					}
				}
				assert.True(t, contentLengthSet, "Content-Length header should be set")
			} else {
				// For non-end of stream, should return nil mutations.
				assert.Nil(t, headerMutation)
				assert.Nil(t, bodyMutation)
			}
		})
	}
}

func TestAnthropicToGCPAnthropicTranslator_ResponseBody_InvalidJSON(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("")

	invalidJSON := []byte(`{"invalid": json"}`)
	bodyReader := bytes.NewReader(invalidJSON)
	respHeaders := map[string]string{"content-type": "application/json"}

	headerMutation, bodyMutation, tokenUsage, err := translator.ResponseBody(respHeaders, bodyReader, true)

	// Should not error, but should pass through as-is with empty token usage.
	require.NoError(t, err)
	assert.NotNil(t, bodyMutation)
	assert.JSONEq(t, string(invalidJSON), string(bodyMutation.GetBody()))
	assert.Equal(t, LLMTokenUsage{}, tokenUsage)
	assert.Nil(t, headerMutation) // No content-length header for invalid JSON.
}

func TestAnthropicToGCPAnthropicTranslator_ResponseBody_ReadError(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("")

	// Create a reader that will fail.
	errorReader := &errorReader{}
	respHeaders := map[string]string{"content-type": "application/json"}

	_, _, _, err := translator.ResponseBody(respHeaders, errorReader, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read response body")
}

// errorReader implements io.Reader but always returns an error.
type errorReader struct{}

func (e *errorReader) Read(_ []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

func TestAnthropicToGCPAnthropicTranslator_ResponseBody_ZeroTokenUsage(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("")

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

	_, _, tokenUsage, err := translator.ResponseBody(respHeaders, bodyReader, true)
	require.NoError(t, err)

	expectedUsage := LLMTokenUsage{
		InputTokens:  0,
		OutputTokens: 0,
		TotalTokens:  0,
	}
	assert.Equal(t, expectedUsage, tokenUsage)
}
