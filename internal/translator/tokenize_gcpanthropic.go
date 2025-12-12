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
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/apischema/tokenize"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

const (
	// Error type constants for GCP Anthropic responses
	gcpAnthropicBackendError = "GCPAnthropicBackendError"
)

// NewTokenizeToGCPAnthropicTranslator implements [Factory] for tokenize to GCP Anthropic translation.
func NewTokenizeToGCPAnthropicTranslator(modelNameOverride internalapi.ModelNameOverride) TokenizeTranslator {
	return &ToGCPAnthropicTranslatorV1Tokenize{
		modelNameOverride: modelNameOverride,
	}
}

// ToGCPAnthropicTranslatorV1Tokenize translates tokenize API requests to GCP Anthropic format.
// Converts OpenAI-compatible tokenize requests to GCP Anthropic Messages API format for token counting.
// Uses the count-tokens model with rawPredict method for token counting.
type ToGCPAnthropicTranslatorV1Tokenize struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	apiVersion        string
}

// tokenizeToAnthropicMessages converts an OpenAI tokenize chat request to GCP Anthropic token counting format.
// Since Anthropic doesn't have a dedicated tokenization endpoint, we use the MessageCountTokens API
// to count input tokens accurately without needing to generate any output.
func (o *ToGCPAnthropicTranslatorV1Tokenize) tokenizeToAnthropicMessages(tokenizeChatReq *tokenize.TokenizeChatRequest, requestModel internalapi.RequestModel) (*anthropic.MessageCountTokensParams, error) {
	// Convert OpenAI messages to Anthropic format
	messages, systemBlocks, err := openAIToAnthropicMessages(tokenizeChatReq.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Build Anthropic MessageCountTokens request
	countTokensParam := &anthropic.MessageCountTokensParams{
		Messages: messages,
		Model:    anthropic.Model(requestModel),
	}

	// Set system prompt if present
	if len(systemBlocks) > 0 {
		// Convert system blocks to MessageCountTokensParamsSystemUnion
		if len(systemBlocks) == 1 {
			// Single system block - use string format
			countTokensParam.System = anthropic.MessageCountTokensParamsSystemUnion{
				OfString: anthropic.String(systemBlocks[0].Text),
			}
		} else {
			// Multiple system blocks - use array format
			textBlocks := make([]anthropic.TextBlockParam, len(systemBlocks))
			for i, block := range systemBlocks {
				textBlocks[i] = anthropic.TextBlockParam{
					Text: block.Text,
				}
			}
			countTokensParam.System = anthropic.MessageCountTokensParamsSystemUnion{
				OfTextBlockArray: textBlocks,
			}
		}
	}

	// Convert tools if present
	if len(tokenizeChatReq.Tools) > 0 {
		countTokensParam.Tools = make([]anthropic.MessageCountTokensToolUnionParam, len(tokenizeChatReq.Tools))
		for i, tool := range tokenizeChatReq.Tools {
			if tool.Function != nil {
				countTokensParam.Tools[i] = anthropic.MessageCountTokensToolParamOfTool(
					anthropic.ToolInputSchemaParam{
						Properties: tool.Function.Parameters,
					},
					tool.Function.Name,
				)
			}
		}
	}

	return countTokensParam, nil
}

// anthropicTokensCountToTokenizeResponse converts an Anthropic MessageTokensCount response to OpenAI tokenize format.
// Extracts the input token count from the token counting response.
func (o *ToGCPAnthropicTranslatorV1Tokenize) anthropicTokensCountToTokenizeResponse(anthropicResp *anthropic.MessageTokensCount) (*tokenize.TokenizeResponse, error) {
	tokenizeResp := &tokenize.TokenizeResponse{
		Count: int(anthropicResp.InputTokens),
	}

	return tokenizeResp, nil
}

// RequestBody implements [TokenizeTranslator.RequestBody] for GCP Anthropic.
// This method translates an OpenAI tokenize request to GCP Anthropic Messages format.
// TODO: check whether I need to add other fields.
func (o *ToGCPAnthropicTranslatorV1Tokenize) RequestBody(_ []byte, tokenizeReq *tokenize.TokenizeRequestUnion, _ bool) (
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
		return nil, nil, fmt.Errorf("only TokenizeChatRequest is supported for gcp anthropic models")
	}

	if o.modelNameOverride != "" {
		// Use modelName override if set.
		o.requestModel = o.modelNameOverride
	}

	// Build the correct path for GCP Anthropic token counting
	// Use countTokens method as per: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/partner-models/claude/count-tokens
	path := buildGCPModelPathSuffix(gcpModelPublisherAnthropic, "count-tokens", gcpMethodRawPredict)

	anthropicReq, err := o.tokenizeToAnthropicMessages(tokenizeReq.TokenizeChatRequest, o.requestModel)
	if err != nil {
		return nil, nil, fmt.Errorf("error converting to Anthropic request: %w", err)
	}

	newBody, err = json.Marshal(anthropicReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling Anthropic messages request: %w", err)
	}

	// Add anthropic_version field (required by GCP)
	anthropicVersion := "vertex-2023-10-16" // Default version
	if o.apiVersion != "" {
		anthropicVersion = o.apiVersion
	}
	newBody, err = sjson.SetBytes(newBody, "anthropic_version", anthropicVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to set anthropic_version: %w", err)
	}

	newHeaders = []internalapi.Header{
		{pathHeaderName, path},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseError implements [TokenizeTranslator.ResponseError] for GCP Anthropic.
// Translate GCP Anthropic exceptions to OpenAI error type.
func (o *ToGCPAnthropicTranslatorV1Tokenize) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	return translateGCPAnthropicErrorToOpenAI(respHeaders, body)
}

// ResponseHeaders implements [TokenizeTranslator.ResponseHeaders].
func (o *ToGCPAnthropicTranslatorV1Tokenize) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [TokenizeTranslator.ResponseBody] for GCP Anthropic.
// This method translates a GCP Anthropic MessageTokensCount response to OpenAI tokenize format.
// GCP Anthropic uses deterministic model mapping without virtualization, where the requested model
// is exactly what gets executed. We extract token count from the token counting response.
func (o *ToGCPAnthropicTranslatorV1Tokenize) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.TokenizeSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	anthropicResp := &anthropic.MessageTokensCount{}
	if err = json.NewDecoder(body).Decode(&anthropicResp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	responseModel = o.requestModel

	// Convert to OpenAI format.
	openAIResp, err := o.anthropicTokensCountToTokenizeResponse(anthropicResp)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error converting Anthropic response to OpenAI format: %w", err)
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

// translateGCPAnthropicErrorToOpenAI translates GCP Anthropic error responses to OpenAI error format.
// This is a shared helper function for GCP Anthropic translators.
func translateGCPAnthropicErrorToOpenAI(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	var openaiError openai.Error
	var decodeErr error

	// Check for a JSON content type to decide how to parse the error.
	if v, ok := respHeaders[contentTypeHeaderName]; ok && strings.Contains(v, jsonContentType) {
		var gcpError anthropic.ErrorResponse
		if decodeErr = json.NewDecoder(body).Decode(&gcpError); decodeErr != nil {
			// If we expect JSON but fail to decode, it's an internal translator error.
			return nil, nil, fmt.Errorf("failed to unmarshal JSON error body: %w", decodeErr)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    gcpError.Error.Type,
				Message: gcpError.Error.Message,
				Code:    &statusCode,
			},
		}
	} else {
		// If not JSON, read the raw body as the error message.
		var buf []byte
		buf, decodeErr = io.ReadAll(body)
		if decodeErr != nil {
			return nil, nil, fmt.Errorf("failed to read raw error body: %w", decodeErr)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    gcpAnthropicBackendError,
				Message: string(buf),
				Code:    &statusCode,
			},
		}
	}

	// Marshal the translated OpenAI error.
	newBody, err = json.Marshal(openaiError)
	if err != nil {
		// This is an internal failure to create the response.
		return nil, nil, fmt.Errorf("failed to marshal OpenAI error body: %w", err)
	}
	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}
