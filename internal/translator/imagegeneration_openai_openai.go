// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"

	openaisdk "github.com/openai/openai-go/v2"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewImageGenerationOpenAIToOpenAITranslator implements [Factory] for OpenAI to OpenAI image generation translation.
func NewImageGenerationOpenAIToOpenAITranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIImageGenerationTranslator {
	return &openAIToOpenAIImageGenerationTranslator{modelNameOverride: modelNameOverride, path: path.Join("/", apiVersion, "images/generations")}
}

// openAIToOpenAIImageGenerationTranslator implements [ImageGenerationTranslator] for /v1/images/generations.
type openAIToOpenAIImageGenerationTranslator struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the images generations endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
	// requestModel stores the effective model for this request (override or provided)
	// so we can attribute metrics later; the OpenAI Images response omits a model field.
	requestModel internalapi.RequestModel
}

// RequestBody implements [ImageGenerationTranslator.RequestBody].
func (o *openAIToOpenAIImageGenerationTranslator) RequestBody(original []byte, p *openaisdk.ImageGenerateParams, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		newBody, err = sjson.SetBytesOptions(original, "model", o.modelNameOverride, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
	}
	// Persist the effective model used. The Images endpoint omits model in responses,
	// so we derive it from the request (or override) for downstream metrics.
	o.requestModel = cmp.Or(o.modelNameOverride, p.Model)

	// Always set the path header to the images generations endpoint so that the request is routed correctly.
	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}
	newHeaders = []internalapi.Header{{pathHeaderName, o.path}}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, fmt.Sprintf("%d", len(newBody))})
	}
	return
}

// ResponseError implements [ImageGenerationTranslator.ResponseError]
// For OpenAI based backend we return the OpenAI error type as is.
// If connection fails the error body is translated to OpenAI error type for events such as HTTP 503 or 504.
func (o *openAIToOpenAIImageGenerationTranslator) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	// Read the upstream error body regardless of content-type. Some backends may mislabel it.
	buf, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read error body: %w", err)
	}
	// If upstream already returned JSON, preserve it as-is.
	if json.Valid(buf) {
		return nil, nil, nil
	}
	// Otherwise, wrap the plain-text (or non-JSON) error into OpenAI REST error schema.
	openaiError := struct {
		Error openai.ErrorType `json:"error"`
	}{
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
	// Ensure downstream sees a JSON error payload
	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseHeaders implements [ImageGenerationTranslator.ResponseHeaders].
func (o *openAIToOpenAIImageGenerationTranslator) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [ImageGenerationTranslator.ResponseBody].
func (o *openAIToOpenAIImageGenerationTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.ImageGenerationSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	// Decode using OpenAI SDK v2 schema to avoid drift.
	resp := &openaisdk.ImagesResponse{}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to decode response body: %w", err)
	}

	// Populate token usage if provided (GPT-Image-1); otherwise remain zero.
	if resp.JSON.Usage.Valid() {
		tokenUsage.SetInputTokens(uint32(resp.Usage.InputTokens))   //nolint:gosec
		tokenUsage.SetOutputTokens(uint32(resp.Usage.OutputTokens)) //nolint:gosec
		tokenUsage.SetTotalTokens(uint32(resp.Usage.TotalTokens))   //nolint:gosec
	}

	// There is no response model field, so use the request one.
	responseModel = o.requestModel

	// Record the response in the span if tracing is enabled.
	if span != nil {
		span.RecordResponse(resp)
	}

	return
}
