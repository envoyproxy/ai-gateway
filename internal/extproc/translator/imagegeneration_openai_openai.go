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

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"

	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	openaisdk "github.com/openai/openai-go/v2"
)

// NewImageGenerationOpenAIToOpenAITranslator implements [Factory] for OpenAI to OpenAI image generation translation.
func NewImageGenerationOpenAIToOpenAITranslator(apiVersion string, modelNameOverride string) ImageGenerationTranslator {
	return &openAIToOpenAIImageGenerationTranslator{modelNameOverride: modelNameOverride, path: path.Join("/", apiVersion, "images/generations")}
}

// openAIToOpenAIImageGenerationTranslator implements [ImageGenerationTranslator] for /v1/images/generations.
type openAIToOpenAIImageGenerationTranslator struct {
	modelNameOverride string
	// The path of the images generations endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
}

// RequestBody implements [ImageGenerationTranslator.RequestBody].
func (o *openAIToOpenAIImageGenerationTranslator) RequestBody(original []byte, req *openaisdk.ImageGenerateParams, forceBodyMutation bool) (
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
		// Note: content-length header is set via dynamic metadata in the processor
		// to avoid conflicts with Envoy's REPLACE_AND_CONTINUE processing mode
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
	if v, ok := respHeaders[contentTypeHeaderName]; ok && v != jsonContentType {
		var openaiError ImageGenerationError
		buf, err := io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read error body: %w", err)
		}
		openaiError = ImageGenerationError{
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
	return nil, nil, nil
}

// ResponseHeaders implements [ImageGenerationTranslator.ResponseHeaders].
func (o *openAIToOpenAIImageGenerationTranslator) ResponseHeaders(map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	return nil, nil
}

// ResponseBody implements [ImageGenerationTranslator.ResponseBody].
func (o *openAIToOpenAIImageGenerationTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, imageMetadata ImageGenerationMetadata, err error,
) {
	// Read the entire response body first to debug any issues
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, imageMetadata, fmt.Errorf("failed to read response body: %w", err)
	}

	// Debug logging for response body content
	bodyPreview := string(bodyBytes)
	if len(bodyPreview) > 200 {
		bodyPreview = bodyPreview[:200] + "..."
	}
	fmt.Printf("DEBUG: Image generation translator received body - Length: %d, Preview: %s\n", len(bodyBytes), bodyPreview)

	// Check if body looks like JSON
	if len(bodyBytes) > 0 && bodyBytes[0] != '{' && bodyBytes[0] != '[' {
		previewLen := 10
		if len(bodyBytes) < previewLen {
			previewLen = len(bodyBytes)
		}
		fmt.Printf("DEBUG: Body does not start with JSON character. First %d bytes: %v\n", previewLen, bodyBytes[:previewLen])
	}

	// Decode using OpenAI SDK v2 schema to avoid drift.
	resp := &openaisdk.ImagesResponse{}
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		fmt.Printf("DEBUG: JSON unmarshal failed - Error: %v, Body preview: %s\n", err, bodyPreview)
		return nil, nil, tokenUsage, imageMetadata, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	// Populate token usage if provided (GPT-Image-1); otherwise remain zero.
	if resp.JSON.Usage.Valid() {
		tokenUsage.InputTokens = uint32(resp.Usage.InputTokens)   //nolint:gosec
		tokenUsage.OutputTokens = uint32(resp.Usage.OutputTokens) //nolint:gosec
		tokenUsage.TotalTokens = uint32(resp.Usage.TotalTokens)   //nolint:gosec
	}

	// Extract image generation metadata for metrics (model may be absent in SDK response)
	imageMetadata.ImageCount = len(resp.Data)
	imageMetadata.Model = ""
	imageMetadata.Size = string(resp.Size)

	return
}

// extractUsageFromBufferEvent extracts token usage from buffered streaming events.
// This is currently not applicable for image generation as it doesn't use streaming.
// TODO: Implement if streaming support is added for image generation in the future.
func (o *openAIToOpenAIImageGenerationTranslator) extractUsageFromBufferEvent(span tracing.ImageGenerationSpan) LLMTokenUsage {
	// Image generation doesn't use streaming, so no token usage to extract
	return LLMTokenUsage{}
}
