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

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	openaisdk "github.com/openai/openai-go/v2"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewImageGenerationOpenAIToOpenAITranslator implements [Factory] for OpenAI to OpenAI image generation translation.
func NewImageGenerationOpenAIToOpenAITranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride, span tracing.ImageGenerationSpan) ImageGenerationTranslator {
	return &openAIToOpenAIImageGenerationTranslator{modelNameOverride: modelNameOverride, path: path.Join("/", apiVersion, "images/generations"), span: span}
}

// openAIToOpenAIImageGenerationTranslator implements [ImageGenerationTranslator] for /v1/images/generations.
type openAIToOpenAIImageGenerationTranslator struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the images generations endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
	// span is the tracing span for this request, inherited from the router filter.
	span tracing.ImageGenerationSpan
	// requestModel stores the effective model for this request (override or provided)
	// so we can attribute metrics later; the OpenAI Images response omits a model field.
	requestModel internalapi.RequestModel
}

// RequestBody implements [ImageGenerationTranslator.RequestBody].
func (o *openAIToOpenAIImageGenerationTranslator) RequestBody(original []byte, p *openaisdk.ImageGenerateParams, forceBodyMutation bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	var newBody []byte
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

// ResponseError implements [ImageGenerationTranslator.ResponseError]
// For OpenAI based backend we return the OpenAI error type as is.
// If connection fails the error body is translated to OpenAI error type for events such as HTTP 503 or 504.
func (o *openAIToOpenAIImageGenerationTranslator) ResponseError(respHeaders map[string]string, body io.Reader) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
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
	// Otherwise, wrap the plain-text (or non-JSON) error into OpenAI Images error schema.
	openaiError := ImageGenerationError{
		Error: struct {
			Type    string  `json:"type"`
			Message string  `json:"message"`
			Code    *string `json:"code,omitempty"`
			Param   *string `json:"param,omitempty"`
		}{
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
	// Ensure downstream sees a JSON error payload
	headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{Header: &corev3.HeaderValue{
		Key:      contentTypeHeaderName,
		RawValue: []byte(jsonContentType),
	}})
	setContentLength(headerMutation, mut.Body)
	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, nil
}

// ResponseHeaders implements [ImageGenerationTranslator.ResponseHeaders].
func (o *openAIToOpenAIImageGenerationTranslator) ResponseHeaders(map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	return nil, nil
}

// ResponseBody implements [ImageGenerationTranslator.ResponseBody].
func (o *openAIToOpenAIImageGenerationTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, imageMetadata ImageGenerationMetadata, err error,
) {
	// Decode using OpenAI SDK v2 schema to avoid drift.
	resp := &openaisdk.ImagesResponse{}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, imageMetadata, fmt.Errorf("failed to decode response body: %w", err)
	}

	// Populate token usage if provided (GPT-Image-1); otherwise remain zero.
	if resp.JSON.Usage.Valid() {
		tokenUsage.InputTokens = uint32(resp.Usage.InputTokens)   //nolint:gosec
		tokenUsage.OutputTokens = uint32(resp.Usage.OutputTokens) //nolint:gosec
		tokenUsage.TotalTokens = uint32(resp.Usage.TotalTokens)   //nolint:gosec
	}

	// Extract image generation metadata for metrics.
	imageMetadata.ImageCount = len(resp.Data)
	imageMetadata.Model = o.requestModel // Model is not present in the response, so we assume the request model == response model.
	imageMetadata.Size = string(resp.Size)

	return
}
