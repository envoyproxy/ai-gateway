// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package endpointspec defines the EndpointSpec which is to bundle the translator, tracing
// and most importantly request and response types for different API endpoints.
package endpointspec

import (
	"encoding/json"
	"fmt"

	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/apischema/tokenize"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

type (
	// Spec defines methods for parsing request bodies and selecting translators
	// for different API endpoints.
	//
	// Type Parameters:
	// * ReqT: The request type.
	// * RespT: The response type.
	// * RespChunkT: The chunk type for streaming responses.
	//
	// This must be implemented by specific endpoint handlers to provide
	// custom logic for parsing and translation.
	Spec[ReqT, RespT, RespChunkT any] interface {
		// ParseBody parses the request body and returns the original model,
		// the parsed request, whether the request is streaming, any mutated body,
		// and an error if parsing fails.
		//
		// Parameters:
		// * body: The raw request body as a byte slice.
		// * costConfigured: A boolean indicating if cost metrics are configured.
		//
		// Returns:
		// * originalModel: The original model specified in the request.
		// * req: The parsed request of type ReqT.
		// * stream: A boolean indicating if the request is for streaming responses.
		// * mutatedBody: The possibly mutated request body as a byte slice. Or nil if no mutation is needed.
		// * err: An error if parsing fails.
		ParseBody(body []byte, costConfigured bool) (originalModel internalapi.OriginalModel, req *ReqT, stream bool, mutatedBody []byte, err error)
		// GetTranslator selects the appropriate translator based on the output API schema
		// and an optional model name override.
		//
		// Parameters:
		// * out: The output API schema for which the translator is needed.
		// * modelNameOverride: An optional model name to override the one specified in the request.
		//
		// Returns:
		// * translator: The selected translator of type Translator[ReqT, RespT, RespChunkT].
		// * err: An error if translator selection fails.
		GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.Translator[ReqT, tracing.Span[RespT, RespChunkT]], error)
	}
	// ChatCompletionsEndpointSpec implements EndpointSpec for /v1/chat/completions.
	ChatCompletionsEndpointSpec struct{}
	// CompletionsEndpointSpec implements EndpointSpec for /v1/completions.
	CompletionsEndpointSpec struct{}
	// EmbeddingsEndpointSpec implements EndpointSpec for /v1/embeddings.
	EmbeddingsEndpointSpec struct{}
	// ImageGenerationEndpointSpec implements EndpointSpec for /v1/images/generations.
	ImageGenerationEndpointSpec struct{}
	// MessagesEndpointSpec implements EndpointSpec for /v1/messages.
	MessagesEndpointSpec struct{}
	// RerankEndpointSpec implements EndpointSpec for /v2/rerank.
	RerankEndpointSpec struct{}
	// TokenizeEndpointSpec implements EndpointSpec for /v1/tokenize.
	TokenizeEndpointSpec struct{}
)

// ParseBody implements [EndpointSpec.ParseBody].
func (ChatCompletionsEndpointSpec) ParseBody(
	body []byte,
	costConfigured bool,
) (internalapi.OriginalModel, *openai.ChatCompletionRequest, bool, []byte, error) {
	var req openai.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal chat completion request: %w", err)
	}
	var mutatedBody []byte
	if req.Stream && costConfigured && (req.StreamOptions == nil || !req.StreamOptions.IncludeUsage) {
		// If the request is a streaming request and cost metrics are configured, we need to include usage in the response
		// to avoid the bypassing of the token usage calculation.
		req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
		// Rewrite the original bytes to include the stream_options.include_usage=true so that forcing the request body
		// mutation, which uses this raw body, will also result in the stream_options.include_usage=true.
		var err error
		mutatedBody, err = sjson.SetBytesOptions(body, "stream_options.include_usage", true, &sjson.Options{
			Optimistic: true,
			// Note: it is safe to do in-place replacement since this route level processor is executed once per request,
			// and the result can be safely shared among possible multiple retries.
			ReplaceInPlace: true,
		})
		if err != nil {
			return "", nil, false, nil, fmt.Errorf("failed to set stream_options: %w", err)
		}
	}
	return req.Model, &req, req.Stream, mutatedBody, nil
}

// GetTranslator implements [EndpointSpec.GetTranslator].
func (ChatCompletionsEndpointSpec) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.OpenAIChatCompletionTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewChatCompletionOpenAIToOpenAITranslator(schema.OpenAIPrefix(), modelNameOverride), nil
	case filterapi.APISchemaAWSBedrock:
		return translator.NewChatCompletionOpenAIToAWSBedrockTranslator(modelNameOverride), nil
	case filterapi.APISchemaAzureOpenAI:
		return translator.NewChatCompletionOpenAIToAzureOpenAITranslator(schema.Version, modelNameOverride), nil
	case filterapi.APISchemaGCPVertexAI:
		return translator.NewChatCompletionOpenAIToGCPVertexAITranslator(modelNameOverride), nil
	case filterapi.APISchemaGCPAnthropic:
		return translator.NewChatCompletionOpenAIToGCPAnthropicTranslator(schema.Version, modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

// ParseBody implements [EndpointSpec.ParseBody].
func (CompletionsEndpointSpec) ParseBody(
	body []byte,
	_ bool,
) (internalapi.OriginalModel, *openai.CompletionRequest, bool, []byte, error) {
	var openAIReq openai.CompletionRequest
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal completion request: %w", err)
	}
	return openAIReq.Model, &openAIReq, openAIReq.Stream, nil, nil
}

// GetTranslator implements [EndpointSpec.GetTranslator].
func (CompletionsEndpointSpec) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.OpenAICompletionTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewCompletionOpenAIToOpenAITranslator(schema.OpenAIPrefix(), modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

// ParseBody implements [EndpointSpec.ParseBody].
func (EmbeddingsEndpointSpec) ParseBody(
	body []byte,
	_ bool,
) (internalapi.OriginalModel, *openai.EmbeddingRequest, bool, []byte, error) {
	var openAIReq openai.EmbeddingRequest
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal embedding request: %w", err)
	}
	return openAIReq.Model, &openAIReq, false, nil, nil
}

// GetTranslator implements [EndpointSpec.GetTranslator].
func (EmbeddingsEndpointSpec) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.OpenAIEmbeddingTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewEmbeddingOpenAIToOpenAITranslator(schema.OpenAIPrefix(), modelNameOverride), nil
	case filterapi.APISchemaAzureOpenAI:
		return translator.NewEmbeddingOpenAIToAzureOpenAITranslator(schema.Version, modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

func (ImageGenerationEndpointSpec) ParseBody(
	body []byte,
	_ bool,
) (internalapi.OriginalModel, *openai.ImageGenerationRequest, bool, []byte, error) {
	var openAIReq openai.ImageGenerationRequest
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal image generation request: %w", err)
	}
	return openAIReq.Model, &openAIReq, false, nil, nil
}

// GetTranslator implements [EndpointSpec.GetTranslator].
func (ImageGenerationEndpointSpec) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.OpenAIImageGenerationTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewImageGenerationOpenAIToOpenAITranslator(schema.OpenAIPrefix(), modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

// ParseBody implements [EndpointSpec.ParseBody].
func (MessagesEndpointSpec) ParseBody(
	body []byte,
	_ bool,
) (internalapi.OriginalModel, *anthropic.MessagesRequest, bool, []byte, error) {
	var anthropicReq anthropic.MessagesRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal Anthropic Messages body: %w", err)
	}

	model := anthropicReq.Model
	if model == "" {
		return "", nil, false, nil, fmt.Errorf("model field is required in Anthropic request")
	}

	stream := anthropicReq.Stream
	return model, &anthropicReq, stream, nil, nil
}

// GetTranslator implements [EndpointSpec.GetTranslator].
func (MessagesEndpointSpec) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.AnthropicMessagesTranslator, error) {
	// Messages processor only supports Anthropic-native translators.
	switch schema.Name {
	case filterapi.APISchemaGCPAnthropic:
		return translator.NewAnthropicToGCPAnthropicTranslator(schema.Version, modelNameOverride), nil
	case filterapi.APISchemaAWSAnthropic:
		return translator.NewAnthropicToAWSAnthropicTranslator(schema.Version, modelNameOverride), nil
	case filterapi.APISchemaAnthropic:
		return translator.NewAnthropicToAnthropicTranslator(schema.Version, modelNameOverride), nil
	default:
		return nil, fmt.Errorf("/v1/messages endpoint only supports backends that return native Anthropic format (Anthropic, GCPAnthropic, AWSAnthropic). Backend %s uses different model format", schema.Name)
	}
}

// ParseBody implements [EndpointSpec.ParseBody].
func (RerankEndpointSpec) ParseBody(
	body []byte,
	_ bool,
) (internalapi.OriginalModel, *cohereschema.RerankV2Request, bool, []byte, error) {
	var req cohereschema.RerankV2Request
	if err := json.Unmarshal(body, &req); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal rerank request: %w", err)
	}
	return req.Model, &req, false, nil, nil
}

// GetTranslator implements [EndpointSpec.GetTranslator].
func (RerankEndpointSpec) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.CohereRerankTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaCohere:
		return translator.NewRerankCohereToCohereTranslator(schema.Version, modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

// ParseBody implements [EndpointSpec.ParseBody].
func (TokenizeEndpointSpec) ParseBody(
	body []byte,
	_ bool,
) (internalapi.OriginalModel, *tokenize.TokenizeRequestUnion, bool, []byte, error) {
	// Try to detect if it's a chat or completion request by checking for "messages" field
	var rawRequest map[string]interface{}
	if err := json.Unmarshal(body, &rawRequest); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal tokenize request: %w", err)
	}

	var req tokenize.TokenizeRequestUnion
	var model string

	// Check if this is a chat tokenize request (has "messages" field)
	if _, hasMessages := rawRequest["messages"]; hasMessages {
		var chatReq tokenize.TokenizeChatRequest
		if err := json.Unmarshal(body, &chatReq); err != nil {
			return "", nil, false, nil, fmt.Errorf("failed to unmarshal chat tokenize request: %w", err)
		}
		// Validate the chat request
		if err := chatReq.Validate(); err != nil {
			return "", nil, false, nil, fmt.Errorf("invalid chat tokenize request: %w", err)
		}
		req.TokenizeChatRequest = &chatReq
		model = chatReq.Model
	} else {
		// This is a completion tokenize request (has "prompt" field)
		var completionReq tokenize.TokenizeCompletionRequest
		if err := json.Unmarshal(body, &completionReq); err != nil {
			return "", nil, false, nil, fmt.Errorf("failed to unmarshal completion tokenize request: %w", err)
		}
		req.TokenizeCompletionRequest = &completionReq
		model = completionReq.Model
	}

	// Validate that the union has exactly one request type set
	if err := req.Validate(); err != nil {
		return "", nil, false, nil, fmt.Errorf("invalid tokenize request: %w", err)
	}

	// Tokenize requests are never streaming
	return model, &req, false, nil, nil
}

// GetTranslator implements [EndpointSpec.GetTranslator].
func (TokenizeEndpointSpec) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.TokenizeTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewTokenizeTranslator(modelNameOverride), nil
	case filterapi.APISchemaGCPVertexAI:
		return translator.NewTokenizeToGCPVertexAITranslator(modelNameOverride), nil
	case filterapi.APISchemaGCPAnthropic:
		return translator.NewTokenizeToGCPAnthropicTranslator(modelNameOverride), nil
	case filterapi.APISchemaAWSBedrock:
		return translator.NewTokenizeToAWSBedrockTranslator(modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema for tokenize endpoint: backend=%s", schema.Name)
	}
}
