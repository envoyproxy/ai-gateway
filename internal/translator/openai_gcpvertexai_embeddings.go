// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

const (
	gcpMethodEmbedContent = "embedContent"

	// Gemini embedding task types
	TaskTypeRetrievalQuery     = "RETRIEVAL_QUERY"
	TaskTypeRetrievalDocument  = "RETRIEVAL_DOCUMENT"
	TaskTypeSemanticSimilarity = "SEMANTIC_SIMILARITY"
	TaskTypeClassification     = "CLASSIFICATION"
	TaskTypeClustering         = "CLUSTERING"
)

// NewEmbeddingOpenAIToGCPVertexAITranslator implements [Factory] for OpenAI to GCP VertexAI translation
// for embeddings.
func NewEmbeddingOpenAIToGCPVertexAITranslator(requestModel internalapi.RequestModel, modelNameOverride internalapi.ModelNameOverride) OpenAIEmbeddingTranslator {
	return &openAIToGCPVertexAITranslatorV1Embedding{
		requestModel:      requestModel,
		modelNameOverride: modelNameOverride,
	}
}

// openAIToGCPVertexAITranslatorV1Embedding implements [OpenAIEmbeddingTranslator] for /embeddings.
type openAIToGCPVertexAITranslatorV1Embedding struct {
	requestModel      internalapi.RequestModel
	modelNameOverride internalapi.ModelNameOverride
}

// InputToGeminiContent converts OpenAI embedding input to a single Gemini Content.
// For multiple inputs, this function should be called multiple times.
func InputToGeminiContent(input openai.EmbeddingRequestInput) (*genai.Content, error) {
	switch v := input.Value.(type) {
	case string:
		return &genai.Content{
			Parts: []*genai.Part{
				{Text: v},
			},
		}, nil
	case []string:
		// For multiple strings, combine them into a single content with multiple parts
		var parts []*genai.Part
		for _, text := range v {
			parts = append(parts, &genai.Part{Text: text})
		}
		return &genai.Content{Parts: parts}, nil
	default:
		return nil, fmt.Errorf("unsupported input type for embedding: %T (supported: string, []string)", v)
	}
}

// determineTaskType determines the appropriate Gemini task type based on OpenAI request.
// Returns the explicit TaskType from GCPVertexAIEmbeddingVendorFields if provided, empty string otherwise.
func determineTaskType(req interface{}) string {
	switch r := req.(type) {
	case *openai.EmbeddingCompletionRequest:
		if r.GCPVertexAIEmbeddingVendorFields != nil && r.GCPVertexAIEmbeddingVendorFields.TaskType != "" {
			return r.GCPVertexAIEmbeddingVendorFields.TaskType
		}
	case *openai.EmbeddingChatRequest:
		if r.GCPVertexAIEmbeddingVendorFields != nil && r.GCPVertexAIEmbeddingVendorFields.TaskType != "" {
			return r.GCPVertexAIEmbeddingVendorFields.TaskType
		}
	}
	// No explicit task type provided, let GCP use their defaults
	return ""
}

// extractTitleFromContent attempts to extract a title from the content for RETRIEVAL_DOCUMENT tasks.
// This is a simple heuristic - in practice, you might want more sophisticated title extraction.
func extractTitleFromContent(content string) string {
	// Simple heuristic: use first line if it looks like a title (short and not ending with punctuation)
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) > 0 {
		firstLine := strings.TrimSpace(lines[0])
		// If first line is short and doesn't end with sentence punctuation, use as title
		if len(firstLine) < 100 && !strings.HasSuffix(firstLine, ".") &&
			!strings.HasSuffix(firstLine, "!") && !strings.HasSuffix(firstLine, "?") {
			return firstLine
		}
	}
	return ""
}

// getTextFromContent extracts all text content from a Gemini Content struct.
func getTextFromContent(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var textParts []string
	for _, part := range content.Parts {
		if part != nil && part.Text != "" {
			textParts = append(textParts, part.Text)
		}
	}
	return strings.Join(textParts, " ")
}

// openAIEmbeddingCompletionToGeminiMessage converts an OpenAI EmbeddingCompletionRequest to a GCP Gemini EmbedContentRequest.
func openAIEmbeddingCompletionToGeminiMessage(openAIReq *openai.EmbeddingCompletionRequest) (*gcp.EmbedContentRequest, error) {
	// Convert OpenAI EmbeddingRequest's input to Gemini Content
	geminiContent, err := InputToGeminiContent(openAIReq.Input)
	if err != nil {
		return nil, err
	}

	// Create the embedding configuration
	config := &genai.EmbedContentConfig{}

	// Set task type if explicitly provided
	if taskType := determineTaskType(openAIReq); taskType != "" {
		config.TaskType = taskType
	}

	// Set output dimensionality if specified
	if openAIReq.Dimensions != nil && *openAIReq.Dimensions > 0 {
		dim := int32(*openAIReq.Dimensions)
		config.OutputDimensionality = &dim
	}

	// For RETRIEVAL_DOCUMENT tasks, try to extract a title
	if config.TaskType == TaskTypeRetrievalDocument {
		if content := getTextFromContent(geminiContent); content != "" {
			if title := extractTitleFromContent(content); title != "" {
				config.Title = title
			}
		}
	}

	// Build the request using genai.EmbedContentConfig
	gcr := &gcp.EmbedContentRequest{
		Content: geminiContent,
		Config:  config,
	}

	return gcr, nil
}

// TODO: Based on https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/text-embeddings-api#request_body, vertex ai does not even support query-aware embedding. Double check it.
// openAIEmbeddingChatToGeminiMessage converts an OpenAI EmbeddingChatRequest to a GCP Gemini EmbedContentRequest.
func openAIEmbeddingChatToGeminiMessage(openAIReq *openai.EmbeddingChatRequest) (*gcp.EmbedContentRequest, error) {
	// Convert OpenAI chat messages to text content for embedding
	var textContent strings.Builder

	for i, msg := range openAIReq.Messages {
		if i > 0 {
			textContent.WriteString("\n")
		}

		// Extract text content from the message union
		if msg.OfUser != nil {
			// Handle user message
			switch content := msg.OfUser.Content.Value.(type) {
			case string:
				textContent.WriteString(content)
			// For more complex content types, we'd need to handle them here
			default:
				if content != nil {
					return nil, fmt.Errorf("unsupported user content type: %T", content)
				}
			}
		} else if msg.OfAssistant != nil {
			// Handle assistant message
			switch content := msg.OfAssistant.Content.Value.(type) {
			case string:
				textContent.WriteString(content)
			default:
				if content != nil {
					return nil, fmt.Errorf("unsupported assistant content type: %T", content)
				}
			}
		} else if msg.OfSystem != nil {
			// Handle system message
			switch content := msg.OfSystem.Content.Value.(type) {
			case string:
				textContent.WriteString(content)
			default:
				return nil, fmt.Errorf("unsupported system content type: %T", content)
			}
		} else if msg.OfTool != nil {
			// Handle tool message
			switch content := msg.OfTool.Content.Value.(type) {
			case string:
				textContent.WriteString(content)
			default:
				return nil, fmt.Errorf("unsupported tool content type: %T", content)
			}
		} else if msg.OfDeveloper != nil {
			// Handle developer message
			switch content := msg.OfDeveloper.Content.Value.(type) {
			case string:
				textContent.WriteString(content)
			default:
				return nil, fmt.Errorf("unsupported developer content type: %T", content)
			}
		} else {
			return nil, fmt.Errorf("unsupported message type in union")
		}
	}

	// Convert the combined text to genai.Content format
	geminiContent := &genai.Content{
		Parts: []*genai.Part{
			{Text: textContent.String()},
		},
	}

	// Create the embedding configuration
	config := &genai.EmbedContentConfig{}

	// Set task type if explicitly provided
	if taskType := determineTaskType(openAIReq); taskType != "" {
		config.TaskType = taskType
	}

	// Set output dimensionality if specified
	if openAIReq.Dimensions != nil && *openAIReq.Dimensions > 0 {
		dim := int32(*openAIReq.Dimensions)
		config.OutputDimensionality = &dim
	}

	// For RETRIEVAL_DOCUMENT tasks, try to extract a title
	if config.TaskType == TaskTypeRetrievalDocument {
		if title := extractTitleFromContent(textContent.String()); title != "" {
			config.Title = title
		}
	}

	// Build the request using genai.EmbedContentConfig
	gcr := &gcp.EmbedContentRequest{
		Content: geminiContent,
		Config:  config,
	}

	return gcr, nil
}

// RequestBody implements [OpenAIEmbeddingTranslator.RequestBody].
func (o *openAIToGCPVertexAITranslatorV1Embedding) RequestBody(original []byte, req *openai.EmbeddingRequest, onRetry bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {

	o.requestModel = openai.GetModelFromEmbeddingRequest(req)
	if o.modelNameOverride != "" {
		// Use modelName override if set.
		o.requestModel = o.modelNameOverride
	}

	// Choose the correct endpoint based on streaming.
	var path string

	// Use the embedding-specific endpoint
	path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, o.requestModel, gcpMethodEmbedContent)

	var gcpReq *gcp.EmbedContentRequest

	if req.OfCompletion != nil {
		gcpReq, err = openAIEmbeddingCompletionToGeminiMessage(req.OfCompletion)
		if err != nil {
			return nil, nil, fmt.Errorf("error converting EmbeddingCompletionRequest: %w", err)
		}
	} else if req.OfChat != nil {
		// For chat requests, directly use the chat-specific converter
		gcpReq, err = openAIEmbeddingChatToGeminiMessage(req.OfChat)
		if err != nil {
			return nil, nil, fmt.Errorf("error converting EmbeddingChatRequest: %w", err)
		}
	} else {
		return nil, nil, fmt.Errorf("invalid EmbeddingRequest: neither completion nor chat request")
	}

	newBody, err = json.Marshal(gcpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling Gemini request: %w", err)
	}
	newHeaders = []internalapi.Header{
		{pathHeaderName, path},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseHeaders implements [OpenAIEmbeddingTranslator.ResponseHeaders].
func (o *openAIToGCPVertexAITranslatorV1Embedding) ResponseHeaders(headers map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAIEmbeddingTranslator.ResponseBody].
func (o *openAIToGCPVertexAITranslatorV1Embedding) ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool, span tracing.EmbeddingsSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	// Read the GCP VertexAI response
	respBody, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Try to parse as genai.EmbedContentResponse first (for batch responses)
	var gcpResp genai.EmbedContentResponse
	if err := json.Unmarshal(respBody, &gcpResp); err != nil {
		// If that fails, try parsing as SingleEmbedContentResponse
		var singleResp genai.SingleEmbedContentResponse
		if err := json.Unmarshal(respBody, &singleResp); err != nil {
			return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal GCP response: %w", err)
		}
		// Convert SingleEmbedContentResponse to EmbedContentResponse format
		gcpResp.Embeddings = []*genai.ContentEmbedding{singleResp.Embedding}
		if singleResp.TokenCount > 0 {
			tokenUsage.SetInputTokens(uint32(singleResp.TokenCount))
		}
	}

	// Convert GCP response to OpenAI format
	openaiResp := openai.EmbeddingResponse{
		Object: "list",
		Model:  string(o.requestModel),
		Usage: openai.EmbeddingUsage{
			PromptTokens: 0, // Will be set from token usage
			TotalTokens:  0, // Will be set from token usage
		},
	}

	// Convert embedding vectors
	if len(gcpResp.Embeddings) > 0 {
		openaiResp.Data = make([]openai.Embedding, len(gcpResp.Embeddings))
		for i, embedding := range gcpResp.Embeddings {
			if embedding != nil {
				// Convert float32 slice to float64 slice for OpenAI format
				float64Values := make([]float64, len(embedding.Values))
				for j, v := range embedding.Values {
					float64Values[j] = float64(v)
				}

				openaiResp.Data[i] = openai.Embedding{
					Object:    "embedding",
					Index:     i,
					Embedding: openai.EmbeddingUnion{Value: float64Values},
				}

				// Extract token count from statistics if available
				if embedding.Statistics != nil {
					// Note: ContentEmbeddingStatistics might contain token count info
					// This would need to be implemented based on what's available in the statistics
				}
			}
		}
	} else {
		openaiResp.Data = []openai.Embedding{}
	}

	// Record the response in the span if successful
	if span != nil {
		span.RecordResponse(&openaiResp)
	}

	// Set token usage from accumulated values
	if inputTokens, ok := tokenUsage.InputTokens(); ok && inputTokens > 0 {
		openaiResp.Usage.PromptTokens = int(inputTokens)
		openaiResp.Usage.TotalTokens = int(inputTokens)
	}

	responseModel = internalapi.ResponseModel(openaiResp.Model)

	// Marshal the OpenAI response
	newBody, err = json.Marshal(openaiResp)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to marshal OpenAI response: %w", err)
	}

	return
}

// ResponseError implements [OpenAIEmbeddingTranslator.ResponseError].
func (o *openAIToGCPVertexAITranslatorV1Embedding) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	return convertGCPVertexAIErrorToOpenAI(respHeaders, body)
}
