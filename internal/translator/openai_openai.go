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

// NewChatCompletionOpenAIToOpenAITranslator implements [Factory] for OpenAI to OpenAI translation.
func NewChatCompletionOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToOpenAITranslatorV1ChatCompletion{modelNameOverride: modelNameOverride, path: path.Join("/", prefix, "chat/completions")}
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
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	if req.Stream {
		o.stream = true
	}
	// Store the request model to use as fallback for response model
	o.requestModel = req.Model
	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		newBody, err = sjson.SetBytesOptions(original, "model", o.modelNameOverride, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
		// Make everything coherent.
		o.requestModel = o.modelNameOverride
	}

	// Always set the path header to the chat completions endpoint so that the request is routed correctly.
	newHeaders = []internalapi.Header{{pathHeaderName, o.path}}

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseError implements [OpenAIChatCompletionTranslator.ResponseError]
// For OpenAI based backend we return the OpenAI error type as is.
// If connection fails the error body is translated to OpenAI error type for events such as HTTP 503 or 504.
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
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

// ResponseHeaders implements [OpenAIChatCompletionTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAIChatCompletionTranslator.ResponseBody].
// OpenAI supports model virtualization through automatic routing and resolution,
// so we return the actual model from the response body which may differ from the requested model
// (e.g., request "gpt-4o" → response "gpt-4o-2024-08-06").
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
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
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	tokenUsage.SetInputTokens(uint32(resp.Usage.PromptTokens))      //nolint:gosec
	tokenUsage.SetOutputTokens(uint32(resp.Usage.CompletionTokens)) //nolint:gosec
	tokenUsage.SetTotalTokens(uint32(resp.Usage.TotalTokens))       //nolint:gosec
	if resp.Usage.PromptTokensDetails != nil {
		tokenUsage.SetCachedInputTokens(uint32(resp.Usage.PromptTokensDetails.CachedTokens))               //nolint:gosec
		tokenUsage.SetCacheCreationInputTokens(uint32(resp.Usage.PromptTokensDetails.CacheCreationTokens)) //nolint:gosec
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
func (o *openAIToOpenAITranslatorV1ChatCompletion) extractUsageFromBufferEvent(span tracing.ChatCompletionSpan) (tokenUsage metrics.TokenUsage) {
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
		if err := json.Unmarshal(bytes.TrimPrefix(line, dataPrefix), event); err != nil {
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
			tokenUsage.SetInputTokens(uint32(usage.PromptTokens))      //nolint:gosec
			tokenUsage.SetOutputTokens(uint32(usage.CompletionTokens)) //nolint:gosec
			tokenUsage.SetTotalTokens(uint32(usage.TotalTokens))       //nolint:gosec
			// Do not mark buffering done; keep scanning to return the latest usage in this batch.
		}
	}
}
