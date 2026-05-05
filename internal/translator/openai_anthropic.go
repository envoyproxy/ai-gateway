// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

const (
	// Default Anthropic API version - most commonly used version
	// https://docs.anthropic.com/en/api/versioning
	anthropicDefaultVersion = "2023-06-01"
	anthropicBackendError   = "AnthropicBackendError"
)

// NewChatCompletionOpenAIToAnthropicTranslator implements [Factory] for OpenAI to Anthropic translation.
// This translator converts OpenAI ChatCompletion API requests to Anthropic Messages API format.
// Unlike cloud-based translators (AWS/GCP), this targets the direct Anthropic API.
func NewChatCompletionOpenAIToAnthropicTranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToAnthropicTranslatorV1ChatCompletion{
		apiVersion:        apiVersion,
		modelNameOverride: modelNameOverride,
	}
}

// openAIToAnthropicTranslatorV1ChatCompletion translates OpenAI Chat Completions API to Anthropic Messages API.
// This uses the direct Anthropic API: https://docs.anthropic.com/en/api/messages
type openAIToAnthropicTranslatorV1ChatCompletion struct {
	apiVersion        string
	modelNameOverride internalapi.ModelNameOverride
	streamParser      *anthropicStreamParser
	requestModel      internalapi.RequestModel
	// Redaction configuration for debug logging
	debugLogEnabled bool
	enableRedaction bool
	logger          *slog.Logger
}

// RequestBody implements [OpenAIChatCompletionTranslator.RequestBody] for Anthropic.
func (o *openAIToAnthropicTranslatorV1ChatCompletion) RequestBody(_ []byte, openAIReq *openai.ChatCompletionRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	// Resolve the request model up front so it's always recorded, even if
	// buildAnthropicParams fails. This keeps response-side fallbacks consistent.
	o.requestModel = openAIReq.Model
	if o.modelNameOverride != "" {
		o.requestModel = o.modelNameOverride
	}

	// Build Anthropic parameters from OpenAI request.
	params, err := buildAnthropicParams(openAIReq, "Anthropic", o.modelNameOverride)
	if err != nil {
		return
	}

	// buildAnthropicParams intentionally leaves Model unset so cloud variants
	// (AWS Bedrock / GCP Vertex AI) can place the model in the URL path. The
	// direct Anthropic API requires it in the request body, so set it here.
	params.Model = o.requestModel

	body, err := json.Marshal(params)
	if err != nil {
		return
	}

	// Initialize stream parser and set stream field if this is a streaming request.
	if openAIReq.Stream {
		body, err = sjson.SetBytes(body, "stream", true)
		if err != nil {
			return
		}
		o.streamParser = newAnthropicStreamParser(o.requestModel)
	}

	newBody = body

	// The direct Anthropic API requires the version to be sent as the
	// `anthropic-version` HTTP header (https://docs.anthropic.com/en/api/versioning).
	// Note: `anthropic_version` in the JSON body is specific to AWS Bedrock and GCP
	// Vertex AI variants and is not used here.
	anthropicVersion := anthropicDefaultVersion
	if o.apiVersion != "" {
		anthropicVersion = o.apiVersion
	}

	newHeaders = []internalapi.Header{
		{pathHeaderName, "/v1/messages"},
		{anthropicVersionHeaderName, anthropicVersion},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseError implements [OpenAIChatCompletionTranslator.ResponseError].
func (o *openAIToAnthropicTranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	var openaiError openai.Error

	// Check for a JSON content type to decide how to parse the error
	if v, ok := respHeaders[contentTypeHeaderName]; ok && strings.Contains(v, jsonContentType) {
		var anthropicError anthropic.ErrorResponse
		if err = json.NewDecoder(body).Decode(&anthropicError); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal Anthropic error body: %w", err)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    anthropicError.Error.Type,
				Message: anthropicError.Error.Message,
				Code:    &statusCode,
			},
		}
	} else {
		// If not JSON, read the raw body as the error message
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read raw error body: %w", err)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    anthropicBackendError,
				Message: string(buf),
				Code:    &statusCode,
			},
		}
	}

	// Marshal the translated OpenAI error
	newBody, err = json.Marshal(openaiError)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal OpenAI error body: %w", err)
	}
	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// SetRedactionConfig implements [ResponseRedactor.SetRedactionConfig].
func (o *openAIToAnthropicTranslatorV1ChatCompletion) SetRedactionConfig(debugLogEnabled, enableRedaction bool, logger *slog.Logger) {
	o.debugLogEnabled = debugLogEnabled
	o.enableRedaction = enableRedaction
	o.logger = logger
}

// RedactBody implements [ResponseRedactor.RedactBody].
// Creates a redacted copy of the response for safe logging without modifying the original.
func (o *openAIToAnthropicTranslatorV1ChatCompletion) RedactBody(resp *openai.ChatCompletionResponse) *openai.ChatCompletionResponse {
	return redactAnthropicChatCompletionResponse(resp)
}

// ResponseHeaders implements [OpenAIChatCompletionTranslator.ResponseHeaders].
func (o *openAIToAnthropicTranslatorV1ChatCompletion) ResponseHeaders(_ map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	if o.streamParser != nil {
		newHeaders = []internalapi.Header{{contentTypeHeaderName, eventStreamContentType}}
	}
	return
}

// ResponseBody implements [OpenAIChatCompletionTranslator.ResponseBody] for Anthropic.
// Anthropic uses deterministic model mapping without virtualization, where the requested model
// is exactly what gets executed. The response contains a model field that we use.
func (o *openAIToAnthropicTranslatorV1ChatCompletion) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracingapi.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	// If a stream parser was initialized, this is a streaming request
	if o.streamParser != nil {
		return o.streamParser.Process(body, endOfStream, span)
	}

	var anthropicResp anthropic.Message
	if err = json.NewDecoder(body).Decode(&anthropicResp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}

	responseModel = o.requestModel
	if anthropicResp.Model != "" {
		responseModel = anthropicResp.Model
	}

	openAIResp, tokenUsage, err := messageToChatCompletion(&anthropicResp, responseModel)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", err
	}

	// Redact and log response when enabled
	if o.debugLogEnabled && o.enableRedaction && o.logger != nil {
		redactedResp := o.RedactBody(openAIResp)
		if jsonBody, marshalErr := json.Marshal(redactedResp); marshalErr == nil {
			o.logger.Debug("response body processing", slog.Any("response", string(jsonBody)))
		}
	}

	newBody, err = json.Marshal(openAIResp)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to marshal body: %w", err)
	}

	if span != nil {
		span.RecordResponse(openAIResp)
	}
	newHeaders = []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return
}
