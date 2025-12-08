// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
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
			name: "embedding completion request with string input",
			input: openai.EmbeddingRequest{
				OfCompletion: &openai.EmbeddingCompletionRequest{
					Model: "text-embedding-004",
					Input: openai.EmbeddingRequestInput{
						Value: "This is a test text for embedding",
					},
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:embedContent",
			wantBodyContains: []string{
				`"content"`,
				`"parts"`,
				`"text":"This is a test text for embedding"`,
				`"config"`,
			},
		},
		{
			name:              "embedding completion request with model override",
			modelNameOverride: "custom-embedding-model",
			input: openai.EmbeddingRequest{
				OfCompletion: &openai.EmbeddingCompletionRequest{
					Model: "text-embedding-004",
					Input: openai.EmbeddingRequestInput{
						Value: "Test text",
					},
				},
			},
			wantPath: "publishers/google/models/custom-embedding-model:embedContent",
			wantBodyContains: []string{
				`"content"`,
				`"text":"Test text"`,
				`"config"`,
			},
		},
		{
			name: "embedding chat request",
			input: openai.EmbeddingRequest{
				OfChat: &openai.EmbeddingChatRequest{
					Model: "text-embedding-004",
					Messages: []openai.ChatCompletionMessageParamUnion{
						{
							OfUser: &openai.ChatCompletionUserMessageParam{
								Content: openai.StringOrUserRoleContentUnion{Value: "Hello"},
								Role:    openai.ChatMessageRoleUser,
							},
						},
						{
							OfAssistant: &openai.ChatCompletionAssistantMessageParam{
								Content: openai.StringOrAssistantRoleContentUnion{Value: "Hi there!"},
								Role:    openai.ChatMessageRoleAssistant,
							},
						},
					},
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:embedContent",
			wantBodyContains: []string{
				`"content"`,
				`"text":"Hello\nHi there!"`,
				`"config"`,
			},
		},
		{
			name: "embedding chat request with system message",
			input: openai.EmbeddingRequest{
				OfChat: &openai.EmbeddingChatRequest{
					Model: "text-embedding-004",
					Messages: []openai.ChatCompletionMessageParamUnion{
						{
							OfSystem: &openai.ChatCompletionSystemMessageParam{
								Content: openai.ContentUnion{Value: "System prompt"},
								Role:    openai.ChatMessageRoleSystem,
							},
						},
						{
							OfUser: &openai.ChatCompletionUserMessageParam{
								Content: openai.StringOrUserRoleContentUnion{Value: "User message"},
								Role:    openai.ChatMessageRoleUser,
							},
						},
					},
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:embedContent",
			wantBodyContains: []string{
				`"content"`,
				`"text":"System prompt\nUser message"`,
				`"config"`,
			},
		},
		{
			name: "embedding completion request with explicit task type",
			input: openai.EmbeddingRequest{
				OfCompletion: &openai.EmbeddingCompletionRequest{
					Model: "text-embedding-004",
					Input: openai.EmbeddingRequestInput{
						Value: "This is a document to be indexed",
					},
					GCPVertexAIEmbeddingVendorFields: &openai.GCPVertexAIEmbeddingVendorFields{
						TaskType: "RETRIEVAL_DOCUMENT",
					},
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:embedContent",
			wantBodyContains: []string{
				`"content"`,
				`"parts"`,
				`"text":"This is a document to be indexed"`,
				`"config"`,
				`"taskType":"RETRIEVAL_DOCUMENT"`,
			},
		},
		{
			name: "embedding chat request with explicit task type",
			input: openai.EmbeddingRequest{
				OfChat: &openai.EmbeddingChatRequest{
					Model: "text-embedding-004",
					Messages: []openai.ChatCompletionMessageParamUnion{
						{
							OfUser: &openai.ChatCompletionUserMessageParam{
								Content: openai.StringOrUserRoleContentUnion{Value: "What is machine learning?"},
								Role:    openai.ChatMessageRoleUser,
							},
						},
					},
					GCPVertexAIEmbeddingVendorFields: &openai.GCPVertexAIEmbeddingVendorFields{
						TaskType: "SEMANTIC_SIMILARITY",
					},
				},
			},
			wantPath: "publishers/google/models/text-embedding-004:embedContent",
			wantBodyContains: []string{
				`"content"`,
				`"text":"What is machine learning?"`,
				`"config"`,
				`"taskType":"SEMANTIC_SIMILARITY"`,
			},
		},
		{
			name: "invalid request - neither completion nor chat",
			input: openai.EmbeddingRequest{
				// Both OfCompletion and OfChat are nil
			},
			wantError: true,
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
				"embeddings": [
					{
						"values": [0.1, 0.2, 0.3, 0.4, 0.5]
					}
				]
			}`,
			wantTokenUsage: tokenUsageFrom(-1, -1, -1, -1), // GCP doesn't provide token usage for embeddings
			wantResponseBody: openai.EmbeddingResponse{
				Object: "list",
				Model:  "text-embedding-004",
				Data: []openai.Embedding{
					{
						Object:    "embedding",
						Index:     0,
						Embedding: openai.EmbeddingUnion{Value: []float64{0.1, 0.2, 0.3, 0.4, 0.5}},
					},
				},
				Usage: openai.EmbeddingUsage{
					PromptTokens: 0,
					TotalTokens:  0,
				},
			},
		},
		{
			name: "response with no embedding data",
			gcpResponse: `{
				"embeddings": null
			}`,
			wantTokenUsage: tokenUsageFrom(-1, -1, -1, -1),
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
				"embeddings": [
					{
						"values": []
					}
				]
			}`,
			wantTokenUsage: tokenUsageFrom(-1, -1, -1, -1),
			wantResponseBody: openai.EmbeddingResponse{
				Object: "list",
				Model:  "text-embedding-004",
				Data: []openai.Embedding{
					{
						Object:    "embedding",
						Index:     0,
						Embedding: openai.EmbeddingUnion{Value: []float64{}},
					},
				},
				Usage: openai.EmbeddingUsage{
					PromptTokens: 0,
					TotalTokens:  0,
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
			require.Nil(t, headerMut) // No header mutations for embeddings
			require.NotNil(t, bodyMut)
			require.Equal(t, "text-embedding-004", string(responseModel))

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
			if len(tc.wantResponseBody.Data) > 0 && len(actualResponse.Data) > 0 {
				wantEmbedding := tc.wantResponseBody.Data[0].Embedding.Value.([]float64)
				actualEmbedding := actualResponse.Data[0].Embedding.Value.([]float64)
				require.Len(t, actualEmbedding, len(wantEmbedding))

				for i, wantVal := range wantEmbedding {
					require.InDelta(t, wantVal, actualEmbedding[i], 1e-6, "Embedding value at index %d", i)
				}
				require.Equal(t, tc.wantResponseBody.Data[0].Object, actualResponse.Data[0].Object)
				require.Equal(t, tc.wantResponseBody.Data[0].Index, actualResponse.Data[0].Index)
			}
		})
	}
}

func TestOpenAIToGCPVertexAITranslatorV1Embedding_ResponseError(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		body        string
		wantError   openai.Error
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
				statusHeaderName:      "503",
				contentTypeHeaderName: "text/plain",
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
			require.Equal(t, jsonContentType, headerMut[0].Value())
			require.Equal(t, contentLengthHeaderName, headerMut[1].Key())
		})
	}
}

func TestInputToGeminiContent(t *testing.T) {
	tests := []struct {
		name        string
		input       openai.EmbeddingRequestInput
		wantContent *genai.Content
		wantError   bool
	}{
		{
			name: "string input",
			input: openai.EmbeddingRequestInput{
				Value: "This is a test string",
			},
			wantContent: &genai.Content{
				Parts: []*genai.Part{
					{Text: "This is a test string"},
				},
			},
		},
		{
			name: "string array input",
			input: openai.EmbeddingRequestInput{
				Value: []string{"first", "second", "third"},
			},
			wantContent: &genai.Content{
				Parts: []*genai.Part{
					{Text: "first"},
					{Text: "second"},
					{Text: "third"},
				},
			},
		},
		{
			name: "unsupported type - token array",
			input: openai.EmbeddingRequestInput{
				Value: []int64{1, 2, 3, 4, 5},
			},
			wantError: true,
		},
		{
			name: "unsupported type - token array batch",
			input: openai.EmbeddingRequestInput{
				Value: [][]int64{{1, 2}, {3, 4}, {5, 6}},
			},
			wantError: true,
		},
		{
			name: "unsupported type - int",
			input: openai.EmbeddingRequestInput{
				Value: 12345,
			},
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content, err := InputToGeminiContent(tc.input)

			if tc.wantError {
				require.Error(t, err)
				require.Nil(t, content)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.wantContent, content)
		})
	}
}

// TestResponseModel_GCPVertexAIEmbeddings tests that GCP Vertex AI embeddings returns the request model
func TestResponseModel_GCPVertexAIEmbeddings(t *testing.T) {
	modelName := "text-embedding-004"
	translator := NewEmbeddingOpenAIToGCPVertexAITranslator(modelName, "")

	// Initialize translator with embedding request
	req := &openai.EmbeddingRequest{
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model: "text-embedding-004",
			Input: openai.EmbeddingRequestInput{Value: "test"},
		},
	}
	reqBody, _ := json.Marshal(req)
	_, _, err := translator.RequestBody(reqBody, req, false)
	require.NoError(t, err)

	// GCP VertexAI embedding response
	embeddingResponse := `{
		"embedding": {
			"values": [0.1, 0.2, 0.3]
		}
	}`

	_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(embeddingResponse)), true, nil)
	require.NoError(t, err)
	require.Equal(t, modelName, string(responseModel)) // Returns the request model

	// Token usage should be default values since GCP doesn't provide detailed usage for embeddings
	_, ok := tokenUsage.InputTokens()
	require.False(t, ok) // Should not be available
	_, ok = tokenUsage.OutputTokens()
	require.False(t, ok) // Should not be available
}

func TestEmbeddingChatToGeminiMessage_ComplexMessages(t *testing.T) {
	req := &openai.EmbeddingChatRequest{
		Model: "text-embedding-004",
		Messages: []openai.ChatCompletionMessageParamUnion{
			{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ContentUnion{Value: "You are a helpful assistant"},
					Role:    openai.ChatMessageRoleSystem,
				},
			},
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{Value: "What is the weather?"},
					Role:    openai.ChatMessageRoleUser,
				},
			},
			{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content: openai.StringOrAssistantRoleContentUnion{Value: "I need location to check weather"},
					Role:    openai.ChatMessageRoleAssistant,
				},
			},
			{
				OfTool: &openai.ChatCompletionToolMessageParam{
					Content: openai.ContentUnion{Value: "Tool response data"},
					Role:    openai.ChatMessageRoleTool,
					ToolCallID: "tool_call_123",
				},
			},
			{
				OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
					Content: openai.ContentUnion{Value: "Debug info"},
					Role:    openai.ChatMessageRoleDeveloper,
				},
			},
		},
	}

	result, err := openAIEmbeddingChatToGeminiMessage(req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Content)
	require.Len(t, result.Content.Parts, 1)

	expectedText := "You are a helpful assistant\nWhat is the weather?\nI need location to check weather\nTool response data\nDebug info"
	require.Equal(t, expectedText, result.Content.Parts[0].Text)
}