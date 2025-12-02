// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// NewEmbeddingOpenAIToAzureOpenAITranslator implements [Factory] for OpenAI to Azure OpenAI translation
// for embeddings.
func NewEmbeddingOpenAIToGCPVertexAITranslator(requestModel internalapi.RequestModel, modelNameOverride internalapi.ModelNameOverride) OpenAIEmbeddingTranslator {
	return &openAIToGCPVertexAITranslatorV1Embedding{
		apiVersion: apiVersion,
		openAIToOpenAITranslatorV1Embedding: openAIToOpenAITranslatorV1Embedding{
			modelNameOverride: modelNameOverride,
		},
	}
}

// openAIToGCPVertexAITranslatorV1Embedding implements [OpenAIEmbeddingTranslator] for /embeddings.
type openAIToGCPVertexAITranslatorV1Embedding[T openai.EmbeddingRequest] struct {
	requestModel internalapi.RequestModel
	openAIToOpenAITranslatorV1Embedding
}



func InputToGeminiConent(input openai.EmbeddingRequestInput){
	 switch v := input.Value.(type) {
      case string:

          return v, "string", nil
      case []string:
          // Array of text inputs
          return v, "string_array", nil
      case []int64:
          // Array of token IDs
          return v, "token_array", nil
      case [][]int64:
          // Array of token ID arrays
          return v, "token_array_batch", nil
      default:
          return nil, "unknown", fmt.Errorf("unsupported input type: %T", v)
      }


}

// openAIToGCPVertexAITranslatorV1Embedding converts an OpenAI EmbeddingRequest to a GCP Gemini GenerateContentRequest.
func openAIEmbeddingCompletionToGeminiMessage(openAIReq *openai.EmbeddingCompletionRequest, requestModel internalapi.RequestModel) (*gcp.EmbedContentRequest, error) {
	// Convert OpenAI EmbeddingRequest's input to Gemini Contents
	contents, err := InputToGeminiConent(openAIReq.Input, requestModel)
	if err != nil {
		return nil, err
	}

	// Convert generation config.
	embedConfig,, err := openAIReqToGeminiGenerationConfig(openAIReq, requestModel)
	if err != nil {
		return nil, fmt.Errorf("error converting generation config: %w", err)
	}

	gcr := gcp.EmbedContentRequest{
		Contents:          contents,
		Config:  embedConfig,
	}

	return &gcr, nil
}

// RequestBody implements [OpenAIEmbeddingTranslator.RequestBody].
func (o *openAIToGCPVertexAITranslatorV1Embedding[T]) RequestBody(original []byte, req *T, onRetry bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {

	o.requestModel = openai.GetModelFromEmbeddingRequest(req)
	if o.modelNameOverride != "" {
		// Use modelName override if set.
		o.requestModel = o.modelNameOverride
	}

	// Choose the correct endpoint based on streaming.
	var path string

	path = buildGCPModelPathSuffix(gcpModelPublisherGoogle, o.requestModel, gcpMethodGenerateContent)

	switch any(*req).(type) {
	case openai.EmbeddingCompletionRequest:
		gcpReq, err := openAIEmbeddingCompletionToGeminiMessage(openAIReq, o.requestModel)
	case openai.EmbeddingChatRequest:
		gcpReq, err := openAIEmbeddingChatToGeminiMessage(openAIReq, o.requestModel)

	default:
		return nil, nil, fmt.Errorf("request body is wrong: %w", err)
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
