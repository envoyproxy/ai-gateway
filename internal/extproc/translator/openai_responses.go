// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewResponsesOpenAIToOpenAITranslator implements [OpenAIResponsesTranslator] for OpenAI to OpenAI translation for responses.
func NewResponsesOpenAIToOpenAITranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIResponsesTranslator {
	return &openAIToOpenAITranslatorV1Responses{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", apiVersion, "responses"),
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
	// buffered accumulates SSE chunks for streaming responses.
	buffered []byte
	// streamingResponseModel stores the actual model from streaming responses.
	streamingResponseModel internalapi.ResponseModel
	// requestModel serves as fallback for non-compliant OpenAI backends that
	// don't return model in responses, ensuring metrics/tracing always have a model.
	requestModel internalapi.RequestModel
}

// RequestBody implements [OpenAIResponsesTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1Responses) RequestBody(original []byte, req *openai.ResponseRequest, forceBodyMutation bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	var newBody []byte
	modelName := req.Model
	if o.modelNameOverride != "" {
		// If modelNameOverride is set, we override the model to be used for the request.
		modelName = o.modelNameOverride
		newBody, err = sjson.SetBytesOptions(original, "model", modelName, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model: %w", err)
		}
	}

	// Store the request model
	o.requestModel = modelName
	// Track if this is a streaming request.
	o.stream = req.Stream

	// Always set the path header to the responses endpoint so that the request is routed correctly.
	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: []byte(o.path),
			}},
		},
	}

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		bodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: newBody},
		}
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(strconv.Itoa(len(newBody))),
		}})
	}

	return headerMutation, bodyMutation, nil
}

// ResponseHeaders implements [OpenAIResponsesTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1Responses) ResponseHeaders(map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	// For OpenAI to OpenAI translation, we don't need to mutate the response headers.
	return nil, nil
}

// ResponseBody implements [OpenAIResponsesTranslator.ResponseBody].
// OpenAI responses support model virtualization through automatic routing and resolution,
// so we return the actual model from the response body which may differ from the requested model.
func (o *openAIToOpenAITranslatorV1Responses) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracing.ResponsesSpan) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	if o.stream {
		// Handle streaming response
		return o.handleStreamingResponse(body, endOfStream, span)
	}

	// Handle non-streaming response
	return o.handleNonStreamingResponse(body, span)
}

// handleStreamingResponse handles streaming responses from the Responses API.
func (o *openAIToOpenAITranslatorV1Responses) handleStreamingResponse(body io.Reader, endOfStream bool, span tracing.ResponsesSpan) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	// Buffer the incoming SSE data
	chunk, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to read body: %w", err)
	}
	o.buffered = append(o.buffered, chunk...)

	// Extract token usage from the buffered events if this is the end of the stream
	if endOfStream {
		tokenUsage = o.extractUsageFromBufferEvent(span)
		responseModel = o.streamingResponseModel
		if responseModel == "" {
			responseModel = o.requestModel
		}
	}

	// Pass through the original body without re-encoding to preserve formatting
	return nil, nil, tokenUsage, responseModel, nil
}

// handleNonStreamingResponse handles non-streaming responses from the Responses API.
func (o *openAIToOpenAITranslatorV1Responses) handleNonStreamingResponse(body io.Reader, span tracing.ResponsesSpan) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to read body: %w", err)
	}

	var resp openai.ResponseResponse
	if decodeErr := json.Unmarshal(bodyBytes, &resp); decodeErr != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", decodeErr)
	}

	// Extract the response model
	responseModel = resp.Model
	if responseModel == "" {
		responseModel = o.requestModel
	}

	// TODO: Add reasoning token usage
	// Extract token usage if available
	if resp.Usage != nil {
		// Safely convert int to uint32 with bounds checking
		if resp.Usage.InputTokens >= 0 {
			tokenUsage.InputTokens = uint32(resp.Usage.InputTokens) // #nosec G115
		}
		if resp.Usage.OutputTokens >= 0 {
			tokenUsage.OutputTokens = uint32(resp.Usage.OutputTokens) // #nosec G115
		}
		if resp.Usage.TotalTokens >= 0 {
			tokenUsage.TotalTokens = uint32(resp.Usage.TotalTokens) // #nosec G115
		}
	}

	// Record non-streaming response to span if tracing is enabled.
	if span != nil {
		span.RecordResponse(&resp)
	}

	// Pass through the original body without re-encoding to preserve formatting
	return nil, nil, tokenUsage, responseModel, nil
}

// extractUsageFromBufferEvent extracts the token usage and model from the buffered SSE events.
// It scans complete lines and returns the latest usage found in response.completed event.
func (o *openAIToOpenAITranslatorV1Responses) extractUsageFromBufferEvent(span tracing.ResponsesSpan) (tokenUsage LLMTokenUsage) {
	// Parse SSE events from the buffered data
	// SSE format: "data: {json}\n\n"
	events := bytes.Split(o.buffered, []byte("\n\n"))

	for _, event := range events {
		lines := bytes.Split(event, []byte("\n"))
		for _, line := range lines {
			// Look for lines starting with "data: "
			if !bytes.HasPrefix(line, dataPrefix) {
				continue
			}

			data := bytes.TrimPrefix(line, dataPrefix)
			if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
				continue
			}

			// Try to parse as ResponseCompletedEvent
			var chunk openai.ResponseCompletedEvent
			if err := json.Unmarshal(data, &chunk); err != nil {
				continue // skip other chunks as only ResponseCompletedEvent contains usage
			}

			if chunk.Type == "response.completed" {

				if chunk.Response.Usage != nil {
					if chunk.Response.Usage.InputTokens >= 0 {
						tokenUsage.InputTokens = uint32(chunk.Response.Usage.InputTokens) // #nosec G115
					}
					if chunk.Response.Usage.OutputTokens >= 0 {
						tokenUsage.OutputTokens = uint32(chunk.Response.Usage.OutputTokens) // #nosec G115
					}
					if chunk.Response.Usage.TotalTokens >= 0 {
						tokenUsage.TotalTokens = uint32(chunk.Response.Usage.TotalTokens) // #nosec G115
					}
				}
				if chunk.Response.Model != "" {
					o.streamingResponseModel = chunk.Response.Model
				}

				// Record streaming chunk to span if tracing is enabled.
				if span != nil {
					span.RecordResponseChunk(&chunk)
				}
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
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	if v, ok := respHeaders[contentTypeHeaderName]; ok && v != jsonContentType {
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
		mut := &extprocv3.BodyMutation_Body{}
		mut.Body, err = json.Marshal(openaiError)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
		}
		headerMutation = &extprocv3.HeaderMutation{}
		setContentLength(headerMutation, mut.Body)
		return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, nil
	}
	return nil, nil, nil
}
