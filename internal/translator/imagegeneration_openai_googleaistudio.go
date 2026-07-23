// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"time"

	"google.golang.org/genai"

	gcpschema "github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// NewImageGenerationOpenAIToGoogleAIStudioTranslator returns a translator for
// OpenAI /v1/images/generations → Google AI Studio generateContent.
//
// Google AI Studio image generation uses the generateContent endpoint with
// responseModalities: ["IMAGE", "TEXT"] and returns the image as inlineData
// (raw bytes) in candidates[0].content.parts[]. We base64-encode the bytes to
// produce the OpenAI b64_json field.
//
// https://ai.google.dev/api/generate-content
func NewImageGenerationOpenAIToGoogleAIStudioTranslator(schemaVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIImageGenerationTranslator {
	return &openAIToGoogleAIStudioImageGenerationTranslator{
		schemaVersion:     cmp.Or(schemaVersion, "v1beta"),
		modelNameOverride: modelNameOverride,
	}
}

// openAIToGoogleAIStudioImageGenerationTranslator implements [OpenAIImageGenerationTranslator]
// for /v1/images/generations against Google AI Studio.
type openAIToGoogleAIStudioImageGenerationTranslator struct {
	schemaVersion     string
	modelNameOverride internalapi.ModelNameOverride
	// requestModel stores the effective model for this request (override or provided)
	// so we can attribute metrics later and derive a response model.
	requestModel internalapi.RequestModel
}

// RequestBody implements [OpenAIImageGenerationTranslator.RequestBody]. It translates an OpenAI
// ImageGenerationRequest to a Gemini generateContent request and sets the path header to
// /{schemaVersion}/models/{model}:generateContent.
func (t *openAIToGoogleAIStudioImageGenerationTranslator) RequestBody(
	_ []byte, req *openai.ImageGenerationRequest, _ bool,
) (newHeaders []internalapi.Header, newBody []byte, err error) {
	t.requestModel = cmp.Or(t.modelNameOverride, req.Model)

	// Path: e.g. /v1beta/models/gemini-2.5-flash-image:generateContent.
	modelPath := fmt.Sprintf("/%s/models/%s:%s", t.schemaVersion, t.requestModel, gcpMethodGenerateContent)

	// Build the Gemini generateContent request. responseModalities=["IMAGE", "TEXT"] tells Gemini
	// to return inlineData image bytes.
	gcpReq := &gcpschema.GenerateContentRequest{
		Contents: []genai.Content{
			{
				Role:  genai.RoleUser,
				Parts: []*genai.Part{genai.NewPartFromText(req.Prompt)},
			},
		},
		GenerationConfig: &genai.GenerationConfig{
			ResponseModalities: []genai.Modality{genai.ModalityImage, genai.ModalityText},
		},
	}

	newBody, err = json.Marshal(gcpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal Google AI Studio image generation request: %w", err)
	}
	newHeaders = []internalapi.Header{
		{pathHeaderName, modelPath},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseHeaders implements [OpenAIImageGenerationTranslator.ResponseHeaders].
func (t *openAIToGoogleAIStudioImageGenerationTranslator) ResponseHeaders(map[string]string) ([]internalapi.Header, error) {
	return nil, nil
}

// ResponseBody implements [OpenAIImageGenerationTranslator.ResponseBody]. It translates a Gemini
// generateContent response to an OpenAI ImageGenerationResponse. Gemini returns images as inlineData
// parts with raw bytes, which we base64-encode to produce OpenAI b64_json.
func (t *openAIToGoogleAIStudioImageGenerationTranslator) ResponseBody(
	_ map[string]string, body io.Reader, _ bool, span tracingapi.ImageGenerationSpan,
) (newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error) {
	geminiResp := &genai.GenerateContentResponse{}
	if err = json.NewDecoder(body).Decode(geminiResp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to decode Google AI Studio response: %w", err)
	}

	responseModel = t.requestModel
	if geminiResp.ModelVersion != "" {
		responseModel = geminiResp.ModelVersion
	}

	// Extract image data from inlineData parts across all candidates. InlineData.Data is raw bytes
	// from the genai SDK, so base64-encode it for OpenAI b64_json.
	var imageData []openai.ImageGenerationResponseData
	for _, candidate := range geminiResp.Candidates {
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil && len(part.InlineData.Data) > 0 {
				imageData = append(imageData, openai.ImageGenerationResponseData{
					B64JSON: base64.StdEncoding.EncodeToString(part.InlineData.Data),
				})
			}
		}
	}

	if len(imageData) == 0 {
		return nil, nil, tokenUsage, responseModel,
			fmt.Errorf("google AI Studio returned no image data in response candidates")
	}

	openAIResp := &openai.ImageGenerationResponse{
		Created: time.Now().Unix(),
		Data:    imageData,
	}

	// Populate token usage if available.
	if geminiResp.UsageMetadata != nil {
		tokenUsage.SetInputTokens(uint32(geminiResp.UsageMetadata.PromptTokenCount))      //nolint:gosec
		tokenUsage.SetOutputTokens(uint32(geminiResp.UsageMetadata.CandidatesTokenCount)) //nolint:gosec
		tokenUsage.SetTotalTokens(uint32(geminiResp.UsageMetadata.TotalTokenCount))       //nolint:gosec
	}

	if span != nil {
		span.RecordResponse(openAIResp)
	}

	newBody, err = json.Marshal(openAIResp)
	if err != nil {
		return nil, nil, tokenUsage, responseModel,
			fmt.Errorf("failed to marshal OpenAI image generation response: %w", err)
	}
	newHeaders = []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return
}

// ResponseError implements [OpenAIImageGenerationTranslator.ResponseError]. It translates a non-2xx
// Gemini error response to the OpenAI error format.
func (t *openAIToGoogleAIStudioImageGenerationTranslator) ResponseError(
	respHeaders map[string]string, body io.Reader,
) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}
