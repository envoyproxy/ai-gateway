// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/tokenize"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewTokenizeToGCPVertexAITranslator implements [Factory] for tokenize to GCP Vertex AI translation.
func NewTokenizeToGCPVertexAITranslator(modelNameOverride internalapi.ModelNameOverride) TokenizeTranslator {
	return &ToGCPVertexAITranslatorV1Tokenize{
		modelNameOverride: modelNameOverride,
	}
}

// ToGCPVertexAITranslatorV1Tokenize translates tokenize API requests to GCP Vertex AI format.
// Converts OpenAI-compatible tokenize requests to GCP Gemini CountTokens API format.
// https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/count-tokens
type ToGCPVertexAITranslatorV1Tokenize struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
}

// tokenizeToGeminiCountToken converts an OpenAI tokenize chat request to GCP Gemini CountTokens format.
func (o *ToGCPVertexAITranslatorV1Tokenize) tokenizeToGeminiCountToken(tokenizeChatReq *tokenize.TokenizeChatRequest, requestModel internalapi.RequestModel) (*gcp.CountTokenRequest, error) {
	// Convert messages to Gemini Contents and SystemInstruction.
	contents, systemInstruction, err := openAIMessagesToGeminiContents(tokenizeChatReq.Messages, requestModel)
	if err != nil {
		return nil, err
	}

	// Some models support only partialJSONSchema.
	parametersJSONSchemaAvailable := responseJSONSchemaAvailable(requestModel)
	// Convert OpenAI tools to Gemini tools.
	tools, err := openAIToolsToGeminiTools(tokenizeChatReq.Tools, parametersJSONSchemaAvailable)
	if err != nil {
		return nil, fmt.Errorf("error converting tools: %w", err)
	}

	// Convert []genai.Tool to []*genai.Tool
	var toolPtrs []*genai.Tool
	for i := range tools {
		toolPtrs = append(toolPtrs, &tools[i])
	}

	// only media_resolution is related to the # prompt tokens
	gcr := gcp.CountTokenRequest{
		Contents: contents,
		Config: genai.CountTokensConfig{
			SystemInstruction: systemInstruction,
			Tools:             toolPtrs,
			GenerationConfig: &genai.GenerationConfig{
				MediaResolution: tokenizeChatReq.MediaResolution,
			},
		},
	}

	return &gcr, nil
}

// geminiCountTokenToTokenizeResponse converts a GCP Gemini CountTokens response to OpenAI tokenize format.
func (o *ToGCPVertexAITranslatorV1Tokenize) geminiCountTokenToTokenizeResponse(gcpResp *genai.CountTokensResponse) (*tokenize.TokenizeResponse, error) {
	// only media_resolution is related to the # prompt tokens
	tokenizeResp := tokenize.TokenizeResponse{
		Count: int(gcpResp.TotalTokens),
	}

	return &tokenizeResp, nil
}

// RequestBody implements [TokenizeTranslator.RequestBody] for GCP Vertex AI.
// This method translates an OpenAI tokenize request to GCP Gemini CountTokens format.
func (o *ToGCPVertexAITranslatorV1Tokenize) RequestBody(_ []byte, tokenizeReq *tokenize.TokenizeRequestUnion, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	// Validate that the union has exactly one request type set
	if err = tokenizeReq.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid tokenize request: %w", err)
	}

	// Store the request model to use as fallback for response model
	// Support both TokenizeChatRequest and TokenizeCompletionRequest
	if tokenizeReq.TokenizeChatRequest != nil {
		o.requestModel = tokenizeReq.TokenizeChatRequest.Model
	} else {
		return nil, nil, fmt.Errorf("only TokenizeChatRequest is supported for gemini models")
	}
	if o.modelNameOverride != "" {
		// Use modelName override if set.
		o.requestModel = o.modelNameOverride
	}

	// Build the correct path for GCP Vertex AI Count Tokens API
	path := buildGCPModelPathSuffix(gcpModelPublisherGoogle, o.requestModel, gcpMethodCountTokens)

	gcpReq, err := o.tokenizeToGeminiCountToken(tokenizeReq.TokenizeChatRequest, o.requestModel)
	if err != nil {
		return nil, nil, fmt.Errorf("error converting to Gemini request: %w", err)
	}

	newBody, err = json.Marshal(gcpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling Gemini count token request: %w", err)
	}
	newHeaders = []internalapi.Header{
		{pathHeaderName, path},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseError implements [TokenizeTranslator.ResponseError] for GCP Vertex AI.
// Translate GCP Vertex AI exceptions to OpenAI error type.
func (o *ToGCPVertexAITranslatorV1Tokenize) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	return translateGCPVertexAIErrorToOpenAI(respHeaders, body)
}

// ResponseHeaders implements [TokenizeTranslator.ResponseHeaders].
func (o *ToGCPVertexAITranslatorV1Tokenize) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [TokenizeTranslator.ResponseBody] for GCP Vertex AI.
// This method translates a GCP Gemini CountTokens response to OpenAI tokenize format.
// GCP Vertex AI uses deterministic model mapping without virtualization, where the requested model
// is exactly what gets executed. The response does not contain a model field, so we return
// the request model that was originally sent.
func (o *ToGCPVertexAITranslatorV1Tokenize) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.TokenizeSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	gcpResp := &genai.CountTokensResponse{}
	if err = json.NewDecoder(body).Decode(&gcpResp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	responseModel = o.requestModel

	// Convert to OpenAI format.
	openAIResp, err := o.geminiCountTokenToTokenizeResponse(gcpResp)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error converting GCP response to OpenAI format: %w", err)
	}

	// Marshal the OpenAI response.
	newBody, err = json.Marshal(openAIResp)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error marshaling OpenAI response: %w", err)
	}

	if span != nil {
		span.RecordResponse(openAIResp)
	}
	newHeaders = []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return
}
