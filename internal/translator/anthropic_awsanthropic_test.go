// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"testing"

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
				Model: tt.inputModel,
				Messages: []anthropicschema.MessageParam{
					{
						Role:    anthropicschema.MessageRoleUser,
						Content: anthropicschema.MessageContent{Text: "Hello"},
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
			pathHeader := headerMutation[len(headerMutation)-2]
			require.Equal(t, pathHeaderName, pathHeader.Key())
			expectedPath := "/model/" + tt.expectedInPath + "/invoke"
			assert.Equal(t, expectedPath, pathHeader.Value())

			// Check that model field is removed from body (since it's in the path).
			var modifiedReq map[string]any
			err = json.Unmarshal(bodyMutation, &modifiedReq)
			require.NoError(t, err)

			// Verify anthropic_version field is added (required by AWS Bedrock).
			version, hasVersion := modifiedReq["anthropic_version"]
			assert.True(t, hasVersion, "anthropic_version should be added for AWS Bedrock")
			assert.Equal(t, "bedrock-2023-05-31", version, "anthropic_version should match the configured version")
		})
	}
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
				Model: "anthropic.claude-3-sonnet-20240229-v1:0",
				Messages: []anthropicschema.MessageParam{
					{
						Role: anthropicschema.MessageRoleUser,
						Content: anthropicschema.MessageContent{
							Array: []anthropicschema.ContentBlockParam{
								{Text: &anthropicschema.TextBlockParam{Text: "Hello"}},
							},
						},
					},
				},
			}
			if tt.stream != nil {
				if streamVal, ok := tt.stream.(bool); ok {
					parsedReq.Stream = streamVal
				}
			}

			rawBody, err := json.Marshal(parsedReq)
			require.NoError(t, err)

			headerMutation, _, err := translator.RequestBody(rawBody, parsedReq, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)

			// Check path contains expected suffix.
			// Use the last element as it takes precedence when multiple headers are set.
			pathHeader := headerMutation[len(headerMutation)-2]
			expectedPath := "/model/anthropic.claude-3-sonnet-20240229-v1:0" + tt.expectedPathSuffix
			assert.Equal(t, expectedPath, pathHeader.Value())
		})
	}
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
				Model: tt.modelID,
				Messages: []anthropicschema.MessageParam{
					{
						Role: anthropicschema.MessageRoleUser,
						Content: anthropicschema.MessageContent{
							Array: []anthropicschema.ContentBlockParam{
								{Text: &anthropicschema.TextBlockParam{Text: "Hello"}},
							},
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
			pathHeader := headerMutation[len(headerMutation)-2]
			assert.Equal(t, pathHeaderName, pathHeader.Key())
			assert.Equal(t, tt.expectedPath, pathHeader.Value())
		})
	}
}
