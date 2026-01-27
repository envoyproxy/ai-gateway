// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

func TestOpenAIToGCPVertexAITranslatorV1Embedding_RequestBody(t *testing.T) {
	tests := []struct {
		name              string
		modelNameOverride internalapi.ModelNameOverride
		input             openai.EmbeddingRequest
		onRetry           bool
		wantError         bool
		wantPath          string
		wantBodyContains  []string // Substrings that should be present in the request body
	}{
		{
			name: "embedding request with string input",
			input: openai.EmbeddingRequest{
				Model: "text-embedding-004",
				Input: openai.EmbeddingRequestInput{
					Value: "This is a test text for embedding",
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:predict",
			wantBodyContains: []string{
				`"instances"`,
				`"content":"This is a test text for embedding"`,
				`"parameters"`,
			},
		},
		{
			name:              "embedding request with model override",
			modelNameOverride: "custom-embedding-model",
			input: openai.EmbeddingRequest{
				Model: "text-embedding-004",
				Input: openai.EmbeddingRequestInput{
					Value: "Test text",
				},
			},
			wantPath: "publishers/google/models/custom-embedding-model:predict",
			wantBodyContains: []string{
				`"instances"`,
				`"content":"Test text"`,
				`"parameters"`,
			},
		},
		{
			name: "embedding request with EmbeddingInputItem and task_type",
			input: openai.EmbeddingRequest{
				Model: "text-embedding-004",
				Input: openai.EmbeddingRequestInput{
					Value: openai.EmbeddingInputItem{
						Content:  openai.EmbeddingContent{Value: "This is a document for retrieval"},
						TaskType: "RETRIEVAL_DOCUMENT",
						Title:    "Document Title",
					},
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:predict",
			wantBodyContains: []string{
				`"instances"`,
				`"content":"This is a document for retrieval"`,
				`"task_type":"RETRIEVAL_DOCUMENT"`,
				`"title":"Document Title"`,
				`"parameters"`,
			},
		},
		{
			name: "embedding request with array of EmbeddingInputItem",
			input: openai.EmbeddingRequest{
				Model: "text-embedding-004",
				Input: openai.EmbeddingRequestInput{
					Value: []openai.EmbeddingInputItem{
						{
							Content:  openai.EmbeddingContent{Value: "Query about cats"},
							TaskType: "RETRIEVAL_QUERY",
						},
						{
							Content:  openai.EmbeddingContent{Value: "Document about dogs"},
							TaskType: "RETRIEVAL_DOCUMENT",
							Title:    "Dog Info",
						},
					},
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:predict",
			wantBodyContains: []string{
				`"instances"`,
				`"content":"Query about cats"`,
				`"task_type":"RETRIEVAL_QUERY"`,
				`"content":"Document about dogs"`,
				`"task_type":"RETRIEVAL_DOCUMENT"`,
				`"title":"Dog Info"`,
				`"parameters"`,
			},
		},
		{
			name: "embedding request with dimensions parameter",
			input: openai.EmbeddingRequest{
				Model: "text-embedding-004",
				Input: openai.EmbeddingRequestInput{
					Value: "Text for dimension testing",
				},
				Dimensions: &[]int{256}[0],
			},
			wantPath: "publishers/google/models/text-embedding-004:predict",
			wantBodyContains: []string{
				`"instances"`,
				`"content":"Text for dimension testing"`,
				`"parameters"`,
				`"outputDimensionality":256`,
			},
		},
		{
			name: "embedding request with SEMANTIC_SIMILARITY without title",
			input: openai.EmbeddingRequest{
				Model: "text-embedding-004",
				Input: openai.EmbeddingRequestInput{
					Value: openai.EmbeddingInputItem{
						Content:  openai.EmbeddingContent{Value: "Text for similarity check"},
						TaskType: "SEMANTIC_SIMILARITY",
						Title:    "This title should not appear",
					},
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:predict",
			wantBodyContains: []string{
				`"instances"`,
				`"content":"Text for similarity check"`,
				`"task_type":"SEMANTIC_SIMILARITY"`,
				`"parameters"`,
			},
		},
		{
			name: "embedding request with auto_truncate vendor field",
			input: openai.EmbeddingRequest{
				Model: "text-embedding-004",
				Input: openai.EmbeddingRequestInput{
					Value: "Test text for auto truncate",
				},
				GCPVertexAIEmbeddingVendorFields: &openai.GCPVertexAIEmbeddingVendorFields{
					AutoTruncate: true,
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:predict",
			wantBodyContains: []string{
				`"instances"`,
				`"content":"Test text for auto truncate"`,
				`"parameters"`,
				`"auto_truncate":true`,
			},
		},
		{
			name: "embedding request with global task_type overriding individual task_type",
			input: openai.EmbeddingRequest{
				Model: "text-embedding-004",
				Input: openai.EmbeddingRequestInput{
					Value: []openai.EmbeddingInputItem{
						{
							Content:  openai.EmbeddingContent{Value: "Query text"},
							TaskType: "RETRIEVAL_DOCUMENT", // This should be overridden
						},
						{
							Content: openai.EmbeddingContent{Value: "Another text"},
							// No task type specified
						},
					},
				},
				GCPVertexAIEmbeddingVendorFields: &openai.GCPVertexAIEmbeddingVendorFields{
					TaskType: "RETRIEVAL_QUERY", // Global task type
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:predict",
			wantBodyContains: []string{
				`"instances"`,
				`"content":"Query text"`,
				`"task_type":"RETRIEVAL_QUERY"`,
				`"content":"Another text"`,
				`"parameters"`,
			},
		},
		{
			name: "embedding request with array of strings",
			input: openai.EmbeddingRequest{
				Model: "text-embedding-004",
				Input: openai.EmbeddingRequestInput{
					Value: []string{"First text", "Second text", "Third text"},
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:predict",
			wantBodyContains: []string{
				`"instances"`,
				`"content":"First text"`,
				`"content":"Second text"`,
				`"content":"Third text"`,
				`"parameters"`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewEmbeddingOpenAIToGCPVertexAITranslator("text-embedding-004", tc.modelNameOverride)
			originalBody, _ := json.Marshal(tc.input)

			headerMut, bodyMut, err := translator.RequestBody(originalBody, &tc.input, tc.onRetry)

			if tc.wantError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, headerMut)
			require.Len(t, headerMut, 2) // path and content-length headers

			// Check path header
			require.Equal(t, pathHeaderName, headerMut[0].Key())
			require.Equal(t, tc.wantPath, headerMut[0].Value())

			// Check content-length header
			require.Equal(t, contentLengthHeaderName, headerMut[1].Key())

			// Check body content
			require.NotNil(t, bodyMut)
			bodyStr := string(bodyMut)
			for _, substr := range tc.wantBodyContains {
				require.Contains(t, bodyStr, substr)
			}
		})
	}
}

func TestOpenAIToGCPVertexAITranslatorV1Embedding_ResponseHeaders(t *testing.T) {
	translator := NewEmbeddingOpenAIToGCPVertexAITranslator("text-embedding-004", "")

	headerMut, err := translator.ResponseHeaders(map[string]string{
		"content-type": "application/json",
	})

	require.NoError(t, err)
	require.Nil(t, headerMut) // No header transformations needed for embeddings
}

func TestOpenAIToGCPVertexAITranslatorV1Embedding_ResponseBody(t *testing.T) {
	tests := []struct {
		name             string
		gcpResponse      string
		wantError        bool
		wantTokenUsage   metrics.TokenUsage
		wantResponseBody openai.EmbeddingResponse
	}{
		{
			name: "successful response with embedding data",
			gcpResponse: `{
				"predictions": [
					{
						"embeddings": {
							"values": [0.1, 0.2, 0.3, 0.4, 0.5],
							"statistics": {
								"token_count": 5,
								"truncated": false
							}
						}
					}
				]
			}`,
			wantTokenUsage: tokenUsageFrom(5, -1, -1, -1, 5),
			wantResponseBody: openai.EmbeddingResponse{
				Object: "list",
				Model:  "text-embedding-004",
				Data: []openai.Embedding{
					{
						Object:    "embedding",
						Index:     0,
						Embedding: openai.EmbeddingUnion{Value: []float64{0.1, 0.2, 0.3, 0.4, 0.5}},
						Truncated: false,
					},
				},
				Usage: openai.EmbeddingUsage{
					PromptTokens: 5,
					TotalTokens:  5,
				},
			},
		},
		{
			name: "response with no embedding data",
			gcpResponse: `{
				"predictions": null
			}`,
			wantTokenUsage: tokenUsageFrom(0, -1, -1, -1, 0),
			wantResponseBody: openai.EmbeddingResponse{
				Object: "list",
				Model:  "text-embedding-004",
				Data:   []openai.Embedding{},
				Usage: openai.EmbeddingUsage{
					PromptTokens: 0,
					TotalTokens:  0,
				},
			},
		},
		{
			name: "response with empty embedding values",
			gcpResponse: `{
				"predictions": [
					{
						"embeddings": {
							"values": [],
							"statistics": {
								"token_count": 3,
								"truncated": false
							}
						}
					}
				]
			}`,
			wantTokenUsage: tokenUsageFrom(3, -1, -1, -1, 3),
			wantResponseBody: openai.EmbeddingResponse{
				Object: "list",
				Model:  "text-embedding-004",
				Data: []openai.Embedding{
					{
						Object:    "embedding",
						Index:     0,
						Embedding: openai.EmbeddingUnion{Value: []float64{}},
						Truncated: false,
					},
				},
				Usage: openai.EmbeddingUsage{
					PromptTokens: 3,
					TotalTokens:  3,
				},
			},
		},
		{
			name: "invalid JSON response",
			gcpResponse: `{
				"embedding": invalid json
			}`,
			wantError: true,
		},
		{
			name: "response with multiple embeddings",
			gcpResponse: `{
				"predictions": [
					{
						"embeddings": {
							"values": [0.1, 0.2, 0.3],
							"statistics": {
								"token_count": 3,
								"truncated": false
							}
						}
					},
					{
						"embeddings": {
							"values": [0.4, 0.5, 0.6],
							"statistics": {
								"token_count": 4,
								"truncated": false
							}
						}
					},
					{
						"embeddings": {
							"values": [0.7, 0.8, 0.9],
							"statistics": {
								"token_count": 5,
								"truncated": true
							}
						}
					}
				]
			}`,
			wantTokenUsage: tokenUsageFrom(12, -1, -1, -1, 12), // 3 + 4 + 5 = 12
			wantResponseBody: openai.EmbeddingResponse{
				Object: "list",
				Model:  "text-embedding-004",
				Data: []openai.Embedding{
					{
						Object:    "embedding",
						Index:     0,
						Embedding: openai.EmbeddingUnion{Value: []float64{0.1, 0.2, 0.3}},
						Truncated: false,
					},
					{
						Object:    "embedding",
						Index:     1,
						Embedding: openai.EmbeddingUnion{Value: []float64{0.4, 0.5, 0.6}},
						Truncated: false,
					},
					{
						Object:    "embedding",
						Index:     2,
						Embedding: openai.EmbeddingUnion{Value: []float64{0.7, 0.8, 0.9}},
						Truncated: true,
					},
				},
				Usage: openai.EmbeddingUsage{
					PromptTokens: 12,
					TotalTokens:  12,
				},
			},
		},
		{
			name: "response with truncated embedding",
			gcpResponse: `{
				"predictions": [
					{
						"embeddings": {
							"values": [0.1, 0.2],
							"statistics": {
								"token_count": 10,
								"truncated": true
							}
						}
					}
				]
			}`,
			wantTokenUsage: tokenUsageFrom(10, -1, -1, -1, 10),
			wantResponseBody: openai.EmbeddingResponse{
				Object: "list",
				Model:  "text-embedding-004",
				Data: []openai.Embedding{
					{
						Object:    "embedding",
						Index:     0,
						Embedding: openai.EmbeddingUnion{Value: []float64{0.1, 0.2}},
						Truncated: true,
					},
				},
				Usage: openai.EmbeddingUsage{
					PromptTokens: 10,
					TotalTokens:  10,
				},
			},
		},
		{
			name: "response with missing statistics",
			gcpResponse: `{
				"predictions": [
					{
						"embeddings": {
							"values": [0.1, 0.2, 0.3]
						}
					}
				]
			}`,
			wantTokenUsage: tokenUsageFrom(0, -1, -1, -1, 0), // No statistics provided
			wantResponseBody: openai.EmbeddingResponse{
				Object: "list",
				Model:  "text-embedding-004",
				Data: []openai.Embedding{
					{
						Object:    "embedding",
						Index:     0,
						Embedding: openai.EmbeddingUnion{Value: []float64{0.1, 0.2, 0.3}},
						Truncated: false,
					},
				},
				Usage: openai.EmbeddingUsage{
					PromptTokens: 0,
					TotalTokens:  0,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewEmbeddingOpenAIToGCPVertexAITranslator("text-embedding-004", "").(*openAIToGCPVertexAITranslatorV1Embedding)

			// Initialize the requestModel field (normally done in RequestBody)
			translator.requestModel = "text-embedding-004"

			headerMut, bodyMut, tokenUsage, responseModel, err := translator.ResponseBody(
				map[string]string{"content-type": "application/json"},
				strings.NewReader(tc.gcpResponse),
				true,
				nil,
			)

			if tc.wantError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, headerMut)
			require.Len(t, headerMut, 1) // content-length header
			require.Equal(t, contentLengthHeaderName, headerMut[0].Key())
			require.NotNil(t, bodyMut)
			require.Equal(t, "text-embedding-004", responseModel)

			// Check token usage
			if diff := cmp.Diff(tc.wantTokenUsage, tokenUsage, cmp.AllowUnexported(metrics.TokenUsage{})); diff != "" {
				t.Errorf("TokenUsage mismatch (-want +got):\n%s", diff)
			}

			// Parse and check response body
			var actualResponse openai.EmbeddingResponse
			err = json.Unmarshal(bodyMut, &actualResponse)
			require.NoError(t, err)

			// Check everything except the embedding values first
			require.Equal(t, tc.wantResponseBody.Object, actualResponse.Object)
			require.Equal(t, tc.wantResponseBody.Model, actualResponse.Model)
			require.Equal(t, tc.wantResponseBody.Usage, actualResponse.Usage)
			require.Len(t, actualResponse.Data, len(tc.wantResponseBody.Data))

			// For embedding values, check with tolerance due to float32->float64 conversion
			for idx := range tc.wantResponseBody.Data {
				require.Equal(t, tc.wantResponseBody.Data[idx].Object, actualResponse.Data[idx].Object)
				require.Equal(t, tc.wantResponseBody.Data[idx].Index, actualResponse.Data[idx].Index)
				require.Equal(t, tc.wantResponseBody.Data[idx].Truncated, actualResponse.Data[idx].Truncated)

				wantEmbedding := tc.wantResponseBody.Data[idx].Embedding.Value.([]float64)
				actualEmbedding := actualResponse.Data[idx].Embedding.Value.([]float64)
				require.Len(t, actualEmbedding, len(wantEmbedding))

				for i, wantVal := range wantEmbedding {
					require.InDelta(t, wantVal, actualEmbedding[i], 1e-6, "Embedding %d value at index %d", idx, i)
				}
			}
		})
	}
}

func TestOpenAIToGCPVertexAITranslatorV1Embedding_ResponseError(t *testing.T) {
	tests := []struct {
		name      string
		headers   map[string]string
		body      string
		wantError openai.Error
	}{
		{
			name: "GCP error response with structured error",
			headers: map[string]string{
				statusHeaderName: "400",
			},
			body: `{
				"error": {
					"code": 400,
					"message": "Invalid embedding request",
					"status": "INVALID_ARGUMENT"
				}
			}`,
			wantError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    "INVALID_ARGUMENT",
					Message: "Invalid embedding request",
					Code:    &[]string{"400"}[0],
				},
			},
		},
		{
			name: "plain text error response",
			headers: map[string]string{
				statusHeaderName: "503",
			},
			body: "Service temporarily unavailable",
			wantError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    gcpVertexAIBackendError,
					Message: "Service temporarily unavailable",
					Code:    &[]string{"503"}[0],
				},
			},
		},
		{
			name: "JSON error response without proper GCP structure",
			headers: map[string]string{
				statusHeaderName: "429",
			},
			body: `{
				"error": {
					"message": "Rate limit exceeded"
				}
			}`,
			wantError: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    "",
					Message: "Rate limit exceeded",
					Code:    &[]string{"429"}[0],
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewEmbeddingOpenAIToGCPVertexAITranslator("text-embedding-004", "").(*openAIToGCPVertexAITranslatorV1Embedding)

			headerMut, bodyMut, err := translator.ResponseError(tc.headers, strings.NewReader(tc.body))

			require.NoError(t, err)
			require.NotNil(t, headerMut)
			require.NotNil(t, bodyMut)

			// Parse the error response
			var actualError openai.Error
			err = json.Unmarshal(bodyMut, &actualError)
			require.NoError(t, err)

			if diff := cmp.Diff(tc.wantError, actualError); diff != "" {
				t.Errorf("Error response mismatch (-want +got):\n%s", diff)
			}

			// Check that content-type and content-length headers are set
			require.Len(t, headerMut, 2)
			require.Equal(t, contentTypeHeaderName, headerMut[0].Key())
			// Use JSONEq by wrapping values as JSON strings
			require.JSONEq(t, fmt.Sprintf(`"%s"`, jsonContentType), fmt.Sprintf(`"%s"`, headerMut[0].Value()))
			require.Equal(t, contentLengthHeaderName, headerMut[1].Key())
		})
	}
}

// TestResponseModel_GCPVertexAIEmbeddings tests that GCP Vertex AI embeddings returns the request model
func TestResponseModel_GCPVertexAIEmbeddings(t *testing.T) {
	modelName := "text-embedding-004"
	translator := NewEmbeddingOpenAIToGCPVertexAITranslator(modelName, "")

	// Initialize translator with embedding request
	req := &openai.EmbeddingRequest{
		Model: "text-embedding-004",
		Input: openai.EmbeddingRequestInput{Value: "test"},
	}
	reqBody, _ := json.Marshal(req)
	_, _, err := translator.RequestBody(reqBody, req, false)
	require.NoError(t, err)

	// GCP VertexAI embedding response
	embeddingResponse := `{
		"predictions": [
			{
				"embeddings": {
					"values": [0.1, 0.2, 0.3],
					"statistics": {
						"token_count": 3,
						"truncated": false
					}
				}
			}
		]
	}`

	_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(embeddingResponse)), true, nil)
	require.NoError(t, err)
	require.Equal(t, modelName, responseModel) // Returns the request model

	// Token usage should be provided from the statistics field
	inputTokens, ok := tokenUsage.InputTokens()
	require.True(t, ok)
	require.Equal(t, uint32(3), inputTokens)
	_, ok = tokenUsage.OutputTokens()
	require.False(t, ok) // Output tokens not available for embeddings
}
