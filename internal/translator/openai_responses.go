// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewResponsesOpenAIToOpenAITranslator implements [OpenAIResponsesTranslator] for OpenAI to OpenAI translation for responses.
func NewResponsesOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) OpenAIResponsesTranslator {
	return &openAIToOpenAITranslatorV1Responses{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", prefix, "responses"),
	}
}

// openAIToOpenAITranslatorV1Responses is a passthrough translator for OpenAI Responses API.
// May apply model overrides but otherwise preserves the OpenAI format:
// https://platform.openai.com/docs/api-reference/responses/create
type openAIToOpenAITranslatorV1Responses struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the responses endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
	// stream indicates whether the request is for streaming.
	stream bool
	// streamingResponseModel stores the actual model from streaming responses.
	streamingResponseModel internalapi.ResponseModel
	// requestModel serves as fallback for non-compliant OpenAI backends that
	// don't return model in responses, ensuring metrics/tracing always have a model.
	requestModel internalapi.RequestModel
}

// RequestBody implements [OpenAIResponsesTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1Responses) RequestBody(original []byte, req *openai.ResponseRequest, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	// Store the request model to use as fallback for response model
	o.requestModel = req.Model
	if o.modelNameOverride != "" {
		// If modelNameOverride is set, we override the model to be used for the request.
		newBody, err = sjson.SetBytesOptions(original, "model", o.modelNameOverride, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model: %w", err)
		}
		o.requestModel = o.modelNameOverride
	}

	// Track if this is a streaming request.
	o.stream = req.Stream

	// Always set the path header to the responses endpoint so that the request is routed correctly.
	newHeaders = []internalapi.Header{{pathHeaderName, o.path}}

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [OpenAIResponsesTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1Responses) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	// For OpenAI to OpenAI translation, we don't need to mutate the response headers.
	return nil, nil
}

// ResponseBody implements [OpenAIResponsesTranslator.ResponseBody].
// OpenAI responses support model virtualization through automatic routing and resolution,
// so we return the actual model from the response body which may differ from the requested model.
func (o *openAIToOpenAITranslatorV1Responses) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.ResponsesSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	if o.stream {
		// Handle streaming response
		return o.handleStreamingResponse(body, span)
	}

	// Handle non-streaming response
	return o.handleNonStreamingResponse(body, span)
}

// handleStreamingResponse handles streaming responses from the Responses API.
func (o *openAIToOpenAITranslatorV1Responses) handleStreamingResponse(body io.Reader, span tracing.ResponsesSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	// Buffer the incoming SSE data
	chunks, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to read body: %w", err)
	}
	tokenUsage = o.extractUsageFromBufferEvent(span, chunks)
	// Use stored streaming response model, fallback to request model for non-compliant backends
	responseModel = cmp.Or(o.streamingResponseModel, o.requestModel)
	return
}

// handleNonStreamingResponse handles non-streaming responses from the Responses API.
func (o *openAIToOpenAITranslatorV1Responses) handleNonStreamingResponse(body io.Reader, span tracing.ResponsesSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	var resp openai.Response
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	// Fallback to request model for test or non-compliant OpenAI backends
	responseModel = cmp.Or(resp.Model, o.requestModel)

	// TODO: Add reasoning token usage
	if resp.Usage != nil {
		tokenUsage.SetInputTokens(uint32(resp.Usage.InputTokens))                           // #nosec G115
		tokenUsage.SetOutputTokens(uint32(resp.Usage.OutputTokens))                         // #nosec G115
		tokenUsage.SetTotalTokens(uint32(resp.Usage.TotalTokens))                           // #nosec G115
		tokenUsage.SetCachedInputTokens(uint32(resp.Usage.InputTokensDetails.CachedTokens)) // #nosec G115
	}

	// Record non-streaming response to span if tracing is enabled.
	if span != nil {
		span.RecordResponse(&resp)
	}
	return
}

// extractUsageFromBufferEvent extracts the token usage and model from the buffered SSE events.
// It scans complete lines and returns the latest usage found in response.completed event.
func (o *openAIToOpenAITranslatorV1Responses) extractUsageFromBufferEvent(span tracing.ResponsesSpan, chunks []byte) (tokenUsage metrics.TokenUsage) {
	// Parse SSE events from the buffered data
	// SSE format: "data: {json}\n\n"
	for event := range bytes.SplitSeq(chunks, []byte("\n\n")) {
		for line := range bytes.SplitSeq(event, []byte("\n")) {
			// Look for lines starting with "data: "
			if !bytes.HasPrefix(line, dataPrefix) {
				continue
			}

			data := bytes.TrimPrefix(line, dataPrefix)
			if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
				continue
			}

			var eventUnion openai.ResponseStreamEventUnion
			if err := json.Unmarshal(data, &eventUnion); err != nil {
				continue // skip invalid JSON
			}

			switch eventUnion.Type {
			case "response.created":
				// Extract model from the first streaming event.
				respCreatedEvent := eventUnion.AsResponseCreated()
				if respCreatedEvent.Response.Model != "" {
					o.streamingResponseModel = respCreatedEvent.Response.Model
				}
			case "response.completed":
				// Extract token usage from response.completed event.
				// Only response.completed contains usage information.
				respComplEvent := eventUnion.AsResponseCompleted()
				tokenUsage.SetInputTokens(uint32(respComplEvent.Response.Usage.InputTokens))                           // #nosec G115
				tokenUsage.SetOutputTokens(uint32(respComplEvent.Response.Usage.OutputTokens))                         // #nosec G115
				tokenUsage.SetTotalTokens(uint32(respComplEvent.Response.Usage.TotalTokens))                           // #nosec G115
				tokenUsage.SetCachedInputTokens(uint32(respComplEvent.Response.Usage.InputTokensDetails.CachedTokens)) // #nosec G115
			}
			// Record streaming chunk to span if tracing is enabled.
			if span != nil {
				span.RecordResponseChunk(&eventUnion)
			}
		}
	}
	return tokenUsage
}

// ResponseError implements [OpenAIResponsesTranslator.ResponseError].
// For OpenAI to OpenAI translation, we don't need to mutate error responses.
// The error format is already in OpenAI format.
// If connection fails the error body is translated to OpenAI error type for events such as HTTP 503 or 504.
func (o *openAIToOpenAITranslatorV1Responses) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	if v, ok := respHeaders[contentTypeHeaderName]; ok && !strings.Contains(v, jsonContentType) {
		var openaiError openai.Error
		buf, err := io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read error body: %w", err)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    openAIBackendError,
				Message: string(buf),
				Code:    &statusCode,
			},
		}
		newBody, err = json.Marshal(openaiError)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
		}
		newHeaders = append(newHeaders,
			internalapi.Header{contentTypeHeaderName, jsonContentType},
			internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))},
		)
	}
	return
}
