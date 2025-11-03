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

			originalReq := &anthropicschema.MessagesRequest{Model: tt.inputModel}

			headerMutation, bodyMutation, err := translator.RequestBody(nil, originalReq, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.NotNil(t, bodyMutation)

			// Check path header contains expected model.
			pathHeader := headerMutation.SetHeaders[0]
			require.Equal(t, ":path", pathHeader.Header.Key)
			expectedPath := "publishers/anthropic/models/" + tt.expectedInPath + ":rawPredict"
			assert.Equal(t, expectedPath, string(pathHeader.Header.RawValue))

			// Check that model field is removed from body (since it's in the path).
			var modifiedReq map[string]any
			err = json.Unmarshal(bodyMutation.GetBody(), &modifiedReq)
			require.NoError(t, err)
			_, hasModel := modifiedReq["model"]
			assert.False(t, hasModel, "model field should be removed from request body")
		})
	}
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

			originalReq := &anthropicschema.MessagesRequest{Model: "claude-3-sonnet-20240229"}

			_, bodyMutation, err := translator.RequestBody(nil, originalReq, false)

			if tt.shouldError {
				require.Error(t, err)
				require.Contains(t, err.Error(), "anthropic_version is required for GCP Vertex AI")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, bodyMutation)

			var outputReq map[string]any
			err = json.Unmarshal(bodyMutation.GetBody(), &outputReq)
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

			reqBody := map[string]any{"model": "claude-3-sonnet-20240229"}

			if tt.stream != nil {
				reqBody["stream"] = tt.stream
			}

			parsedReq := &anthropicschema.MessagesRequest{
				Model: "claude-3-sonnet-20240229",
			}
			if tt.stream != nil {
				if streamVal, ok := tt.stream.(bool); ok {
					parsedReq.Stream = streamVal
				}
			}

			headerMutation, _, err := translator.RequestBody(nil, parsedReq, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)

			// Check path contains expected specifier.
			pathHeader := headerMutation.SetHeaders[0]
			expectedPath := "publishers/anthropic/models/claude-3-sonnet-20240229:" + tt.expectedSpecifier
			assert.Equal(t, expectedPath, string(pathHeader.Header.RawValue))
		})
	}
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

	_, _, tokenUsage, _, err := translator.ResponseBody(respHeaders, bodyReader, true)
	require.NoError(t, err)

	expectedUsage := LLMTokenUsage{
		InputTokens:  0,
		OutputTokens: 0,
		TotalTokens:  0,
	}
	assert.Equal(t, expectedUsage, tokenUsage)
}

func TestAnthropicToGCPAnthropicTranslator_ResponseBody_StreamingTokenUsage(t *testing.T) {
	translator := NewAnthropicToGCPAnthropicTranslator("2023-06-01", "")
	translator.(*anthropicToGCPAnthropicTranslator).stream = true

	tests := []struct {
		name          string
		chunk         string
		endOfStream   bool
		expectedUsage LLMTokenUsage
	}{
		{
			name:        "regular streaming chunk without usage",
			chunk:       "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" to me.\"}}\n\n",
			endOfStream: false,
			expectedUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
		{
			name:        "message_delta chunk with token usage",
			chunk:       "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":84}}\n\n",
			endOfStream: false,
			expectedUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 84,
				TotalTokens:  84,
			},
		},
		{
			name:        "message_stop chunk without usage",
			chunk:       "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			endOfStream: false,
			expectedUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
		{
			name:        "invalid json chunk",
			chunk:       "event: invalid\ndata: {\"invalid\": \"json\"}\n\n",
			endOfStream: false,
			expectedUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
		{
			name:        "message_delta with decimal output_tokens",
			chunk:       "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":42.0}}\n\n",
			endOfStream: false,
			expectedUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 42,
				TotalTokens:  42,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyReader := bytes.NewReader([]byte(tt.chunk))
			respHeaders := map[string]string{"content-type": "application/json"}

			headerMutation, bodyMutation, tokenUsage, _, err := translator.ResponseBody(respHeaders, bodyReader, tt.endOfStream)

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
		expectedUsage LLMTokenUsage
	}{
		{
			name:  "message_start without message field",
			chunk: "event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
			expectedUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
		{
			name:  "message_start without usage field",
			chunk: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\"}}\n\n",
			expectedUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
		{
			name:  "message_delta without usage field",
			chunk: "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
			expectedUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
		{
			name:  "invalid json in data",
			chunk: "event: message_start\ndata: {invalid json}\n\n",
			expectedUsage: LLMTokenUsage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyReader := bytes.NewReader([]byte(tt.chunk))
			respHeaders := map[string]string{"content-type": "application/json"}

			headerMutation, bodyMutation, tokenUsage, _, err := translator.ResponseBody(respHeaders, bodyReader, false)

			require.NoError(t, err)
			require.Nil(t, headerMutation)
			require.Nil(t, bodyMutation)
			require.Equal(t, tt.expectedUsage, tokenUsage)
		})
	}
}
