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
	"strings"
	"time"

	"google.golang.org/genai"

	gcpschema "github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// maxImageGenerationCandidates bounds the OpenAI n parameter, which is forwarded as Gemini's
// candidateCount. OpenAI documents n as 1-10; the bound also keeps the int32 conversion safe.
const maxImageGenerationCandidates = 10

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
//
// Of the OpenAI parameters, prompt, model and n (as candidateCount) have generateContent
// equivalents and are forwarded. The DALL-E/gpt-image-1 rendering hints — size, quality,
// style, background, output_format, output_compression and moderation — have no equivalent
// in the generateContent generationConfig and are not forwarded. response_format and stream
// are rejected when set to something this path cannot honor, rather than silently ignored.
func (t *openAIToGoogleAIStudioImageGenerationTranslator) RequestBody(
	_ []byte, req *openai.ImageGenerationRequest, _ bool,
) (newHeaders []internalapi.Header, newBody []byte, err error) {
	t.requestModel = cmp.Or(t.modelNameOverride, req.Model)

	// Gemini returns the image inline as bytes, so the only response format we can
	// honor is b64_json. Reject an explicit "url" instead of silently returning
	// something the client did not ask for.
	if req.ResponseFormat != "" && req.ResponseFormat != "b64_json" {
		return nil, nil, fmt.Errorf("%w: unsupported response_format: %q (Google AI Studio only returns inline image bytes, supported: b64_json)",
			internalapi.ErrInvalidRequestBody, req.ResponseFormat)
	}
	// generateContent is not streamed here, so honoring stream would mean returning a
	// single response to a client waiting for events. Reject it for the same reason.
	if req.Stream {
		return nil, nil, fmt.Errorf("%w: stream is not supported for Google AI Studio image generation",
			internalapi.ErrInvalidRequestBody)
	}
	if req.N < 0 || req.N > maxImageGenerationCandidates {
		return nil, nil, fmt.Errorf("%w: n must be between 1 and %d, got %d",
			internalapi.ErrInvalidRequestBody, maxImageGenerationCandidates, req.N)
	}

	// Path: e.g. /v1beta/models/gemini-2.5-flash-image:generateContent.
	modelPath := fmt.Sprintf("/%s/models/%s:%s", t.schemaVersion, t.requestModel, gcpMethodGenerateContent)

	// Build the Gemini generateContent request. responseModalities=["IMAGE", "TEXT"] tells Gemini
	// to return inlineData image bytes.
	generationConfig := &genai.GenerationConfig{
		ResponseModalities: []genai.Modality{genai.ModalityImage, genai.ModalityText},
	}
	// OpenAI's n maps to Gemini's candidateCount; leave it unset so the model default (1) applies.
	if req.N > 0 {
		generationConfig.CandidateCount = int32(req.N) //nolint:gosec // bounded by maxImageGenerationCandidates above.
	}
	gcpReq := &gcpschema.GenerateContentRequest{
		Contents: []genai.Content{
			{
				Role:  genai.RoleUser,
				Parts: []*genai.Part{genai.NewPartFromText(req.Prompt)},
			},
		},
		GenerationConfig: generationConfig,
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
	var finishReasons []string
	for _, candidate := range geminiResp.Candidates {
		if candidate.FinishReason != "" && candidate.FinishReason != genai.FinishReasonStop {
			finishReasons = append(finishReasons, string(candidate.FinishReason))
		}
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

	// A blocked prompt comes back 200 with no inlineData, so surface whatever reason Gemini gave
	// rather than reporting a bare "no image data".
	if len(imageData) == 0 {
		reason := "no image data in response candidates"
		if feedback := geminiResp.PromptFeedback; feedback != nil && feedback.BlockReason != "" {
			reason = fmt.Sprintf("prompt blocked: %s", feedback.BlockReason)
			if feedback.BlockReasonMessage != "" {
				reason = fmt.Sprintf("%s (%s)", reason, feedback.BlockReasonMessage)
			}
		} else if len(finishReasons) > 0 {
			reason = fmt.Sprintf("no image data, candidate finish reasons: %s", strings.Join(finishReasons, ", "))
		}
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("google AI Studio returned %s", reason)
	}

	openAIResp := &openai.ImageGenerationResponse{
		Created: time.Now().Unix(),
		Data:    imageData,
	}

	// Populate token usage if available, both for the gateway metrics and for the
	// usage field OpenAI clients read off the response.
	if usage := geminiResp.UsageMetadata; usage != nil {
		tokenUsage.SetInputTokens(uint32(usage.PromptTokenCount))      //nolint:gosec
		tokenUsage.SetOutputTokens(uint32(usage.CandidatesTokenCount)) //nolint:gosec
		tokenUsage.SetTotalTokens(uint32(usage.TotalTokenCount))       //nolint:gosec
		openAIResp.Usage = &openai.ImageGenerationUsage{
			InputTokens:  int(usage.PromptTokenCount),
			OutputTokens: int(usage.CandidatesTokenCount),
			TotalTokens:  int(usage.TotalTokenCount),
		}
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
