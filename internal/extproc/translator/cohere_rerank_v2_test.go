// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
)

type alwaysErrReader struct{}

func (alwaysErrReader) Read(_ []byte) (int, error) { return 0, errors.New("read error") }

func TestCohereToCohereTranslatorV2Rerank_RequestBody(t *testing.T) {
	for _, tc := range []struct {
		name              string
		modelNameOverride string
		onRetry           bool
		expPath           string
		expBodyContains   string
	}{
		{
			name:            "valid_body",
			expPath:         "/v2/rerank",
			expBodyContains: "",
		},
		{
			name:              "model_name_override",
			modelNameOverride: "rerank-english-v3",
			expPath:           "/v2/rerank",
			expBodyContains:   `"model":"rerank-english-v3"`,
		},
		{
			name:    "on_retry_no_change",
			onRetry: true,
			expPath: "/v2/rerank",
		},
		{
			name:              "model_name_override_with_retry",
			modelNameOverride: "rerank-multilingual-v3",
			onRetry:           true,
			expPath:           "/v2/rerank",
			expBodyContains:   `"model":"rerank-multilingual-v3"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewRerankCohereToCohereTranslator("v2", tc.modelNameOverride)
			originalBody := `{"model":"rerank-english-v3","query":"reset password","documents":["doc1","doc2"]}`
			var req cohereschema.RerankV2Request
			require.NoError(t, json.Unmarshal([]byte(originalBody), &req))

			headerMutation, bodyMutation, err := translator.RequestBody([]byte(originalBody), &req, tc.onRetry)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.GreaterOrEqual(t, len(headerMutation.SetHeaders), 1)
			require.Equal(t, ":path", headerMutation.SetHeaders[0].Header.Key)
			require.Equal(t, tc.expPath, string(headerMutation.SetHeaders[0].Header.RawValue))

			switch {
			case tc.expBodyContains != "":
				require.NotNil(t, bodyMutation)
				require.Contains(t, string(bodyMutation.GetBody()), tc.expBodyContains)
				// Verify content-length header is set.
				require.Len(t, headerMutation.SetHeaders, 2)
				require.Equal(t, "content-length", headerMutation.SetHeaders[1].Header.Key)
			case bodyMutation != nil:
				// If there's a body mutation (like on retry), content-length header should be set.
				require.Len(t, headerMutation.SetHeaders, 2)
				require.Equal(t, "content-length", headerMutation.SetHeaders[1].Header.Key)
			default:
				// No body mutation, only path header.
				require.Len(t, headerMutation.SetHeaders, 1)
			}
		})
	}
}

func TestCohereToCohereTranslatorV2Rerank_RequestBody_InvalidJSONCreatesBodyWithOverride(t *testing.T) {
	translator := NewRerankCohereToCohereTranslator("v2", "override-model")
	// Provide invalid JSON; sjson with Optimistic mode can still produce a body with the override.
	originalBody := []byte("not-json")
	var req cohereschema.RerankV2Request
	headerMutation, bodyMutation, err := translator.RequestBody(originalBody, &req, false)
	require.NoError(t, err)
	require.NotNil(t, headerMutation)
	require.NotNil(t, bodyMutation)
	// Body should contain the override model
	require.Contains(t, string(bodyMutation.GetBody()), `"model":"override-model"`)
	// Verify content-length header is set alongside :path
	require.GreaterOrEqual(t, len(headerMutation.SetHeaders), 2)
	require.Equal(t, ":path", headerMutation.SetHeaders[0].Header.Key)
	require.Equal(t, "/v2/rerank", string(headerMutation.SetHeaders[0].Header.RawValue))
	require.Equal(t, "content-length", headerMutation.SetHeaders[1].Header.Key)
}

func TestCohereToCohereTranslatorV2Rerank_RequestBody_SetModelNameError(t *testing.T) {
	orig := sjsonOptions
	sjsonOptions = &sjson.Options{Optimistic: false, ReplaceInPlace: false}
	t.Cleanup(func() { sjsonOptions = orig })

	translator := NewRerankCohereToCohereTranslator("v2", "override-model")
	// Use an array root to make setting an object key fail with Optimistic=false.
	originalBody := []byte("[]")
	var req cohereschema.RerankV2Request

	headerMutation, bodyMutation, err := translator.RequestBody(originalBody, &req, false)
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to set model name")
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
}

func TestCohereToCohereTranslatorV2Rerank_ResponseHeaders(t *testing.T) {
	translator := NewRerankCohereToCohereTranslator("v2", "")
	headerMutation, err := translator.ResponseHeaders(map[string]string{})
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestCohereToCohereTranslatorV2Rerank_ResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name          string
		responseBody  string
		expTokenUsage LLMTokenUsage
		expError      bool
	}{
		{
			name: "valid_response_input_only",
			responseBody: `{
"results": [{"index": 1, "relevance_score": 0.9}],
"id": "rr-123",
"meta": {"tokens": {"input_tokens": 25}}
}`,
			expTokenUsage: LLMTokenUsage{InputTokens: 25, OutputTokens: 0, TotalTokens: 25},
		},
		{
			name: "valid_response_with_output_tokens",
			responseBody: `{
"results": [{"index": 0, "relevance_score": 0.8}],
"id": "rr-456",
"meta": {"tokens": {"input_tokens": 10, "output_tokens": 2}}
}`,
			expTokenUsage: LLMTokenUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
		},
		{
			name:         "invalid_json",
			responseBody: `invalid json`,
			expError:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewRerankCohereToCohereTranslator("v2", "")
			headerMutation, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(
				map[string]string{contentTypeHeaderName: jsonContentType},
				strings.NewReader(tc.responseBody),
				true,
			)

			if tc.expError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expTokenUsage, tokenUsage)
			require.Empty(t, responseModel)
			require.Nil(t, headerMutation)
			require.Nil(t, bodyMutation)
		})
	}
}

func TestCohereToCohereTranslatorV2Rerank_ResponseError(t *testing.T) {
	translator := NewRerankCohereToCohereTranslator("v2", "")

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

		var cohereErr cohereschema.RerankV2Error
		require.NoError(t, json.Unmarshal(bodyMutation.GetBody(), &cohereErr))
		require.NotNil(t, cohereErr.Message)
		require.Equal(t, errorBody, *cohereErr.Message)
		require.NotNil(t, cohereErr.ID)
		require.Equal(t, "503", *cohereErr.ID)
	})

	t.Run("json_error_passthrough", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "400",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"error": {"message": "Invalid request"}}`

		headerMutation, bodyMutation, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
		require.Nil(t, headerMutation)
		require.Nil(t, bodyMutation)
	})

	t.Run("marshal_error", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "500",
			contentTypeHeaderName: "text/plain",
		}
		invalid := []byte{0xff, 0xfe, 0xfd}

		headerMutation, bodyMutation, err := translator.ResponseError(respHeaders, bytes.NewReader(invalid))
		require.NoError(t, err)
		require.NotNil(t, headerMutation)
		require.NotNil(t, bodyMutation)
		var cohereErr cohereschema.RerankV2Error
		require.NoError(t, json.Unmarshal(bodyMutation.GetBody(), &cohereErr))
		require.NotNil(t, cohereErr.Message)
		require.NotNil(t, cohereErr.ID)
	})

	t.Run("read_error", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "500",
			contentTypeHeaderName: "text/plain",
		}
		headerMutation, bodyMutation, err := translator.ResponseError(respHeaders, alwaysErrReader{})
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to read error body")
		require.Nil(t, headerMutation)
		require.Nil(t, bodyMutation)
	})
}
