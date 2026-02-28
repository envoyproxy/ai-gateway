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
	"strings"
	"time"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
	"google.golang.org/genai"
)

// NewImageGenerationOpenAIToGCPVertexAITranslator implements [Factory] for OpenAI to GCP Vertex AI
// translation for image generation.
func NewImageGenerationOpenAIToGCPVertexAITranslator(modelNameOverride internalapi.ModelNameOverride) OpenAIImageGenerationTranslator {
	return &openAIToGCPVertexAIImageGenerationTranslator{modelNameOverride: modelNameOverride}
}

// openAIToGCPVertexAIImageGenerationTranslator translates OpenAI Image Generation API requests to
// GCP Vertex AI image generation APIs. It supports two backends:
//   - Imagen models: uses the predict endpoint.
//     See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/imagen-api
//   - Gemini image models: uses the generateContent endpoint.
//     See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference
type openAIToGCPVertexAIImageGenerationTranslator struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	// imagen models and gemini-image models are using different endpoints
	isImagenModel bool
}

func (o *openAIToGCPVertexAIImageGenerationTranslator) RequestBody(original []byte, openAIReq *openai.ImageGenerationRequest, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	o.requestModel = cmp.Or(o.modelNameOverride, openAIReq.Model)
	o.isImagenModel = strings.HasPrefix(o.requestModel, "imagen")
	var path string
	if o.isImagenModel {
		newBody, err = json.Marshal(openAIToImagenRequest(openAIReq))
		path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, string(o.requestModel), gcpMethodPredict)
	} else {
		newBody, err = json.Marshal(openAIToGeminiRequest(openAIReq))
		path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, string(o.requestModel), gcpMethodGenerateContent)
	}
	if err != nil {
		return
	}
	newHeaders = []internalapi.Header{
		{pathHeaderName, path},
		{contentLengthHeaderName, fmt.Sprintf("%d", len(newBody))},
	}
	return
}

func (o *openAIToGCPVertexAIImageGenerationTranslator) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertGCPVertexAIErrorToOpenAI(respHeaders, body)
}

func (o *openAIToGCPVertexAIImageGenerationTranslator) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

func (o *openAIToGCPVertexAIImageGenerationTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracingapi.ImageGenerationSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	var openAIResp *openai.ImageGenerationResponse
	if o.isImagenModel {
		var resp gcp.ImagePredictionResponse
		if err = json.NewDecoder(body).Decode(&resp); err != nil {
			return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to decode response: %w", err)
		}
		openAIResp = imagenToOpenAIResponse(&resp)
	} else {
		var resp genai.GenerateContentResponse
		if err = json.NewDecoder(body).Decode(&resp); err != nil {
			return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to decode response: %w", err)
		}
		openAIResp = geminiToOpenAIResponse(&resp, &tokenUsage)
	}
	if span != nil {
		span.RecordResponse(openAIResp)
	}
	if newBody, err = json.Marshal(openAIResp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to encode response: %w", err)
	}
	responseModel = o.requestModel
	newHeaders = []internalapi.Header{
		{contentLengthHeaderName, fmt.Sprintf("%d", len(newBody))},
	}
	return
}

// openAIToImagenRequest converts an OpenAI image generation request to a GCP Imagen predict request.
// It maps the size parameter to an aspect ratio and the quality parameter to an output image size.
// See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/imagen-api
func openAIToImagenRequest(req *openai.ImageGenerationRequest) *gcp.ImagePredictRequest {
	outputOptionsMIMEType := outputFormatToMIMEType(req.OutputFormat)
	var compressionQuality int
	if req.OutputCompression != nil {
		compressionQuality = *req.OutputCompression
	}
	var outputOptions *gcp.ImageOutputOptions
	if outputOptionsMIMEType != "" || compressionQuality != 0 {
		outputOptions = &gcp.ImageOutputOptions{
			MIMEType:           outputOptionsMIMEType,
			CompressionQuality: compressionQuality,
		}
	}

	return &gcp.ImagePredictRequest{
		Instances: []*gcp.ImageInstance{
			{Prompt: req.Prompt},
		},
		Parameters: gcp.ImageParameters{
			SampleCount:     int(cmp.Or(req.N, 1)),
			AspectRatio:     sizeToAspectRatio(req.Size),
			SampleImageSize: qualityToImageSize(req.Quality),
			OutputOptions:   outputOptions,
		},
	}
}

// openAIToGeminiRequest converts an OpenAI image generation request to a GCP Gemini generateContent request.
// See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference
func openAIToGeminiRequest(req *openai.ImageGenerationRequest) *gcp.GenerateContentRequest {
	return &gcp.GenerateContentRequest{
		Contents: []genai.Content{
			{
				Parts: []*genai.Part{
					{Text: req.Prompt},
				},
			},
		},
		// Note: ImageConfig (aspect ratio, image size) is only available in
		// genai.GenerateContentConfig (SDK wrapper), not genai.GenerationConfig which is used
		// by GenerateContentRequest. Size/quality parameters are not forwarded for Gemini models.
		// See: https://pkg.go.dev/google.golang.org/genai#GenerateContentConfig
		GenerationConfig: &genai.GenerationConfig{
			CandidateCount: int32(cmp.Or(req.N, 1)),
		},
	}
}

// imagenToOpenAIResponse converts a GCP Imagen prediction response to an OpenAI image generation response.
// Predictions with an empty BytesBase64Encoded field are skipped, as they indicate images filtered
// by Responsible AI safety policies.
func imagenToOpenAIResponse(resp *gcp.ImagePredictionResponse) *openai.ImageGenerationResponse {
	var images []openai.ImageGenerationResponseData
	outputFormat := ""

	for _, prediction := range resp.Predictions {
		if prediction.BytesBase64Encoded == "" {
			continue
		}
		images = append(images, openai.ImageGenerationResponseData{
			B64JSON:       prediction.BytesBase64Encoded,
			RevisedPrompt: prediction.Prompt,
		})
		if outputFormat == "" && strings.HasPrefix(prediction.MIMEType, "image/") {
			outputFormat = prediction.MIMEType[6:]
		}
	}
	return &openai.ImageGenerationResponse{
		Created:      time.Now().Unix(),
		Data:         images,
		OutputFormat: outputFormat,
	}
}

// geminiToOpenAIResponse converts a GCP Gemini generateContent response to an OpenAI image generation response.
// Only parts with InlineData (image bytes) are included; text parts are ignored.
func geminiToOpenAIResponse(resp *genai.GenerateContentResponse, tokenUsage *metrics.TokenUsage) *openai.ImageGenerationResponse {
	created := resp.CreateTime.Unix()
	if resp.CreateTime.IsZero() {
		created = time.Now().Unix()
	}

	var images []openai.ImageGenerationResponseData
	outputFormat := ""

	for _, candidate := range resp.Candidates {
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil {
				continue
			}
			images = append(images, openai.ImageGenerationResponseData{
				B64JSON: base64.StdEncoding.EncodeToString(part.InlineData.Data),
			})
			if outputFormat == "" && strings.HasPrefix(part.InlineData.MIMEType, "image/") {
				outputFormat = part.InlineData.MIMEType[6:]
			}
		}
	}

	var usage *openai.ImageGenerationUsage
	if resp.UsageMetadata != nil {
		usage = &openai.ImageGenerationUsage{
			TotalTokens:  int(resp.UsageMetadata.TotalTokenCount),
			InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
		}
		tokenUsage.AddInputTokens(uint32(resp.UsageMetadata.PromptTokenCount))
		tokenUsage.AddOutputTokens(uint32(resp.UsageMetadata.CandidatesTokenCount))
	}

	return &openai.ImageGenerationResponse{
		Created:      created,
		Data:         images,
		OutputFormat: outputFormat,
		Usage:        usage,
	}
}

// sizeToAspectRatio maps OpenAI image sizes to Imagen aspect ratios.
// Supported aspect ratios: 1:1, 3:4, 4:3, 9:16, 16:9.
// See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/imagen/4-0-generate
func sizeToAspectRatio(size string) string {
	switch size {
	case "1792x1024":
		return "16:9"
	case "1024x1792":
		return "9:16"
	case "1536x1024":
		return "4:3"
	case "1024x1536":
		return "3:4"
	default: // "1024x1024", "512x512", "256x256", ""
		return "1:1"
	}
}

// outputFormatToMIMEType maps an OpenAI output format string to a MIME type.
// Vertex AI Imagen does not support webp output format, so only png and jpeg are mapped.
// See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/imagen-api
func outputFormatToMIMEType(outputFormat string) string {
	// webp is not supported by Vertex AI Imagen.
	if outputFormat == "png" || outputFormat == "jpeg" {
		return "image/" + outputFormat
	}
	return ""
}

// qualityToImageSize maps OpenAI quality values to Imagen image size strings.
// gpt-image-1 uses low/medium/high; DALL-E models use standard/hd.
// See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/imagen-api
func qualityToImageSize(quality string) string {
	switch quality {
	case "low":
		return "1K"
	case "medium":
		return "2K"
	case "high":
		return "4K"
	case "standard":
		return "1K"
	case "hd":
		return "2K"
	default:
		return ""
	}
}
