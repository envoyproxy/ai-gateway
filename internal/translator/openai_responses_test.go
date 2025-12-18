// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"testing"

	"github.com/openai/openai-go/v2/packages/param"
	"github.com/openai/openai-go/v2/responses"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func Test_NewResponsesOpenAIToOpenAITranslator(t *testing.T) {
	tests := []struct {
		name       string
		apiVersion string
		expectPath string
	}{
		{
			name:       "v1",
			apiVersion: "v1",
			expectPath: "/v1/responses",
		},
		{
			name:       "custom path",
			apiVersion: "custom/v1",
			expectPath: "/custom/v1/responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewResponsesOpenAIToOpenAITranslator(tt.apiVersion, "").(*openAIToOpenAITranslatorV1Responses)
			require.NotNil(t, translator)
			require.Equal(t, tt.expectPath, translator.path)
		})
	}
}

func TestResponsesOpenAIToOpenAITranslator_RequestBody(t *testing.T) {
	t.Run("basic request without override", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		original := []byte(`{"model":"gpt-4o","input":"Hi"}`)

		headers, body, err := translator.RequestBody(original, req, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", translator.requestModel)
		require.False(t, translator.stream)
		require.Equal(t, 1, len(headers))
		require.Equal(t, pathHeaderName, headers[0].Key())
		require.Equal(t, "/v1/responses", headers[0].Value())
		// Body should be nil when no mutation needed
		require.Nil(t, body)
	})

	t.Run("streaming request", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: true,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		original := []byte(`{"model":"gpt-4o","stream":true,"input":"Hi"}`)

		headers, body, err := translator.RequestBody(original, req, false)
		require.NoError(t, err)
		require.True(t, translator.stream)
		require.Equal(t, 1, len(headers))
		// Body should be nil when no mutation needed
		require.Nil(t, body)
	})

	t.Run("model name override", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "gpt-4-turbo").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		original := []byte(`{"model":"gpt-4o","input":"Hi"}`)

		headers, body, err := translator.RequestBody(original, req, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4-turbo", translator.requestModel)

		// Verify the model was overridden in the body
		var result map[string]interface{}
		err = json.Unmarshal(body, &result)
		require.NoError(t, err)
		require.Equal(t, "gpt-4-turbo", result["model"])

		// Verify content-length header is set
		require.Equal(t, 2, len(headers))
		require.Equal(t, contentLengthHeaderName, headers[1].Key())
		require.Equal(t, strconv.Itoa(len(body)), headers[1].Value())
	})

	t.Run("forced mutation without override", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		original := []byte(`{"model":"gpt-4o", "input":"Hi"}`)

		headers, body, err := translator.RequestBody(original, req, true)
		require.NoError(t, err)

		// When forced mutation is true but no override, body should still be returned
		require.NotNil(t, body)
		require.Equal(t, 2, len(headers))
		require.Equal(t, contentLengthHeaderName, headers[1].Key())
	})

	t.Run("empty original with forced mutation", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "override-model").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}

		_, body, err := translator.RequestBody([]byte{}, req, true)
		require.NoError(t, err)
		require.NotNil(t, body)
		require.Greater(t, len(body), 0)

		// Verify the model override is in the body
		var result map[string]interface{}
		err = json.Unmarshal(body, &result)
		require.NoError(t, err)
		require.Equal(t, "override-model", result["model"])
	})
}

func TestResponsesOpenAIToOpenAITranslator_ResponseHeaders(t *testing.T) {
	translator := NewResponsesOpenAIToOpenAITranslator("v1", "")

	headers, err := translator.ResponseHeaders(map[string]string{
		"content-type": "application/json",
		":status":      "200",
	})

	require.NoError(t, err)
	require.Nil(t, headers)
}

func TestResponsesOpenAIToOpenAITranslator_ResponseBody(t *testing.T) {
	t.Run("non-streaming response", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		orginal := []byte(`{"model":"gpt-4o","input":"Hi"}`)
		_, _, err := translator.RequestBody(orginal, req, false)
		require.NoError(t, err)

		// Create a valid response
		respJSON := []byte(`{
  			"id": "resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b",
  			"object": "response",
  			"created_at": 1741476542,
  			"status": "completed",
  			"model": "gpt-4o-2024-11-20",
  			"output": [
    			{
      				"type": "message",
     				"id": "msg_67ccd2bf17f0819081ff3bb2cf6508e60bb6a6b452d3795b",
      				"status": "completed",
      				"role": "assistant",
      				"content": [
        				{
          					"type": "output_text",
          					"text": "Hello, how can I help?"
        				}
      				]
    			}
  			],
  			"usage": {
    			"input_tokens": 10,
    			"input_tokens_details": {
      				"cached_tokens": 2
    			},
    			"output_tokens": 5,
    			"output_tokens_details": {
      				"reasoning_tokens": 0
    			},
    			"total_tokens": 15
  			}
		}`)
		var resp openai.Response
		err = json.Unmarshal(respJSON, &resp)
		require.NoError(t, err)

		_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(respJSON), false, nil)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o-2024-11-20", responseModel)

		inputTokens, ok := tokenUsage.InputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(10), inputTokens)

		outputTokens, ok := tokenUsage.OutputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(5), outputTokens)

		totalTokens, ok := tokenUsage.TotalTokens()
		require.True(t, ok)
		require.Equal(t, uint32(15), totalTokens)

		cachedTokens, ok := tokenUsage.CachedInputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(2), cachedTokens)
	})

	t.Run("non-streaming response with fallback model", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		orginal := []byte(`{"model":"gpt-4o","input":"Hi"}`)
		_, _, err := translator.RequestBody(orginal, req, false)
		require.NoError(t, err)

		// Response without model field
		respJSON := []byte(`{
  			"id": "resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b",
  			"object": "response",
  			"created_at": 1741476542,
  			"status": "completed",
  			"model": "",
  			"output": [
    			{
      				"type": "message",
     				"id": "msg_67ccd2bf17f0819081ff3bb2cf6508e60bb6a6b452d3795b",
      				"status": "completed",
      				"role": "assistant",
      				"content": [
        				{
          					"type": "output_text",
          					"text": "Hello, how can I help?"
        				}
      				]
    			}
  			],
  			"usage": {
    			"input_tokens": 10,
    			"input_tokens_details": {
      				"cached_tokens": 2
    			},
    			"output_tokens": 5,
    			"output_tokens_details": {
      				"reasoning_tokens": 0
    			},
    			"total_tokens": 15
  			}
		}`)
		_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(respJSON), false, nil)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o", responseModel) // Falls back to request model

		inputTokens, ok := tokenUsage.InputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(10), inputTokens)
	})

	t.Run("streaming response", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: true,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		orginal := []byte(`{"model":"gpt-4o","input":"Hi","stream":true}`)
		_, _, err := translator.RequestBody(orginal, req, false)
		require.NoError(t, err)
		require.True(t, translator.stream)

		// Simulate SSE stream with response events
		sseChunks := `data: {"type":"response.created","response":{"model":"gpt-4o-2024-11-20"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"in_progress","role":"assistant","content":[]}}

data: {"type":"response.content_part.added","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

data: {"type":"response.output_text.delta","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"delta":"Hello"}

data: {"type":"response.output_text.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"text":"Hello, how can I help?"}

data: {"type":"response.content_part.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello, how can I help?","annotations":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?","annotations":[]}]}}

data: {"type":"response.completed","response":{"id":"resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b","object":"response","created_at":1741476542,"status":"completed","model":"","output":[{"type":"message","id":"msg_67ccd2bf17f0819081ff3bb2cf6508e60bb6a6b452d3795b","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?"}]}],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":2},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":15}}}

data: [DONE]

`
		_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(sseChunks)), true, nil)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o-2024-11-20", responseModel)

		inputTokens, ok := tokenUsage.InputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(10), inputTokens)

		outputTokens, ok := tokenUsage.OutputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(5), outputTokens)

		totalTokens, ok := tokenUsage.TotalTokens()
		require.True(t, ok)
		require.Equal(t, uint32(15), totalTokens)

		cachedTokens, ok := tokenUsage.CachedInputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(2), cachedTokens)
	})

	t.Run("streaming response with fallback model", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o-mini",
			Stream: true,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		orginal := []byte(`{"model":"gpt-4o-mini","input":"Hi","stream": true}`)
		_, _, err := translator.RequestBody(orginal, req, false)
		require.NoError(t, err)

		// Streaming response without model in events
		sseChunks := `data: {"type":"response.created","response":{"model":""}}

data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"in_progress","role":"assistant","content":[]}}

data: {"type":"response.content_part.added","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

data: {"type":"response.output_text.delta","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"delta":"Hello"}

data: {"type":"response.output_text.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"text":"Hello, how can I help?"}

data: {"type":"response.content_part.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello, how can I help?","annotations":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?","annotations":[]}]}}

data: {"type":"response.completed","response":{"id":"resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b","object":"response","created_at":1741476542,"status":"completed","model":"","output":[{"type":"message","id":"msg_67ccd2bf17f0819081ff3bb2cf6508e60bb6a6b452d3795b","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?"}]}],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":2},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":15}}}

data: [DONE]

`
		_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(sseChunks)), true, nil)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o-mini", responseModel) // Falls back to request model

		inputTokens, ok := tokenUsage.InputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(10), inputTokens)
	})
}

func TestResponses_HandleStreamingResponse(t *testing.T) {
	t.Run("valid streaming events", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: true,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		orginal := []byte(`{"model":"gpt-4o","input":"Hi","stream":true}`)
		_, _, err := translator.RequestBody(orginal, req, false)
		require.NoError(t, err)

		sseChunks := `data: {"type":"response.created","response":{"model":"gpt-4o-2024-11-20"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"in_progress","role":"assistant","content":[]}}

data: {"type":"response.content_part.added","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

data: {"type":"response.output_text.delta","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"delta":"Hello"}

data: {"type":"response.output_text.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"text":"Hello, how can I help?"}

data: {"type":"response.content_part.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello, how can I help?","annotations":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?","annotations":[]}]}}

data: {"type":"response.completed","response":{"id":"resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b","object":"response","created_at":1741476542,"status":"completed","model":"","output":[{"type":"message","id":"msg_67ccd2bf17f0819081ff3bb2cf6508e60bb6a6b452d3795b","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?"}]}],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":2},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":15}}}

data: [DONE]

`
		_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(sseChunks)), true, nil)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o-2024-11-20", responseModel)

		inputTokens, _ := tokenUsage.InputTokens()
		require.Equal(t, uint32(10), inputTokens)

		outputTokens, _ := tokenUsage.OutputTokens()
		require.Equal(t, uint32(5), outputTokens)

		totalTokens, _ := tokenUsage.TotalTokens()
		require.Equal(t, uint32(15), totalTokens)

		cachedTokens, _ := tokenUsage.CachedInputTokens()
		require.Equal(t, uint32(2), cachedTokens)
	})

	t.Run("streaming read error", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: true,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		orginal := []byte(`{"model":"gpt-4o","input":"Hi","stream":true}`)
		_, _, err := translator.RequestBody(orginal, req, false)
		require.NoError(t, err)

		// Create a reader that fails
		failReader := &failingReader{}

		_, _, _, _, err = translator.ResponseBody(nil, failReader, true, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read body")
	})
}

func TestResponses_HandleNonStreamingResponse(t *testing.T) {
	t.Run("complete response", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{Value: "Hi"},
			},
		}
		orginal := []byte(`{"model":"gpt-4o","input":"Hi"`)
		_, _, err := translator.RequestBody(orginal, req, false)
		require.NoError(t, err)

		respJSON := []byte(`{
  			"id": "resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b",
  			"object": "response",
  			"created_at": 1741476542,
  			"status": "completed",
  			"model": "gpt-4o-2024-11-20",
  			"output": [
    			{
      				"type": "message",
     				"id": "msg_67ccd2bf17f0819081ff3bb2cf6508e60bb6a6b452d3795b",
      				"status": "completed",
      				"role": "assistant",
      				"content": [
        				{
          					"type": "output_text",
          					"text": "Hello, how can I help?"
        				}
      				]
    			}
  			],
  			"usage": {
    			"input_tokens": 10,
    			"input_tokens_details": {
      				"cached_tokens": 2
    			},
    			"output_tokens": 5,
    			"output_tokens_details": {
      				"reasoning_tokens": 0
    			},
    			"total_tokens": 15
  			}
		}`)

		_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(respJSON), false, nil)
		require.NoError(t, err)
		require.Equal(t, "gpt-4o-2024-11-20", responseModel)

		inputTokens, _ := tokenUsage.InputTokens()
		require.Equal(t, uint32(10), inputTokens)

		outputTokens, _ := tokenUsage.OutputTokens()
		require.Equal(t, uint32(5), outputTokens)

		totalTokens, _ := tokenUsage.TotalTokens()
		require.Equal(t, uint32(15), totalTokens)

		cachedTokens, _ := tokenUsage.CachedInputTokens()
		require.Equal(t, uint32(2), cachedTokens)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
		}
		_, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		invalidBody := bytes.NewReader([]byte(`{invalid json`))

		_, _, _, _, err = translator.ResponseBody(nil, invalidBody, false, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal body")
	})
}

func TestResponses_ExtractUsageFromBufferEvent(t *testing.T) {
	t.Run("valid usage data", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		chunks := []byte(`data: {"type":"response.created","response":{"model":"gpt-4o-2024-11-20"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"in_progress","role":"assistant","content":[]}}

data: {"type":"response.content_part.added","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

data: {"type":"response.output_text.delta","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"delta":"Hello"}

data: {"type":"response.output_text.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"text":"Hello, how can I help?"}

data: {"type":"response.content_part.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello, how can I help?","annotations":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?","annotations":[]}]}}

data: {"type":"response.completed","response":{"id":"resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b","object":"response","created_at":1741476542,"status":"completed","model":"","output":[{"type":"message","id":"msg_67ccd2bf17f0819081ff3bb2cf6508e60bb6a6b452d3795b","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?"}]}],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":2},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":15}}}

data: [DONE]

`)

		tokenUsage := translator.extractUsageFromBufferEvent(nil, chunks)

		inputTokens, ok := tokenUsage.InputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(10), inputTokens)

		outputTokens, ok := tokenUsage.OutputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(5), outputTokens)

		totalTokens, ok := tokenUsage.TotalTokens()
		require.True(t, ok)
		require.Equal(t, uint32(15), totalTokens)

		cachedTokens, ok := tokenUsage.CachedInputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(2), cachedTokens)
	})

	t.Run("model extraction", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		chunks := []byte(`data: {"type":"response.created","response":{"model":"gpt-4o-2024-11-20"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"in_progress","role":"assistant","content":[]}}

data: {"type":"response.content_part.added","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

data: {"type":"response.output_text.delta","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"delta":"Hello"}

data: {"type":"response.output_text.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"text":"Hello, how can I help?"}

data: {"type":"response.content_part.done","item_id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello, how can I help?","annotations":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_67c9fdcf37fc8190ba82116e33fb28c507b8b0ad4e5eb654","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?","annotations":[]}]}}

data: {"type":"response.completed","response":{"id":"resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b","object":"response","created_at":1741476542,"status":"completed","model":"","output":[{"type":"message","id":"msg_67ccd2bf17f0819081ff3bb2cf6508e60bb6a6b452d3795b","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello, how can I help?"}]}],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":2},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":15}}}

data: [DONE]

`)

		translator.extractUsageFromBufferEvent(nil, chunks)
		require.Equal(t, "gpt-4o-2024-11-20", translator.streamingResponseModel)
	})

	t.Run("invalid JSON skipped", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		chunks := []byte(`data: {invalid json}

data: {"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}

`)

		tokenUsage := translator.extractUsageFromBufferEvent(nil, chunks)

		inputTokens, ok := tokenUsage.InputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(5), inputTokens)

		outputTokens, ok := tokenUsage.OutputTokens()
		require.True(t, ok)
		require.Equal(t, uint32(3), outputTokens)
	})

	t.Run("no usage data", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)

		chunks := []byte(`data: {"type":"response.created","response":{"model":"gpt-4o"}}

data: [DONE]

`)

		tokenUsage := translator.extractUsageFromBufferEvent(nil, chunks)

		_, inputSet := tokenUsage.InputTokens()
		_, outputSet := tokenUsage.OutputTokens()
		_, totalSet := tokenUsage.TotalTokens()
		_, cachedSet := tokenUsage.CachedInputTokens()

		require.False(t, totalSet)
		require.False(t, cachedSet)
		require.False(t, inputSet)
		require.False(t, outputSet)
	})
}

func TestResponsesOpenAIToOpenAITranslator_ResponseError(t *testing.T) {
	tests := []struct {
		name            string
		responseHeaders map[string]string
		input           io.Reader
		expectHeaders   int
	}{
		{
			name: "non-JSON error response",
			responseHeaders: map[string]string{
				":status":      "503",
				"content-type": "text/plain",
			},
			input:         bytes.NewBuffer([]byte("service unavailable")),
			expectHeaders: 2,
		},
		{
			name: "JSON error response",
			responseHeaders: map[string]string{
				":status":      "400",
				"content-type": "application/json",
			},
			input:         bytes.NewBuffer([]byte(`{"error":{"message":"bad request"}}`)),
			expectHeaders: 0,
		},
		{
			name: "missing content-type header",
			responseHeaders: map[string]string{
				":status": "500",
			},
			input:         bytes.NewBuffer([]byte("internal error")),
			expectHeaders: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translator := NewResponsesOpenAIToOpenAITranslator("v1", "")

			headers, body, err := translator.ResponseError(tt.responseHeaders, tt.input)
			require.NoError(t, err)

			if tt.expectHeaders > 0 {
				require.Len(t, headers, tt.expectHeaders)
				// Check that error was converted to OpenAI format
				var errResp openai.Error
				err = json.Unmarshal(body, &errResp)
				require.NoError(t, err)
				require.Equal(t, "error", errResp.Type)
				require.Equal(t, openAIBackendError, errResp.Error.Type)
				require.Equal(t, tt.responseHeaders[":status"], *errResp.Error.Code)
			} else {
				// JSON response or missing content-type should pass through unchanged
				require.Nil(t, body)
			}
		})
	}
}

func TestResponsesOpenAIToOpenAITranslatorWithModelOverride(t *testing.T) {
	t.Run("request body with override", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "gpt-4-turbo").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
		}
		original := []byte(`{"model":"gpt-4o"}`)

		_, _, err := translator.RequestBody(original, req, false)
		require.NoError(t, err)
		require.Equal(t, "gpt-4-turbo", translator.requestModel)
	})

	t.Run("response uses override as fallback", func(t *testing.T) {
		translator := NewResponsesOpenAIToOpenAITranslator("v1", "gpt-4-turbo").(*openAIToOpenAITranslatorV1Responses)

		req := &openai.ResponseRequest{
			Model:  "gpt-4o",
			Stream: false,
		}
		_, _, err := translator.RequestBody(nil, req, false)
		require.NoError(t, err)

		// Response without model
		resp := openai.Response{
			Model: "",
			Usage: openai.ResponseUsage{
				InputTokens:  10,
				OutputTokens: 5,
				TotalTokens:  15,
			},
		}

		body, err := json.Marshal(resp)
		require.NoError(t, err)

		_, _, _, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(body), false, nil)
		require.NoError(t, err)
		require.Equal(t, "gpt-4-turbo", responseModel)
	})
}

// Helper types for testing

type failingReader struct{}

func (f *failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
