// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"

	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// NewImageGenerationOpenAIToGCPVertexAITranslator creates a translator for OpenAI to GCP VertexAI image generation.
func NewImageGenerationOpenAIToGCPVertexAITranslator(modelNameOverride internalapi.ModelNameOverride) OpenAIImageGenerationTranslator {
	return &openAIToGCPVertexAIImageGenerationTranslator{
		modelNameOverride: modelNameOverride,
	}
}

// openAIToGCPVertexAIImageGenerationTranslator implements [OpenAIImageGenerationTranslator] for GCP VertexAI.
type openAIToGCPVertexAIImageGenerationTranslator struct {
	modelNameOverride internalapi.ModelNameOverride
	// requestModel stores the effective model for this request (override or provided)
	// so we can attribute metrics later; the Gemini Images response may omit a model field.
	requestModel internalapi.RequestModel
}

// RequestBody implements [OpenAIImageGenerationTranslator.RequestBody].
// Converts OpenAI ImageGenerationRequest to GCP VertexAI GenerateContentRequest.
func (o *openAIToGCPVertexAIImageGenerationTranslator) RequestBody(_ []byte, openAIReq *openai.ImageGenerationRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	o.requestModel = openAIReq.Model
	if o.modelNameOverride != "" {
		o.requestModel = o.modelNameOverride
	}

	// Convert OpenAI request to Gemini GenerateContentRequest
	geminiReq, err := o.openAIToGeminiImageRequest(openAIReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert OpenAI request to Gemini: %w", err)
	}

	// Marshal the Gemini request
	newBody, err = json.Marshal(geminiReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal Gemini request: %w", err)
	}

	// Build the path based on the model type
	path := buildGCPModelPathSuffix(gcpModelPublisherGoogle, o.requestModel, gcpMethodGenerateContent)

	newHeaders = []internalapi.Header{
		{pathHeaderName, path},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}

	return newHeaders, newBody, nil
}

// openAIToGeminiImageRequest converts an OpenAI ImageGenerationRequest to a Gemini GenerateContentRequest.
func (o *openAIToGCPVertexAIImageGenerationTranslator) openAIToGeminiImageRequest(req *openai.ImageGenerationRequest) (*gcp.GenerateContentRequest, error) {
	// Create the prompt as a text part
	content := genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			genai.NewPartFromText(req.Prompt),
		},
	}

	// Build generation config with image generation parameters
	generationConfig := &genai.GenerationConfig{}

	// Map the number of images to generate
	if req.N > 0 {
		generationConfig.CandidateCount = int32(req.N) //nolint:gosec
	}

	// Map quality parameter if provided
	// Note: Gemini uses different quality settings than OpenAI
	if req.Quality != "" {
		// This is a simplified mapping - actual Gemini API may use different parameters
		// for quality control depending on the model
		generationConfig.ResponseMIMEType = "image/png"
	}

	// Map size parameter if provided
	// Note: Gemini handles size differently than OpenAI
	// Size handling would be model-specific in Gemini and is not directly supported
	// in the current GenerationConfig. Future implementations may add vendor-specific
	// fields to handle size constraints.

	geminiReq := &gcp.GenerateContentRequest{
		Contents:         []genai.Content{content},
		GenerationConfig: generationConfig,
	}

	return geminiReq, nil
}

// ResponseHeaders implements [OpenAIImageGenerationTranslator.ResponseHeaders].
func (o *openAIToGCPVertexAIImageGenerationTranslator) ResponseHeaders(_ map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	// No special headers needed for non-streaming image generation
	return nil, nil
}

// ResponseBody implements [OpenAIImageGenerationTranslator.ResponseBody].
// Converts Gemini GenerateContentResponse to OpenAI ImageGenerationResponse.
func (o *openAIToGCPVertexAIImageGenerationTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracingapi.ImageGenerationSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	// Decode the Gemini response
	geminiResp := &genai.GenerateContentResponse{}
	if decodeErr := json.NewDecoder(body).Decode(geminiResp); decodeErr != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to decode Gemini response: %w", decodeErr)
	}

	// Convert to OpenAI format
	openAIResp, err := o.geminiToOpenAIImageResponse(geminiResp)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to convert Gemini response to OpenAI: %w", err)
	}

	// Marshal the OpenAI response
	newBody, err = json.Marshal(openAIResp)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to marshal OpenAI response: %w", err)
	}

	// Extract token usage if available
	if geminiResp.UsageMetadata != nil {
		tokenUsage.SetInputTokens(uint32(geminiResp.UsageMetadata.PromptTokenCount))      //nolint:gosec
		tokenUsage.SetOutputTokens(uint32(geminiResp.UsageMetadata.CandidatesTokenCount)) //nolint:gosec
		tokenUsage.SetTotalTokens(uint32(geminiResp.UsageMetadata.TotalTokenCount))       //nolint:gosec
	}

	// Use request model as response model (Gemini doesn't always return model version for images)
	responseModel = o.requestModel
	if geminiResp.ModelVersion != "" {
		responseModel = geminiResp.ModelVersion
	}

	// Record the response in the span if tracing is enabled
	if span != nil {
		span.RecordResponse(openAIResp)
	}

	newHeaders = []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return newHeaders, newBody, tokenUsage, responseModel, nil
}

// geminiToOpenAIImageResponse converts a Gemini GenerateContentResponse to OpenAI ImageGenerationResponse.
func (o *openAIToGCPVertexAIImageGenerationTranslator) geminiToOpenAIImageResponse(geminiResp *genai.GenerateContentResponse) (*openai.ImageGenerationResponse, error) {
	var imageData []openai.ImageGenerationResponseData

	// Extract images from candidates
	for _, candidate := range geminiResp.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}

		for _, part := range candidate.Content.Parts {
			if part == nil {
				continue
			}

			// Check if this part contains inline data (image bytes)
			if part.InlineData != nil && len(part.InlineData.Data) > 0 {
				// Encode image data as base64
				b64Data := base64.StdEncoding.EncodeToString(part.InlineData.Data)
				imageData = append(imageData, openai.ImageGenerationResponseData{
					B64JSON: b64Data,
				})
			} else if part.FileData != nil && part.FileData.FileURI != "" {
				// If image is provided as a URI
				imageData = append(imageData, openai.ImageGenerationResponseData{
					URL: part.FileData.FileURI,
				})
			}
		}
	}

	if len(imageData) == 0 {
		return nil, fmt.Errorf("no image data found in Gemini response")
	}

	// Build OpenAI response
	openAIResp := &openai.ImageGenerationResponse{
		Created: geminiResp.CreateTime.Unix(),
		Data:    imageData,
	}

	// Add usage information if available
	if geminiResp.UsageMetadata != nil {
		openAIResp.Usage = &openai.ImageGenerationUsage{
			TotalTokens:  int(geminiResp.UsageMetadata.TotalTokenCount),
			InputTokens:  int(geminiResp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(geminiResp.UsageMetadata.CandidatesTokenCount),
		}
	}

	return openAIResp, nil
}

// ResponseError implements [OpenAIImageGenerationTranslator.ResponseError].
// Converts GCP VertexAI errors to OpenAI error format.
func (o *openAIToGCPVertexAIImageGenerationTranslator) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	var buf []byte
	buf, err = io.ReadAll(body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read error body: %w", err)
	}

	// Assume all responses have a valid status code header
	statusCode := respHeaders[statusHeaderName]

	openaiError := openai.Error{
		Type: "error",
		Error: openai.ErrorType{
			Type: gcpVertexAIBackendError,
			Code: &statusCode,
		},
	}

	var gcpError gcpVertexAIError
	// Try to parse as GCP error response structure
	if err = json.Unmarshal(buf, &gcpError); err == nil {
		errMsg := gcpError.Error.Message
		if len(gcpError.Error.Details) > 0 {
			// If details are present and not null, append them to the error message
			errMsg = fmt.Sprintf("Error: %s\nDetails: %s", errMsg, string(gcpError.Error.Details))
		}
		openaiError.Error.Type = gcpError.Error.Status
		openaiError.Error.Message = errMsg
	} else {
		// If not JSON, read the raw body as the error message
		openaiError.Error.Message = string(buf)
	}

	newBody, err = json.Marshal(openaiError)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
	}

	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}

	return newHeaders, newBody, nil
}

// isImagenModel checks if the given model is an Imagen model.
func isImagenModel(model string) bool {
	return strings.HasPrefix(model, "imagen-")
}
