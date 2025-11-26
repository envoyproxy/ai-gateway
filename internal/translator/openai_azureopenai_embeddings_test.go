// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

func TestOpenAIToAzureOpenAITranslatorV1EmbeddingRequestBody(t *testing.T) {
	for _, tc := range []struct {
		name              string
		modelNameOverride internalapi.ModelNameOverride
		onRetry           bool
		expPath           string
		expBodyContains   string
	}{
		{
			name:            "valid_body",
			expPath:         "/openai/deployments/text-embedding-ada-002/embeddings?api-version=2024-12-01-preview",
			expBodyContains: "",
		},
		{
			name:              "model_name_override",
			modelNameOverride: "custom-embedding-model",
			expPath:           "/openai/deployments/custom-embedding-model/embeddings?api-version=2024-12-01-preview",
		},
		{
			name:    "on_retry",
			onRetry: true,
			expPath: "/openai/deployments/text-embedding-ada-002/embeddings?api-version=2024-12-01-preview",
		},
		{
			name:              "model_name_override",
			modelNameOverride: "custom-embedding-model",
			onRetry:           true,
			expPath:           "/openai/deployments/custom-embedding-model/embeddings?api-version=2024-12-01-preview",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewEmbeddingOpenAIToAzureOpenAITranslator("2024-12-01-preview", tc.modelNameOverride)
			originalBody := `{"model":"text-embedding-ada-002","input":"test input"}`
			var req openai.EmbeddingRequest
			require.NoError(t, json.Unmarshal([]byte(originalBody), &req))

			headerMutation, bodyMutation, err := translator.RequestBody([]byte(originalBody), &req, tc.onRetry)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.GreaterOrEqual(t, len(headerMutation), 1)
			require.Equal(t, pathHeaderName, headerMutation[0].Key())
			require.Equal(t, tc.expPath, headerMutation[0].Value())

			switch {
			case tc.expBodyContains != "":
				require.NotNil(t, bodyMutation)
				require.Contains(t, string(bodyMutation), tc.expBodyContains)
				// Verify content-length header is set.
				require.Len(t, headerMutation, 2)
				require.Equal(t, contentLengthHeaderName, headerMutation[1].Key())
			case bodyMutation != nil:
				// If there's a body mutation (like on retry), content-length header should be set.
				require.Len(t, headerMutation, 2)
				require.Equal(t, contentLengthHeaderName, headerMutation[1].Key())
			default:
				// No body mutation, only path header.
				require.Len(t, headerMutation, 1)
			}
		})
	}
}

func TestOpenAIToAzureOpenAITranslatorV1EmbeddingResponseHeaders(t *testing.T) {
	translator := NewEmbeddingOpenAIToAzureOpenAITranslator("2024-12-01-preview", "")
	headerMutation, err := translator.ResponseHeaders(map[string]string{})
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestOpenAIToAzureOpenAITranslatorV1EmbeddingResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name           string
		responseBody   string
		responseStatus string
		expTokenUsage  metrics.TokenUsage
		expError       bool
	}{
		{
			name: "valid_response",
			responseBody: `{
				"object": "list",
				"data": [
					{
						"object": "embedding",
						"embedding": [0.1, 0.2, 0.3],
						"index": 0
					}
				],
				"model": "text-embedding-ada-002",
				"usage": {
					"prompt_tokens": 8,
					"total_tokens": 8
				}
			}`,
			expTokenUsage: tokenUsageFrom(8, -1, -1, 8),
		},
		{
			name:          "invalid_json",
			responseBody:  `invalid json`,
			expError:      true,
			expTokenUsage: tokenUsageFrom(-1, -1, -1, -1),
		},
		{
			name:           "error_response",
			responseBody:   `{"error": {"message": "Invalid input", "type": "BadRequestError"}}`,
			responseStatus: "400",
			expTokenUsage:  tokenUsageFrom(0, -1, -1, 0),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewEmbeddingOpenAIToAzureOpenAITranslator("2024-12-01-preview", "")
			respHeaders := map[string]string{
				"content-type": "application/json",
			}
			if tc.responseStatus != "" {
				respHeaders[statusHeaderName] = tc.responseStatus
			} else {
				respHeaders[statusHeaderName] = "200"
			}

			headerMutation, bodyMutation, tokenUsage, _, err := translator.ResponseBody(
				respHeaders,
				strings.NewReader(tc.responseBody),
				true,
				nil,
			)

			if tc.expError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expTokenUsage, tokenUsage)

			// Both error and success responses should have nil mutations for OpenAI to OpenAI translation.
			require.Nil(t, headerMutation)
			require.Nil(t, bodyMutation)
		})
	}
}

func TestOpenAIToAzureOpenAITranslatorV1EmbeddingResponseError(t *testing.T) {
	translator := NewEmbeddingOpenAIToAzureOpenAITranslator("2024-12-01-preview", "").(*openAIToAzureOpenAITranslatorV1Embedding)

	t.Run("non_json_error", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "503",
			contentTypeHeaderName: "text/plain",
		}
		errorBody := "Service Unavailable"

		headerMutation, bodyMutation, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
		require.NotNil(t, headerMutation)
		require.NotNil(t, bodyMutation)

		// Should convert to OpenAI error format.
		var openaiError openai.Error
		require.NoError(t, json.Unmarshal(bodyMutation, &openaiError))
		require.Equal(t, "error", openaiError.Type)
		require.Equal(t, openAIBackendError, openaiError.Error.Type)
		require.Equal(t, errorBody, openaiError.Error.Message)
		require.Equal(t, "503", *openaiError.Error.Code)
	})

	t.Run("json_error_passthrough", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "400",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"error": {"message": "Invalid input", "type": "BadRequestError"}}`

		headerMutation, bodyMutation, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
		require.Nil(t, headerMutation)
		require.Nil(t, bodyMutation)
	})
}
