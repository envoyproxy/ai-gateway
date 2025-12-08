// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"

	sonic "github.com/bytedance/sonic"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewChatCompletionOpenAIToOpenAITranslator implements [Factory] for OpenAI to OpenAI translation.
func NewChatCompletionOpenAIToOpenAITranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToOpenAITranslatorV1ChatCompletion{modelNameOverride: modelNameOverride, path: path.Join("/", apiVersion, "chat/completions")}
}

// openAIToOpenAITranslatorV1ChatCompletion is a passthrough translator for OpenAI Chat Completions API.
// May apply model overrides but otherwise preserves the OpenAI format:
// https://platform.openai.com/docs/api-reference/chat/create
type openAIToOpenAITranslatorV1ChatCompletion struct {
	modelNameOverride internalapi.ModelNameOverride
	// requestModel serves as fallback for non-compliant OpenAI backends that
	// don't return model in responses, ensuring metrics/tracing always have a model.
	requestModel internalapi.RequestModel
	// streamingResponseModel stores the actual model from streaming responses
	streamingResponseModel internalapi.ResponseModel
	stream                 bool
	buffered               []byte
	// The path of the chat completions endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
}

// RequestBody implements [OpenAIChatCompletionTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1ChatCompletion) RequestBody(original []byte, req *openai.ChatCompletionRequest, forceBodyMutation bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	if req.Stream {
		o.stream = true
	}
	// Store the request model to use as fallback for response model
	o.requestModel = req.Model
	var newBody []byte
	// source tracks the latest version of the body we are mutating.
	// Start from the original raw body if present.
	source := original

	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		newBody, err = sjson.SetBytesOptions(source, "model", o.modelNameOverride, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
		// Make everything coherent.
		o.requestModel = o.modelNameOverride
		source = newBody
	}

	// For streaming requests, always ensure stream_options.include_usage=true so that
	// the backend reports token usage in the stream. This is done only when we have a
	// non-empty, valid JSON body to avoid changing existing behavior in tests and
	// non-JSON edge cases.
	if req.Stream && len(source) > 0 && sonic.Valid(source) {
		if newBody, err = sjson.SetBytesOptions(source, "stream_options.include_usage", true, sjsonOptions); err != nil {
			return nil, nil, fmt.Errorf("failed to set stream_options.include_usage: %w", err)
		}
		source = newBody
	}

	// Always set the path header to the chat completions endpoint so that the request is routed correctly.
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
	return
}

// ResponseError implements [OpenAIChatCompletionTranslator.ResponseError]
// For OpenAI based backend we return the OpenAI error type as is.
// If connection fails the error body is translated to OpenAI error type for events such as HTTP 503 or 504.
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
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

// ResponseHeaders implements [OpenAIChatCompletionTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseHeaders(map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAIChatCompletionTranslator.ResponseBody].
// OpenAI supports model virtualization through automatic routing and resolution,
// so we return the actual model from the response body which may differ from the requested model
// (e.g., request "gpt-4o" → response "gpt-4o-2024-08-06").
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.ChatCompletionSpan) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, responseModel string, err error,
) {
	if o.stream {
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, tokenUsage, o.requestModel, fmt.Errorf("failed to read body: %w", err)
		}
		o.buffered = append(o.buffered, buf...)
		tokenUsage = o.extractUsageFromBufferEvent(span)
		// Use stored streaming response model, fallback to request model for non-compliant backends
		responseModel = cmp.Or(o.streamingResponseModel, o.requestModel)
		return
	}
	resp := &openai.ChatCompletionResponse{}
	raw, readErr := io.ReadAll(body)
	if readErr != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to read body: %w", readErr)
	}
	if unmarshalErr := sonic.Unmarshal(raw, resp); unmarshalErr != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal body: %w", unmarshalErr)
	}
	tokenUsage = LLMTokenUsage{
		InputTokens:  uint32(resp.Usage.PromptTokens),     //nolint:gosec
		OutputTokens: uint32(resp.Usage.CompletionTokens), //nolint:gosec
		TotalTokens:  uint32(resp.Usage.TotalTokens),      //nolint:gosec
	}
	if resp.Usage.PromptTokensDetails != nil {
		tokenUsage.CachedInputTokens = uint32(resp.Usage.PromptTokensDetails.CachedTokens) //nolint:gosec
	}
	// Fallback to request model for test or non-compliant OpenAI backends
	responseModel = cmp.Or(resp.Model, o.requestModel)
	if span != nil {
		span.RecordResponse(resp)
	}
	return
}

var dataPrefix = []byte("data: ")

// extractUsageFromBufferEvent extracts the token usage from the buffered event.
// It scans complete lines and returns the latest usage found in this batch.
func (o *openAIToOpenAITranslatorV1ChatCompletion) extractUsageFromBufferEvent(span tracing.ChatCompletionSpan) (tokenUsage LLMTokenUsage) {
	for {
		i := bytes.IndexByte(o.buffered, '\n')
		if i == -1 {
			return
		}
		line := o.buffered[:i]
		o.buffered = o.buffered[i+1:]
		if !bytes.HasPrefix(line, dataPrefix) {
			continue
		}
		event := &openai.ChatCompletionResponseChunk{}
		if err := sonic.Unmarshal(bytes.TrimPrefix(line, dataPrefix), event); err != nil {
			continue
		}
		if span != nil {
			span.RecordResponseChunk(event)
		}
		if event.Model != "" {
			// Store the response model for future batches
			o.streamingResponseModel = event.Model
		}
		if usage := event.Usage; usage != nil {
			tokenUsage.InputTokens = uint32(usage.PromptTokens)      //nolint:gosec
			tokenUsage.OutputTokens = uint32(usage.CompletionTokens) //nolint:gosec
			tokenUsage.TotalTokens = uint32(usage.TotalTokens)       //nolint:gosec
			// Do not mark buffering done; keep scanning to return the latest usage in this batch.
		}
	}
}
