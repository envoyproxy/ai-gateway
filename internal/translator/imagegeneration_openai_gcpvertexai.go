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
	if o.requestModel == "" {
		err = fmt.Errorf("%w: model field is required", internalapi.ErrInvalidRequestBody)
		return
	}
	o.isImagenModel = strings.HasPrefix(o.requestModel, "imagen")
	var path string
	if o.isImagenModel {
		var imgGenReq *gcp.ImagePredictRequest
		imgGenReq, err = openAIToImagenRequest(openAIReq)
		if err != nil {
			return
		}
		newBody, err = json.Marshal(imgGenReq)
		path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, string(o.requestModel), gcpMethodPredict)
	} else {
		var geminiReq *gcp.GenerateContentRequest
		geminiReq, err = openAIToGeminiRequest(openAIReq)
		if err != nil {
			return
		}
		newBody, err = json.Marshal(geminiReq)
		path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, string(o.requestModel), gcpMethodGenerateContent)
	}
	if err != nil {
		err = fmt.Errorf("failed to encode request: %w", err)
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
// It maps supported OpenAI sizes to explicit Imagen aspect ratio + sample image size.
// See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/imagen-api
func openAIToImagenRequest(req *openai.ImageGenerationRequest) (*gcp.ImagePredictRequest, error) {
	if req.Quality != "" {
		return nil, fmt.Errorf("%w: quality parameter is not supported for Vertex AI Imagen models",
			internalapi.ErrInvalidRequestBody)
	}

	outputOptionsMIMEType, err := outputFormatToMIMEType(req.OutputFormat)
	if err != nil {
		return nil, err
	}
	var outputOptions *gcp.ImageOutputOptions
	if outputOptionsMIMEType != "" || req.OutputCompression != nil {
		outputOptions = &gcp.ImageOutputOptions{
			MIMEType:           outputOptionsMIMEType,
			CompressionQuality: req.OutputCompression,
		}
	}

	aspectRatio, sampleImageSize, err := sizeToAspectRatioAndSampleImageSize(req.Size)
	if err != nil {
		return nil, err
	}

	return &gcp.ImagePredictRequest{
		Instances: []*gcp.ImageInstance{
			{Prompt: req.Prompt},
		},
		Parameters: &gcp.ImageParameters{
			SampleCount:     int(cmp.Or(req.N, 1)),
			AspectRatio:     aspectRatio,
			SampleImageSize: sampleImageSize,
			OutputOptions:   outputOptions,
		},
	}, nil
}

// openAIToGeminiRequest converts an OpenAI image generation request to a GCP Gemini generateContent request.
// See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference
func openAIToGeminiRequest(req *openai.ImageGenerationRequest) (*gcp.GenerateContentRequest, error) {
	// Note: this translator builds Vertex REST JSON directly, instead of calling
	// genai.Models.GenerateContent(..., *genai.GenerateContentConfig). The request
	// schema currently uses generation_config backed by genai.GenerationConfig,
	// which does not carry ImageConfig. Therefore size/quality cannot be
	// forwarded for Gemini image models in this path.
	// See: https://pkg.go.dev/google.golang.org/genai#GenerateContentConfig
	if req.Size != "" {
		return nil, fmt.Errorf("%w: size parameter is not supported for Gemini image models",
			internalapi.ErrInvalidRequestBody)
	}
	if req.Quality != "" {
		return nil, fmt.Errorf("%w: quality parameter is not supported for Gemini image models",
			internalapi.ErrInvalidRequestBody)
	}

	return &gcp.GenerateContentRequest{
		Contents: []genai.Content{
			{
				Parts: []*genai.Part{
					{Text: req.Prompt},
				},
			},
		},
		GenerationConfig: &genai.GenerationConfig{
			CandidateCount: int32(cmp.Or(req.N, 1)),
		},
	}, nil
}

// imagenToOpenAIResponse converts a GCP Imagen prediction response to an OpenAI image generation response.
// Predictions with an empty BytesBase64Encoded field are skipped, as they indicate images filtered
// by Responsible AI safety policies.
// See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/image/responsible-ai-imagen
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
		inputTokens := resp.UsageMetadata.PromptTokenCount + resp.UsageMetadata.ToolUsePromptTokenCount
		outputTokens := resp.UsageMetadata.CandidatesTokenCount + resp.UsageMetadata.ThoughtsTokenCount
		totalTokens := resp.UsageMetadata.TotalTokenCount

		usage = &openai.ImageGenerationUsage{
			TotalTokens:  int(totalTokens),
			InputTokens:  int(inputTokens),
			OutputTokens: int(outputTokens),
		}
		tokenUsage.SetInputTokens(uint32(inputTokens))
		tokenUsage.SetOutputTokens(uint32(outputTokens))
		tokenUsage.SetTotalTokens(uint32(totalTokens))
	}

	return &openai.ImageGenerationResponse{
		Created:      created,
		Data:         images,
		OutputFormat: outputFormat,
		Usage:        usage,
	}
}

// sizeToAspectRatioAndSampleImageSize maps OpenAI size to explicit Imagen output settings.
// Empty/auto size is preserved as backend default by not sending explicit aspect ratio/image size.
// For explicit OpenAI size, only the cross-provider compatible size 1024x1024 is supported.
// See: https://developers.openai.com/api/reference/resources/images/methods/generate
// And: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/imagen/4-0-generate
func sizeToAspectRatioAndSampleImageSize(size string) (aspectRatio string, sampleImageSize string, err error) {
	switch size {
	case "", "auto":
		return "", "", nil
	case "1024x1024":
		return "1:1", "1K", nil
	}
	return "", "", fmt.Errorf("%w: size %q is not supported by Vertex AI Imagen (supported: 1024x1024)", internalapi.ErrInvalidRequestBody, size)
}

// outputFormatToMIMEType maps an OpenAI output format string to a MIME type.
// Vertex AI Imagen does not support webp output format, so only png and jpeg are mapped.
// See: https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/imagen-api
func outputFormatToMIMEType(outputFormat string) (string, error) {
	// webp is not supported by Vertex AI Imagen.
	switch outputFormat {
	case "png", "jpeg":
		return "image/" + outputFormat, nil
	case "":
		return "", nil
	}
	return "", fmt.Errorf("%w: output format %q is not supported by Vertex AI Imagen (supported: png, jpeg)", internalapi.ErrInvalidRequestBody, outputFormat)
}
