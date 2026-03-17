// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"io"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

func TestGeminiToGCPVertexAITranslator_RequestBody(t *testing.T) {
	t.Run("non-streaming path rewrite", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		req := &gcp.GenerateContentRequest{
			Contents: []genai.Content{
				{
					Parts: []*genai.Part{
						{Text: "Hello, world!"},
					},
					Role: "user",
				},
			},
		}

		headers, mutatedBody, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Nil(t, mutatedBody, "body should not be mutated when no FunctionResponse.ID present")

		// Verify path header is set correctly
		require.Len(t, headers, 1)
		require.Equal(t, ":path", headers[0][0])
		require.Contains(t, headers[0][1], "publishers/google/models/gemini-flash:generateContent")
	})

	t.Run("streaming path rewrite", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-pro-1.5", true)

		req := &gcp.GenerateContentRequest{
			Contents: []genai.Content{
				{
					Parts: []*genai.Part{
						{Text: "Stream me!"},
					},
					Role: "user",
				},
			},
		}

		headers, mutatedBody, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Nil(t, mutatedBody)

		// Verify streaming path and alt=sse query param
		require.Len(t, headers, 1)
		require.Equal(t, ":path", headers[0][0])
		require.Contains(t, headers[0][1], "publishers/google/models/gemini-pro-1.5:streamGenerateContent")
		require.Contains(t, headers[0][1], "alt=sse")
	})

	t.Run("strips FunctionResponse.ID", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		req := &gcp.GenerateContentRequest{
			Contents: []genai.Content{
				{
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name: "get_weather",
								ID:   "call_123456", // This should be stripped
								Response: map[string]any{
									"temperature": 72,
									"unit":        "F",
								},
							},
						},
					},
					Role: "function",
				},
				{
					Parts: []*genai.Part{
						{Text: "What's the weather?"},
					},
					Role: "user",
				},
			},
		}

		headers, mutatedBody, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.NotNil(t, mutatedBody, "body should be mutated when FunctionResponse.ID is present")

		// Verify FunctionResponse.ID was stripped
		var mutatedReq gcp.GenerateContentRequest
		err = json.Unmarshal(mutatedBody, &mutatedReq)
		require.NoError(t, err)
		require.Len(t, mutatedReq.Contents, 2)
		require.NotNil(t, mutatedReq.Contents[0].Parts[0].FunctionResponse)
		require.Empty(t, mutatedReq.Contents[0].Parts[0].FunctionResponse.ID, "ID should be stripped")
		require.Equal(t, "get_weather", mutatedReq.Contents[0].Parts[0].FunctionResponse.Name, "Name should be preserved")

		// Verify path header is still set
		require.Len(t, headers, 1)
		require.Equal(t, ":path", headers[0][0])
	})

	t.Run("strips multiple FunctionResponse.IDs", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		req := &gcp.GenerateContentRequest{
			Contents: []genai.Content{
				{
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name: "get_weather",
								ID:   "call_111",
								Response: map[string]any{
									"temperature": 72,
								},
							},
						},
						{
							FunctionResponse: &genai.FunctionResponse{
								Name: "get_location",
								ID:   "call_222",
								Response: map[string]any{
									"city": "San Francisco",
								},
							},
						},
					},
					Role: "function",
				},
			},
		}

		headers, mutatedBody, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.NotNil(t, mutatedBody)

		var mutatedReq gcp.GenerateContentRequest
		err = json.Unmarshal(mutatedBody, &mutatedReq)
		require.NoError(t, err)
		require.Len(t, mutatedReq.Contents[0].Parts, 2)
		require.Empty(t, mutatedReq.Contents[0].Parts[0].FunctionResponse.ID)
		require.Empty(t, mutatedReq.Contents[0].Parts[1].FunctionResponse.ID)
		require.Equal(t, "get_weather", mutatedReq.Contents[0].Parts[0].FunctionResponse.Name)
		require.Equal(t, "get_location", mutatedReq.Contents[0].Parts[1].FunctionResponse.Name)

		require.Len(t, headers, 1)
	})

	t.Run("does not mutate when FunctionResponse.ID is empty", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		req := &gcp.GenerateContentRequest{
			Contents: []genai.Content{
				{
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name: "get_weather",
								ID:   "", // Already empty
								Response: map[string]any{
									"temperature": 72,
								},
							},
						},
					},
					Role: "function",
				},
			},
		}

		headers, mutatedBody, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Nil(t, mutatedBody, "body should not be mutated when ID is already empty")
		require.Len(t, headers, 1)
	})

	t.Run("handles empty model name", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("", false)

		req := &gcp.GenerateContentRequest{
			Contents: []genai.Content{
				{
					Parts: []*genai.Part{
						{Text: "Hello"},
					},
					Role: "user",
				},
			},
		}

		headers, mutatedBody, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Nil(t, mutatedBody)

		// Verify path is constructed even with empty model
		require.Len(t, headers, 1)
		require.Equal(t, ":path", headers[0][0])
		require.Contains(t, headers[0][1], "publishers/google/models/:generateContent")
	})
}

func TestGeminiToGCPVertexAITranslator_ResponseHeaders(t *testing.T) {
	t.Run("non-streaming returns no headers", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		headers, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Nil(t, headers)
	})

	t.Run("streaming returns content-type header", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", true)

		headers, err := translator.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Len(t, headers, 1)
		require.Equal(t, "content-type", headers[0][0])
		require.Equal(t, "text/event-stream", headers[0][1])
	})
}

func TestGeminiToGCPVertexAITranslator_ResponseBody(t *testing.T) {
	t.Run("passes through non-streaming response", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		responseBody := `{
			"candidates": [{
				"content": {
					"parts": [{
						"text": "Hello, how can I help you?"
					}],
					"role": "model"
				},
				"finishReason": "STOP"
			}],
			"usageMetadata": {
				"promptTokenCount": 10,
				"candidatesTokenCount": 8,
				"totalTokenCount": 18
			}
		}`

		headers, mutatedBody, tokenUsage, responseModel, err := translator.ResponseBody(
			nil,
			bytes.NewReader([]byte(responseBody)),
			false,
			nil,
		)

		require.NoError(t, err)
		require.Equal(t, []byte(responseBody), mutatedBody, "body should be passed through unchanged")
		require.Len(t, headers, 1)
		require.Equal(t, "content-length", headers[0][0])
		require.Equal(t, "gemini-flash", responseModel)
		require.Equal(t, metrics.TokenUsage{}, tokenUsage, "token usage not extracted in passthrough mode")
	})

	t.Run("passes through streaming response", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-pro-1.5", true)

		sseData := `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}]}

data: {"candidates":[{"content":{"parts":[{"text":" world"}],"role":"model"}}]}

data: [DONE]
`

		headers, mutatedBody, tokenUsage, responseModel, err := translator.ResponseBody(
			nil,
			bytes.NewReader([]byte(sseData)),
			true,
			nil,
		)

		require.NoError(t, err)
		require.Equal(t, []byte(sseData), mutatedBody, "streaming body should be passed through")
		require.Len(t, headers, 1)
		require.Equal(t, "content-length", headers[0][0])
		require.Equal(t, "gemini-pro-1.5", responseModel)
		require.Equal(t, metrics.TokenUsage{}, tokenUsage)
	})

	t.Run("handles empty response body", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		headers, mutatedBody, tokenUsage, responseModel, err := translator.ResponseBody(
			nil,
			bytes.NewReader([]byte("")),
			false,
			nil,
		)

		require.NoError(t, err)
		require.Empty(t, mutatedBody)
		require.Len(t, headers, 1)
		require.Equal(t, "content-length", headers[0][0])
		require.Equal(t, "0", headers[0][1])
		require.Equal(t, "gemini-flash", responseModel)
		require.Equal(t, metrics.TokenUsage{}, tokenUsage)
	})

	t.Run("handles io.Reader error", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		// Create a reader that returns an error
		testErrorReader := &testReaderWithError{err: io.ErrUnexpectedEOF}

		headers, mutatedBody, tokenUsage, responseModel, err := translator.ResponseBody(
			nil,
			testErrorReader,
			false,
			nil,
		)

		require.Error(t, err)
		require.Equal(t, io.ErrUnexpectedEOF, err)
		require.Nil(t, headers)
		require.Nil(t, mutatedBody)
		require.Empty(t, responseModel)
		require.Equal(t, metrics.TokenUsage{}, tokenUsage)
	})
}

func TestGeminiToGCPVertexAITranslator_ResponseError(t *testing.T) {
	t.Run("passes through error response", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		errorBody := `{
			"error": {
				"code": 400,
				"message": "Invalid request: missing required field",
				"status": "INVALID_ARGUMENT"
			}
		}`

		headers, mutatedBody, err := translator.ResponseError(
			nil,
			bytes.NewReader([]byte(errorBody)),
		)

		require.NoError(t, err)
		require.Equal(t, []byte(errorBody), mutatedBody, "error body should be passed through")
		require.Len(t, headers, 1)
		require.Equal(t, "content-length", headers[0][0])
		require.Equal(t, strconv.Itoa(len(errorBody)), headers[0][1])
	})

	t.Run("handles io.Reader error", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		testErrorReader := &testReaderWithError{err: io.ErrClosedPipe}

		headers, mutatedBody, err := translator.ResponseError(
			nil,
			testErrorReader,
		)

		require.Error(t, err)
		require.Equal(t, io.ErrClosedPipe, err)
		require.Nil(t, headers)
		require.Nil(t, mutatedBody)
	})

	t.Run("handles empty error body", func(t *testing.T) {
		translator := NewGeminiToGCPVertexAITranslator("gemini-flash", false)

		headers, mutatedBody, err := translator.ResponseError(
			nil,
			bytes.NewReader([]byte("")),
		)

		require.NoError(t, err)
		require.Empty(t, mutatedBody)
		require.Len(t, headers, 1)
		require.Equal(t, "content-length", headers[0][0])
		require.Equal(t, "0", headers[0][1])
	})
}

// testReaderWithError is a test helper that always returns an error on Read.
type testReaderWithError struct {
	err error
}

func (e *testReaderWithError) Read(p []byte) (n int, err error) {
	return 0, e.err
}
