// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"io"
	"strconv"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

const (
	gcpMethodPredict = "predict"
)

// NewEmbeddingOpenAIToGCPVertexAITranslator implements [Factory] for OpenAI to GCP VertexAI translation
// for embeddings.
func NewEmbeddingOpenAIToGCPVertexAITranslator(requestModel internalapi.RequestModel, modelNameOverride internalapi.ModelNameOverride) OpenAIEmbeddingTranslator {
	return &openAIToGCPVertexAITranslatorV1Embedding{
		requestModel:      requestModel,
		modelNameOverride: modelNameOverride,
	}
}

// openAIToGCPVertexAITranslatorV1Embedding translates OpenAI Embeddings API to GCP Vertex AI Gemini Embeddings API.
// Note: This uses the Gemini native API (predict endpoint), not Vertex AI's OpenAI-compatible API:
// https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/text-embeddings-api
type openAIToGCPVertexAITranslatorV1Embedding struct {
	requestModel      internalapi.RequestModel
	modelNameOverride internalapi.ModelNameOverride
}

// createInstancesFromEmbeddingInputItem converts an EmbeddingInputItem to GCP Instance(s).
// This handles the mapping of OpenAI's extended embedding input format to GCP's instance format,
// including task_type and title metadata for optimized embedding generation.
// When content is an array of strings, each string becomes a separate instance with the same task_type.
func createInstancesFromEmbeddingInputItem(item openai.EmbeddingInputItem, instances []*gcp.Instance) []*gcp.Instance {
	switch v := item.Content.Value.(type) {
	case string:
		instance := &gcp.Instance{Content: v}
		if item.TaskType != "" {
			instance.TaskType = item.TaskType
		}
		// Title is only valid with task_type=RETRIEVAL_DOCUMENT.
		// See: https://cloud.google.com/vertex-ai/generative-ai/docs/embeddings/task-types
		if item.TaskType == openai.EmbeddingTaskTypeRetrievalDocument && item.Title != "" {
			instance.Title = item.Title
		}
		instances = append(instances, instance)
	case []string:
		// Multiple strings with the same task_type - each becomes a separate instance
		for _, text := range v {
			instance := &gcp.Instance{Content: text}
			if item.TaskType != "" {
				instance.TaskType = item.TaskType
			}
			// Title is only valid with task_type=RETRIEVAL_DOCUMENT.
			if item.TaskType == openai.EmbeddingTaskTypeRetrievalDocument && item.Title != "" {
				instance.Title = item.Title
			}
			instances = append(instances, instance)
		}
	}
	return instances
}

// setInstances converts OpenAI embedding input to GCP instances.
// It handles multiple input formats: string, []string, EmbeddingInputItem, []EmbeddingInputItem.
// Each input element is converted to a separate GCP Instance for batch embedding generation.
func setInstances(input openai.EmbeddingRequestInput, instances []*gcp.Instance) ([]*gcp.Instance, error) {
	switch v := input.Value.(type) {
	case string:
		instances = append(instances, &gcp.Instance{Content: v})
		return instances, nil
	case []string:
		// Array of strings: create a separate instance for each string.
		for _, text := range v {
			instances = append(instances, &gcp.Instance{Content: text})
		}
		return instances, nil
	case openai.EmbeddingInputItem:
		// Single EmbeddingInputItem with enhanced metadata.
		// Content can be string or []string.
		instances = createInstancesFromEmbeddingInputItem(v, instances)
		return instances, nil
	case []openai.EmbeddingInputItem:
		// Array of EmbeddingInputItem objects with metadata support.
		for _, item := range v {
			instances = createInstancesFromEmbeddingInputItem(item, instances)
		}
		return instances, nil
	default:
		return nil, fmt.Errorf("unsupported input type for embedding: %T (supported: string, []string, EmbeddingInputItem, []EmbeddingInputItem)", v)
	}
}

// openAIEmbeddingToGeminiMessage converts an OpenAI EmbeddingRequest to a GCP PredictRequest.
func openAIEmbeddingToGeminiMessage(openAIReq *openai.EmbeddingRequest) (*gcp.PredictRequest, error) {
	// Convert OpenAI EmbeddingRequest's input to Gemini instances.
	var instances []*gcp.Instance
	instances, err := setInstances(openAIReq.Input, instances)
	if err != nil {
		return nil, err
	}

	// Create the embedding prediction parameters.
	parameters := &gcp.Parameters{}

	// Set output dimensionality if specified.
	if openAIReq.Dimensions != nil && *openAIReq.Dimensions > 0 {
		parameters.OutputDimensionality = *openAIReq.Dimensions
	}

	// Apply vendor-specific fields if present.
	if openAIReq.GCPVertexAIEmbeddingVendorFields != nil {
		// Set auto truncate if specified.
		if openAIReq.AutoTruncate {
			parameters.AutoTruncate = openAIReq.AutoTruncate
		}

		// Apply global task type to all instances if specified.
		// This overrides any task_type set on individual input items.
		if openAIReq.TaskType != "" {
			for _, instance := range instances {
				instance.TaskType = openAIReq.TaskType
			}
		}
	}

	// Build the request using gcp.PredictRequest.
	gcr := &gcp.PredictRequest{
		Instances:  instances,
		Parameters: *parameters,
	}

	return gcr, nil
}

// RequestBody implements [OpenAIEmbeddingTranslator.RequestBody] for GCP Gemini.
// This method translates an OpenAI Embedding request to a GCP Gemini Embeddings API request.
func (o *openAIToGCPVertexAITranslatorV1Embedding) RequestBody(_ []byte, req *openai.EmbeddingRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	o.requestModel = req.Model
	if o.modelNameOverride != "" {
		// Use modelName override if set.
		o.requestModel = o.modelNameOverride
	}

	// Use the predict endpoint for text embeddings in Vertex AI.
	// https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/text-embeddings-api#curl
	path := buildGCPModelPathSuffix(gcpModelPublisherGoogle, o.requestModel, gcpMethodPredict)

	var gcpReq *gcp.PredictRequest

	gcpReq, err = openAIEmbeddingToGeminiMessage(req)
	if err != nil {
		return nil, nil, fmt.Errorf("error converting EmbeddingRequest: %w", err)
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
func (o *openAIToGCPVertexAITranslatorV1Embedding) ResponseHeaders(_ map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAIEmbeddingTranslator.ResponseBody] for GCP Gemini.
// This method translates a GCP Gemini Embeddings API response to the OpenAI Embeddings format.
// GCP Vertex AI uses deterministic model mapping without virtualization, where the requested model
// is exactly what gets executed. The response does not contain a model field, so we return
// the request model that was originally sent.
func (o *openAIToGCPVertexAITranslatorV1Embedding) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.EmbeddingsSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	// Read the Gemini embedding response.
	respBody, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to read gemini embedding response body: %w", err)
	}

	// Unmarshal as GCP PredictResponse.
	var gcpResp gcp.PredictResponse
	err = json.Unmarshal(respBody, &gcpResp)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Convert GCP response to OpenAI format.
	openaiResp := openai.EmbeddingResponse{
		Object: "list",
		Model:  o.requestModel,
		Usage: openai.EmbeddingUsage{
			PromptTokens: 0, // Will be set from token usage
			TotalTokens:  0, // Will be set from token usage
		},
	}

	var promptTokens int
	// Convert embedding vectors from GCP predictions to OpenAI embeddings.
	if len(gcpResp.Predictions) > 0 {
		openaiResp.Data = make([]openai.Embedding, len(gcpResp.Predictions))
		for i, prediction := range gcpResp.Predictions {
			if prediction != nil {
				// Convert float32 slice to float64 slice for OpenAI format.
				float64Values := make([]float64, len(prediction.Embeddings.Values))
				for j, v := range prediction.Embeddings.Values {
					float64Values[j] = float64(v)
				}

				openaiResp.Data[i] = openai.Embedding{
					Object:    "embedding",
					Index:     i,
					Embedding: openai.EmbeddingUnion{Value: float64Values},
				}

				// Extract token count from statistics if available.
				if prediction.Embeddings.Statistics != nil {
					// Accumulate token counts across all predictions.
					promptTokens += prediction.Embeddings.Statistics.TokenCount
					// Propagate truncation information to the response.
					openaiResp.Data[i].Truncated = prediction.Embeddings.Statistics.Truncated
				}
			}
		}
	} else {
		openaiResp.Data = []openai.Embedding{}
	}

	// Set token usage from accumulated values.
	// Embeddings only consume input tokens, so total equals prompt tokens.
	openaiResp.Usage.PromptTokens = promptTokens
	openaiResp.Usage.TotalTokens = promptTokens

	// Marshal the OpenAI response.
	newBody, err = json.Marshal(openaiResp)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to marshal OpenAI response: %w", err)
	}

	// Update token usage metrics.
	// Embeddings don't return output tokens; populate input and total when provided.
	tokenUsage.SetInputTokens(uint32(promptTokens)) //nolint:gosec
	tokenUsage.SetTotalTokens(uint32(promptTokens)) //nolint:gosec

	// Record the response in the span for tracing.
	if span != nil {
		span.RecordResponse(&openaiResp)
	}

	newHeaders = []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}

	responseModel = openaiResp.Model

	return
}

// ResponseError implements [OpenAIEmbeddingTranslator.ResponseError].
// Translate GCP Vertex AI exceptions to OpenAI error type.
// GCP error responses typically contain JSON with error details or plain text error messages.
func (o *openAIToGCPVertexAITranslatorV1Embedding) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	return convertGCPVertexAIErrorToOpenAI(respHeaders, body)
}
