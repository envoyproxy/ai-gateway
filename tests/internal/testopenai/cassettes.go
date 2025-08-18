// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Cassette is an HTTP interaction recording.
//
// Note: At the moment, our tests are optimized for single request/response
// pairs and do not include scenarios requiring multiple round-trips, such as
// `cached_tokens`.
type Cassette int

const (
	// Cassettes for the OpenAI /chat/completions endpoint.

	// CassetteChatBasic is the canonical OpenAI chat completion request.
	CassetteChatBasic Cassette = iota
	// CassetteChatJSONMode is a chat completion request with JSON response format.
	CassetteChatJSONMode
	// CassetteChatMultimodal is a multimodal chat request with text and image inputs.
	CassetteChatMultimodal
	// CassetteChatMultiturn is a multi-turn conversation with message history.
	CassetteChatMultiturn
	// CassetteChatNoMessages is a request missing the required messages field.
	CassetteChatNoMessages
	// CassetteChatParallelTools is a chat completion with parallel function calling enabled.
	CassetteChatParallelTools
	// CassetteChatStreaming is the canonical OpenAI chat completion request,
	// with streaming enabled.
	CassetteChatStreaming
	// CassetteChatTools is a chat completion request with function tools.
	CassetteChatTools
	// CassetteChatUnknownModel is a request with a non-existent model.
	CassetteChatUnknownModel
	// CassetteChatBadRequest is a request with multiple validation errors.
	CassetteChatBadRequest
	// CassetteChatReasoning tests capture of reasoning_tokens in completion_tokens_details for O1 models.
	CassetteChatReasoning
	// CassetteChatImageToText tests image input processing showing image token
	// count in usage details.
	CassetteChatImageToText
	// CassetteChatTextToImageTool tests image generation through tool calls since
	// chat completions cannot natively output images.
	CassetteChatTextToImageTool
	// CassetteChatAudioToText tests audio input transcription and audio_tokens
	// in prompt_tokens_details.
	CassetteChatAudioToText
	// CassetteChatTextToAudio tests audio output generation where the model
	// produces audio content, showing audio_tokens in completion_tokens_details.
	CassetteChatTextToAudio
	// CassetteChatDetailedUsage tests capture of all token usage detail fields in a single response.
	CassetteChatDetailedUsage
	// CassetteChatStreamingDetailedUsage tests capture of detailed token usage in streaming responses with include_usage.
	CassetteChatStreamingDetailedUsage
	// CassetteChatWebSearch tests OpenAI Web Search tool with a small URL response, including citations.
	CassetteChatWebSearch
	// CassetteChatStreamingWebSearch is CassetteChatWebSearch except with streaming enabled.
	CassetteChatStreamingWebSearch
	// CassetteChatOpenAIAgentsPython is a real request from OpenAI Agents Python library for financial research.
	// See https://github.com/openai/openai-agents-python/tree/main/examples/financial_research_agent
	CassetteChatOpenAIAgentsPython

	// Cassettes for the OpenAI /embeddings endpoint.

	// CassetteEmbeddingsBasic is the canonical OpenAI embeddings request with a single string input.
	CassetteEmbeddingsBasic
	// CassetteEmbeddingsBase64 tests base64 encoding format for embedding vectors.
	CassetteEmbeddingsBase64
	// CassetteEmbeddingsTokens tests embeddings with token array input instead of text.
	CassetteEmbeddingsTokens
	// CassetteEmbeddingsLargeText tests embeddings with a longer text input.
	CassetteEmbeddingsLargeText
	// CassetteEmbeddingsUnknownModel tests error handling for non-existent model.
	CassetteEmbeddingsUnknownModel
	// CassetteEmbeddingsDimensions tests embeddings with specified output dimensions.
	CassetteEmbeddingsDimensions
	// CassetteEmbeddingsMixedBatch tests batch with varying text lengths.
	CassetteEmbeddingsMixedBatch
	// CassetteEmbeddingsMaxTokens tests input that approaches token limit.
	CassetteEmbeddingsMaxTokens
	// CassetteEmbeddingsWhitespace tests handling of various whitespace patterns.
	CassetteEmbeddingsWhitespace
	// CassetteEmbeddingsBadRequest tests request with multiple validation errors.
	CassetteEmbeddingsBadRequest
	_cassetteNameEnd // Sentinel value for iteration.
)

// stringValues maps Cassette values to their string representations.
var stringValues = map[Cassette]string{
	CassetteChatBasic:                  "chat-basic",
	CassetteChatJSONMode:               "chat-json-mode",
	CassetteChatMultimodal:             "chat-multimodal",
	CassetteChatMultiturn:              "chat-multiturn",
	CassetteChatNoMessages:             "chat-no-messages",
	CassetteChatParallelTools:          "chat-parallel-tools",
	CassetteChatStreaming:              "chat-streaming",
	CassetteChatTools:                  "chat-tools",
	CassetteChatUnknownModel:           "chat-unknown-model",
	CassetteChatBadRequest:             "chat-bad-request",
	CassetteChatReasoning:              "chat-reasoning",
	CassetteChatImageToText:            "chat-image-to-text",
	CassetteChatTextToImageTool:        "chat-text-to-image-tool",
	CassetteChatAudioToText:            "chat-audio-to-text",
	CassetteChatTextToAudio:            "chat-text-to-audio",
	CassetteChatDetailedUsage:          "chat-detailed-usage",
	CassetteChatStreamingDetailedUsage: "chat-streaming-detailed-usage",
	CassetteChatWebSearch:              "chat-web-search",
	CassetteChatStreamingWebSearch:     "chat-streaming-web-search",
	CassetteChatOpenAIAgentsPython:     "chat-openai-agents-python",

	CassetteEmbeddingsBasic:        "embeddings-basic",
	CassetteEmbeddingsBase64:       "embeddings-base64",
	CassetteEmbeddingsTokens:       "embeddings-tokens",
	CassetteEmbeddingsLargeText:    "embeddings-large-text",
	CassetteEmbeddingsUnknownModel: "embeddings-unknown-model",
	CassetteEmbeddingsDimensions:   "embeddings-dimensions",
	CassetteEmbeddingsMixedBatch:   "embeddings-mixed-batch",
	CassetteEmbeddingsMaxTokens:    "embeddings-max-tokens",
	CassetteEmbeddingsWhitespace:   "embeddings-whitespace",
	CassetteEmbeddingsBadRequest:   "embeddings-bad-request",
}

// String returns the string representation of the cassette name.
func (c Cassette) String() string {
	if s, ok := stringValues[c]; ok {
		return s
	}
	return "unknown"
}

// NewRequest creates a new OpenAI request for the given cassette.
//
// The returned request is an http.MethodPost with the body and
// CassetteNameHeader according to the pre-recorded cassette.
func NewRequest(ctx context.Context, baseURL string, cassette Cassette) (*http.Request, error) {
	if r, ok := chatRequests[cassette]; ok {
		return newRequest(ctx, cassette, baseURL+"/chat/completions", r)
	} else if r, ok := embeddingsRequests[cassette]; ok {
		return newRequest(ctx, cassette, baseURL+"/embeddings", r)
	}
	return nil, fmt.Errorf("unknown cassette: %s", cassette)
}

// ResponseBody is used in tests to avoid duplicating body content when the
// proxy serialization matches exactly the upstream (testopenai) server.
func ResponseBody(cassette Cassette) string {
	if c, ok := allVCRCassettes[cassette.String()]; ok {
		return c.Interactions[0].Response.Body
	}
	return ""
}

// cassettes contains an ordered slice the request keys.
func cassettes[R any](requests map[Cassette]*R) []Cassette {
	result := make([]Cassette, 0, int(_cassetteNameEnd))
	for c := Cassette(0); c < _cassetteNameEnd; c++ {
		if _, ok := requests[c]; ok {
			result = append(result, c)
		}
	}
	return result
}

// newRequest creates a new HTTP request for the given cassette.
//
// The returned request is an http.MethodPost with the body and
// CassetteNameHeader according to the pre-recorded cassette.
func newRequest[R any](ctx context.Context, cassette Cassette, url string, request *R) (*http.Request, error) {
	// Marshal the request body to JSON.
	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create the request.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}

	// Set headers.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cassette-Name", cassette.String())

	return req, nil
}
