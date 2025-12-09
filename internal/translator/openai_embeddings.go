// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewEmbeddingOpenAIToOpenAITranslator implements [Factory] for OpenAI to OpenAI translation for embeddings.
func NewEmbeddingOpenAIToOpenAITranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIEmbeddingTranslator {
	return &openAIToOpenAITranslatorV1Embedding{modelNameOverride: modelNameOverride, path: path.Join("/", apiVersion, "embeddings")}
}

// openAIToOpenAITranslatorV1Embedding is a passthrough translator for OpenAI Embeddings API.
// May apply model overrides but otherwise preserves the OpenAI format:
// https://platform.openai.com/docs/api-reference/embeddings/create
type openAIToOpenAITranslatorV1Embedding struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the embeddings endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
}

// RequestBody implements [OpenAIEmbeddingTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1Embedding) RequestBody(original []byte, _ *openai.EmbeddingRequest, onRetry bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		newBody, err = sjson.SetBytesOptions(original, "model", o.modelNameOverride, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
	}

	// Always set the path header to the embeddings endpoint so that the request is routed correctly.
	if onRetry && len(newBody) == 0 {
		newBody = original
	}
	newHeaders = []internalapi.Header{{pathHeaderName, o.path}}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [OpenAIEmbeddingTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1Embedding) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAIEmbeddingTranslator.ResponseBody].
// OpenAI embeddings support model virtualization through automatic routing and resolution,
// so we return the actual model from the response body which may differ from the requested model
// (e.g., request "text-embedding-3-small" → response with specific version).
func (o *openAIToOpenAITranslatorV1Embedding) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.EmbeddingsSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	var resp openai.EmbeddingResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}

	// Record the response in the span if successful.
	if span != nil {
		span.RecordResponse(&resp)
	}

	// Embeddings don't return output tokens; populate input and total when provided.
	tokenUsage.SetInputTokens(uint32(resp.Usage.PromptTokens)) //nolint:gosec
	tokenUsage.SetTotalTokens(uint32(resp.Usage.TotalTokens))  //nolint:gosec
	responseModel = resp.Model
	return
}

// ResponseError implements [Translator.ResponseError]
// For OpenAI based backend we return the OpenAI error type as is.
// If connection fails the error body is translated to OpenAI error type for events such as HTTP 503 or 504.
func (o *openAIToOpenAITranslatorV1Embedding) ResponseError(respHeaders map[string]string, body io.Reader) (
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
		newHeaders = []internalapi.Header{
			{contentTypeHeaderName, jsonContentType},
			{contentLengthHeaderName, strconv.Itoa(len(newBody))},
		}
	}
	return
}
