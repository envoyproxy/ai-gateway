// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"cmp"
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
	// anthropicBackendError is the error type used for non-JSON error responses
	// from a first-party Anthropic backend.
	anthropicBackendError = "AnthropicBackendError"
	// anthropicVersionHeaderName is the HTTP header that carries the Anthropic
	// API version. The Anthropic API requires this header on every request;
	// the framework does not inject it, so the translator must set it.
	anthropicVersionHeaderName = "anthropic-version"
	// anthropicDefaultVersion is the version the official Anthropic SDK pins by
	// default (see anthropic-sdk-go/internal/requestconfig). Used when no
	// explicit version is supplied via the schema.
	anthropicDefaultVersion = "2023-06-01"
)

// NewChatCompletionOpenAIToAnthropicTranslator implements [Factory] for OpenAI to
// first-party Anthropic translation. It converts OpenAI ChatCompletion API
// requests to the native Anthropic Messages API (/v1/messages).
//
// apiVersion is sent as the `anthropic-version` HTTP header on every request
// (defaulting to `2023-06-01` when empty). It is NOT injected into the request
// body — this differs from the GCP/AWS siblings, which instead set the
// `anthropic_version` body key because rawPredict/streamRawPredict carry the
// version in the JSON payload rather than a header.
func NewChatCompletionOpenAIToAnthropicTranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToAnthropicTranslatorV1ChatCompletion{
		apiVersion:        apiVersion,
		modelNameOverride: modelNameOverride,
	}
}

// openAIToAnthropicTranslatorV1ChatCompletion translates OpenAI Chat Completions
// API to the native Anthropic Messages API:
// https://docs.anthropic.com/en/api/messages
//
// Unlike the GCP/AWS variants there is no rawPredict path, no anthropic_version
// body key, and no eventstream unwrapping — the wire format is plain Anthropic
// JSON / SSE.
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
	// "Anthropic" makes buildAnthropicParams treat this as a non-GCP backend
	// (isGCPBackend=false), which enables structured outputs — correct for the
	// native Anthropic API.
	params, err := buildAnthropicParams(openAIReq, "Anthropic", o.modelNameOverride)
	if err != nil {
		return
	}

	body, err := json.Marshal(params)
	if err != nil {
		return
	}

	o.requestModel = openAIReq.Model
	if o.modelNameOverride != "" {
		o.requestModel = o.modelNameOverride
	}

	// First-party /v1/messages requires the model in the request body (unlike
	// GCP Vertex, where the model lives in the rawPredict URL).
	body, err = sjson.SetBytes(body, "model", o.requestModel)
	if err != nil {
		return
	}

	if openAIReq.Stream {
		body, err = sjson.SetBytes(body, "stream", true)
		if err != nil {
			return
		}
		o.streamParser = newAnthropicStreamParser(o.requestModel)
	}

	newBody = body
	newHeaders = []internalapi.Header{
		{pathHeaderName, "/v1/messages"},
		{anthropicVersionHeaderName, o.anthropicVersion()},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// anthropicVersion returns the Anthropic API version to send, defaulting to the
// SDK's pinned version when no explicit version was configured.
func (o *openAIToAnthropicTranslatorV1ChatCompletion) anthropicVersion() string {
	if o.apiVersion != "" {
		return o.apiVersion
	}
	return anthropicDefaultVersion
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
func (o *openAIToAnthropicTranslatorV1ChatCompletion) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracingapi.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	// If a stream parser was initialized, this is a streaming request. First-party
	// Anthropic SSE uses the same wire format as GCP Anthropic, so no unwrapping.
	if o.streamParser != nil {
		return o.streamParser.Process(body, endOfStream, span)
	}

	var anthropicResp anthropic.Message
	if err = json.NewDecoder(body).Decode(&anthropicResp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}

	responseModel = cmp.Or(anthropicResp.Model, o.requestModel)

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

// ResponseError implements [OpenAIChatCompletionTranslator.ResponseError].
func (o *openAIToAnthropicTranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	var openaiError openai.Error
	var decodeErr error

	// Check for a JSON content type to decide how to parse the error.
	if v, ok := respHeaders[contentTypeHeaderName]; ok && strings.Contains(v, jsonContentType) {
		var anthropicError anthropic.ErrorResponse
		if decodeErr = json.NewDecoder(body).Decode(&anthropicError); decodeErr != nil {
			// If we expect JSON but fail to decode, it's an internal translator error.
			return nil, nil, fmt.Errorf("failed to unmarshal JSON error body: %w", decodeErr)
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
		// If not JSON, read the raw body as the error message.
		var buf []byte
		buf, decodeErr = io.ReadAll(body)
		if decodeErr != nil {
			return nil, nil, fmt.Errorf("failed to read raw error body: %w", decodeErr)
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

	// Marshal the translated OpenAI error.
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
// Reuses the provider-agnostic redaction helper since Anthropic responses are converted to
// OpenAI format before redaction.
func (o *openAIToAnthropicTranslatorV1ChatCompletion) RedactBody(resp *openai.ChatCompletionResponse) *openai.ChatCompletionResponse {
	if resp == nil {
		return nil
	}

	// Create a shallow copy of the response
	redacted := *resp

	// Redact choices (contains AI-generated content)
	if len(resp.Choices) > 0 {
		redacted.Choices = make([]openai.ChatCompletionResponseChoice, len(resp.Choices))
		for i := range resp.Choices {
			redactedChoice := resp.Choices[i]
			redactedChoice.Message = redactAnthropicResponseMessage(&resp.Choices[i].Message)
			redacted.Choices[i] = redactedChoice
		}
	}

	return &redacted
}
