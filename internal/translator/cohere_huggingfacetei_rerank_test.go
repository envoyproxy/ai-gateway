// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestCohereToHuggingFaceTEITranslatorV2Rerank_RequestBody(t *testing.T) {
	for _, tc := range []struct {
		name              string
		modelNameOverride string
		expModel          string
	}{
		{
			name:     "valid_body",
			expModel: "BAAI/bge-reranker-v2-m3",
		},
		{
			name:              "model_name_override",
			modelNameOverride: "override-model",
			expModel:          "override-model",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewRerankCohereToHuggingFaceTEITranslator(tc.modelNameOverride)
			originalBody := `{"model":"BAAI/bge-reranker-v2-m3","query":"What is machine learning?","documents":["ML is a subset of AI.","The weather is sunny."]}`
			var req cohereschema.RerankV2Request
			require.NoError(t, json.Unmarshal([]byte(originalBody), &req))

			headerMutation, bodyMutation, err := translator.RequestBody([]byte(originalBody), &req, false)
			require.NoError(t, err)
			require.Len(t, headerMutation, 2)
			require.Equal(t, pathHeaderName, headerMutation[0].Key())
			require.Equal(t, "/rerank", headerMutation[0].Value())
			require.Equal(t, contentLengthHeaderName, headerMutation[1].Key())

			require.JSONEq(t, `{"query":"What is machine learning?","texts":["ML is a subset of AI.","The weather is sunny."]}`,
				string(bodyMutation))
			require.Equal(t, tc.expModel, translator.(*cohereToHuggingFaceTEITranslatorV2Rerank).requestModel)
		})
	}
}

func TestCohereToHuggingFaceTEITranslatorV2Rerank_ResponseHeaders(t *testing.T) {
	translator := NewRerankCohereToHuggingFaceTEITranslator("")
	headerMutation, err := translator.ResponseHeaders(map[string]string{})
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestCohereToHuggingFaceTEITranslatorV2Rerank_ResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name         string
		responseBody string
		topN         *int
		expResults   []*cohereschema.RerankV2Result
		expError     bool
	}{
		{
			name:         "valid_response",
			responseBody: `[{"index":0,"score":0.999},{"index":1,"score":0.00002}]`,
			expResults: []*cohereschema.RerankV2Result{
				{Index: 0, RelevanceScore: 0.999},
				{Index: 1, RelevanceScore: 0.00002},
			},
		},
		{
			name:         "top_n_truncates",
			responseBody: `[{"index":2,"score":0.9},{"index":0,"score":0.5},{"index":1,"score":0.1}]`,
			topN:         ptr.To(2),
			expResults: []*cohereschema.RerankV2Result{
				{Index: 2, RelevanceScore: 0.9},
				{Index: 0, RelevanceScore: 0.5},
			},
		},
		{
			name:         "top_n_larger_than_results",
			responseBody: `[{"index":0,"score":0.9}]`,
			topN:         ptr.To(5),
			expResults: []*cohereschema.RerankV2Result{
				{Index: 0, RelevanceScore: 0.9},
			},
		},
		{
			name:         "empty_results",
			responseBody: `[]`,
			expResults:   []*cohereschema.RerankV2Result{},
		},
		{
			name:         "invalid_json",
			responseBody: `invalid json`,
			expError:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewRerankCohereToHuggingFaceTEITranslator("")
			impl := translator.(*cohereToHuggingFaceTEITranslatorV2Rerank)
			impl.requestModel = "BAAI/bge-reranker-v2-m3"
			impl.topN = tc.topN

			headerMutation, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(
				map[string]string{contentTypeHeaderName: jsonContentType},
				strings.NewReader(tc.responseBody),
				true,
				nil,
			)

			if tc.expError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			// TEI does not report token usage.
			require.Equal(t, tokenUsageFrom(-1, -1, -1, -1, -1, -1), tokenUsage)
			require.Equal(t, "BAAI/bge-reranker-v2-m3", responseModel)
			require.Len(t, headerMutation, 1)
			require.Equal(t, contentLengthHeaderName, headerMutation[0].Key())

			var resp cohereschema.RerankV2Response
			require.NoError(t, json.Unmarshal(bodyMutation, &resp))
			require.Equal(t, tc.expResults, resp.Results)
		})
	}
}

func TestCohereToHuggingFaceTEITranslatorV2Rerank_ResponseError(t *testing.T) {
	translator := NewRerankCohereToHuggingFaceTEITranslator("")

	t.Run("tei_json_error", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "413",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"error":"batch size 1025 > maximum allowed batch size 1024","error_type":"Validation"}`

		headerMutation, bodyMutation, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
		require.NotNil(t, headerMutation)

		var cohereErr cohereschema.RerankV2Error
		require.NoError(t, json.Unmarshal(bodyMutation, &cohereErr))
		require.NotNil(t, cohereErr.Message)
		require.Equal(t, "batch size 1025 > maximum allowed batch size 1024", *cohereErr.Message)
	})

	t.Run("non_json_error", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "503",
			contentTypeHeaderName: "text/plain",
		}
		errorBody := "Service Unavailable"

		headerMutation, bodyMutation, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
		require.NotNil(t, headerMutation)

		var cohereErr cohereschema.RerankV2Error
		require.NoError(t, json.Unmarshal(bodyMutation, &cohereErr))
		require.NotNil(t, cohereErr.Message)
		require.Equal(t, errorBody, *cohereErr.Message)
	})

	t.Run("json_error_unexpected_shape", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "400",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"message":"some other error shape"}`

		headerMutation, bodyMutation, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
		require.NotNil(t, headerMutation)

		// Falls back to wrapping the raw body.
		var cohereErr cohereschema.RerankV2Error
		require.NoError(t, json.Unmarshal(bodyMutation, &cohereErr))
		require.NotNil(t, cohereErr.Message)
		require.Equal(t, errorBody, *cohereErr.Message)
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

func TestCohereToHuggingFaceTEITranslatorV2Rerank_ResponseBody_RecordsResponseInSpan(t *testing.T) {
	mspan := &mockRerankSpanTranslator{}
	tr := NewRerankCohereToHuggingFaceTEITranslator("")
	tr.(*cohereToHuggingFaceTEITranslatorV2Rerank).requestModel = "BAAI/bge-reranker-v2-m3"

	body := `[{"index":0,"score":0.9}]`
	_, _, _, _, err := tr.ResponseBody(
		map[string]string{contentTypeHeaderName: jsonContentType},
		strings.NewReader(body),
		true,
		mspan,
	)
	require.NoError(t, err)
	require.True(t, mspan.recordCalled)
}
