// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"encoding/json"
	"fmt"
	"log/slog"

	openaisdk "github.com/openai/openai-go/v2"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

// ChatCompletionProcessorFactory returns a ProcessorFactory for /v1/chat/completions.
func ChatCompletionProcessorFactory(f metrics.Factory, tracer tracing.ChatCompletionTracer) ProcessorFactory {
	return newFactory[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk, chatCompletionsEndpointHandler](f, tracer)
}

// CompletionsProcessorFactory returns a ProcessorFactory for /v1/completions.
func CompletionsProcessorFactory(f metrics.Factory, tracer tracing.CompletionTracer) ProcessorFactory {
	return newFactory[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse, completionsEndpointHandler](f, tracer)
}

// EmbeddingsProcessorFactory returns a ProcessorFactory for /v1/embeddings.
func EmbeddingsProcessorFactory(f metrics.Factory, tracer tracing.EmbeddingsTracer) ProcessorFactory {
	return newFactory[openai.EmbeddingRequest, openai.EmbeddingResponse, struct{}, embeddingsEndpointHandler](f, tracer)
}

// ImageGenerationProcessorFactory returns a ProcessorFactory for /v1/images/generations.
func ImageGenerationProcessorFactory(f metrics.Factory, tracer tracing.ImageGenerationTracer) ProcessorFactory {
	return newFactory[openaisdk.ImageGenerateParams, openaisdk.ImagesResponse, struct{}, imageGenerationEndpointHandler](f, tracer)
}

// MessagesProcessorFactory returns a ProcessorFactory for /v1/messages.
func MessagesProcessorFactory(f metrics.Factory, tracer tracing.MessageTracer) ProcessorFactory {
	return newFactory[anthropic.MessagesRequest, anthropic.MessagesResponse, anthropic.MessagesStreamChunk, messagesEndpointHandler](f, tracer)
}

// RerankProcessorFactory returns a ProcessorFactory for /v2/rerank.
func RerankProcessorFactory(f metrics.Factory, tracer tracing.RerankTracer) ProcessorFactory {
	return newFactory[cohereschema.RerankV2Request, cohereschema.RerankV2Response, struct{}, rerankEndpointHandler](f, tracer)
}

// newFactory creates a ProcessorFactory with the given parameters.
//
// Type Parameters:
// * ReqT: The request type.
// * RespT: The response type.
// * RespChunkT: The chunk type for streaming responses.
//
// Parameters:
// * f: Metrics factory for creating metrics instances.
// * tracer: Request tracer for tracing requests and responses.
// * parseBody: Function to parse the request body.
// * selectTranslator: Function to select the appropriate translator based on the output schema.
//
// Returns:
// * ProcessorFactory: A factory function to create processors based on the configuration.
func newFactory[ReqT any, RespT any, RespChunkT any, EndpointHandlerT endpointHandler[ReqT, RespT, RespChunkT]](
	f metrics.Factory,
	tracer tracing.RequestTracer[ReqT, RespT, RespChunkT],
) ProcessorFactory {
	return func(config *filterapi.RuntimeConfig, requestHeaders map[string]string, logger *slog.Logger, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return newRouterProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT](
				config,
				requestHeaders,
				logger,
				tracer,
			), nil
		}
		return newUpstreamProcessor[ReqT, RespT, RespChunkT, EndpointHandlerT](
			requestHeaders,
			f.NewMetrics(),
		), nil
	}
}

type (
	// endpointHandler defines methods for parsing request bodies and selecting translators
	// for different API endpoints.
	//
	// Type Parameters:
	// * ReqT: The request type.
	// * RespT: The response type.
	// * RespChunkT: The chunk type for streaming responses.
	//
	// This must be implemented by specific endpoint handlers to provide
	// custom logic for parsing and translation.
	endpointHandler[ReqT, RespT, RespChunkT any] interface {
		// ParseBody parses the request body and returns the original model,
		// the parsed request, whether the request is streaming, any mutated body,
		// and an error if parsing fails.
		//
		// Parameters:
		// * body: The raw request body as a byte slice.
		//
		// Returns:
		// * originalModel: The original model specified in the request.
		// * req: The parsed request of type ReqT.
		// * stream: A boolean indicating if the request is for streaming responses.
		// * mutatedBody: The possibly mutated request body as a byte slice. Or nil if no mutation is needed.
		// * err: An error if parsing fails.
		ParseBody(body []byte) (originalModel internalapi.OriginalModel, req *ReqT, stream bool, mutatedBody []byte, err error)
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
	// chatCompletionsEndpointHandler implements endpointHandler for /v1/chat/completions.
	chatCompletionsEndpointHandler struct{}
	// completionsEndpointHandler implements endpointHandler for /v1/completions.
	completionsEndpointHandler struct{}
	// embeddingsEndpointHandler implements endpointHandler for /v1/embeddings.
	embeddingsEndpointHandler struct{}
	// imageGenerationEndpointHandler implements endpointHandler for /v1/images/generations.
	imageGenerationEndpointHandler struct{}
	// messagesEndpointHandler implements endpointHandler for /v1/messages.
	messagesEndpointHandler struct{}
	// rerankEndpointHandler implements endpointHandler for /v2/rerank.
	rerankEndpointHandler struct{}
)

// ParseBody implements [endpointHandler.ParseBody].
func (chatCompletionsEndpointHandler) ParseBody(
	body []byte,
) (internalapi.OriginalModel, *openai.ChatCompletionRequest, bool, []byte, error) {
	var req openai.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal chat completion request: %w", err)
	}
	var mutatedBody []byte
	if req.Stream && (req.StreamOptions == nil || !req.StreamOptions.IncludeUsage) {
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

// GetTranslator implements [endpointHandler.GetTranslator].
func (chatCompletionsEndpointHandler) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.OpenAIChatCompletionTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewChatCompletionOpenAIToOpenAITranslator(schema.Version, modelNameOverride), nil
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

// ParseBody implements [endpointHandler.ParseBody].
func (completionsEndpointHandler) ParseBody(
	body []byte,
) (internalapi.OriginalModel, *openai.CompletionRequest, bool, []byte, error) {
	var openAIReq openai.CompletionRequest
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal completion request: %w", err)
	}
	return openAIReq.Model, &openAIReq, openAIReq.Stream, nil, nil
}

// GetTranslator implements [endpointHandler.GetTranslator].
func (completionsEndpointHandler) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.OpenAICompletionTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewCompletionOpenAIToOpenAITranslator(schema.Version, modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

// ParseBody implements [endpointHandler.ParseBody].
func (embeddingsEndpointHandler) ParseBody(
	body []byte,
) (internalapi.OriginalModel, *openai.EmbeddingRequest, bool, []byte, error) {
	var openAIReq openai.EmbeddingRequest
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal embedding request: %w", err)
	}
	return openAIReq.Model, &openAIReq, false, nil, nil
}

// GetTranslator implements [endpointHandler.GetTranslator].
func (embeddingsEndpointHandler) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.OpenAIEmbeddingTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewEmbeddingOpenAIToOpenAITranslator(schema.Version, modelNameOverride), nil
	case filterapi.APISchemaAzureOpenAI:
		return translator.NewEmbeddingOpenAIToAzureOpenAITranslator(schema.Version, modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

func (imageGenerationEndpointHandler) ParseBody(
	body []byte,
) (internalapi.OriginalModel, *openaisdk.ImageGenerateParams, bool, []byte, error) {
	var openAIReq openaisdk.ImageGenerateParams
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal image generation request: %w", err)
	}
	return openAIReq.Model, &openAIReq, false, nil, nil
}

// GetTranslator implements [endpointHandler.GetTranslator].
func (imageGenerationEndpointHandler) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.OpenAIImageGenerationTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewImageGenerationOpenAIToOpenAITranslator(schema.Version, modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

// ParseBody implements [endpointHandler.ParseBody].
func (messagesEndpointHandler) ParseBody(
	body []byte,
) (internalapi.OriginalModel, *anthropic.MessagesRequest, bool, []byte, error) {
	var anthropicReq anthropic.MessagesRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal Anthropic Messages body: %w", err)
	}

	model := anthropicReq.GetModel()
	if model == "" {
		return "", nil, false, nil, fmt.Errorf("model field is required in Anthropic request")
	}

	stream := anthropicReq.GetStream()
	return model, &anthropicReq, stream, nil, nil
}

// GetTranslator implements [endpointHandler.GetTranslator].
func (messagesEndpointHandler) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.AnthropicMessagesTranslator, error) {
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

// ParseBody implements [endpointHandler.ParseBody].
func (rerankEndpointHandler) ParseBody(
	body []byte,
) (internalapi.OriginalModel, *cohereschema.RerankV2Request, bool, []byte, error) {
	var req cohereschema.RerankV2Request
	if err := json.Unmarshal(body, &req); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal rerank request: %w", err)
	}
	return req.Model, &req, false, nil, nil
}

// GetTranslator implements [endpointHandler.GetTranslator].
func (rerankEndpointHandler) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.CohereRerankTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaCohere:
		return translator.NewRerankCohereToCohereTranslator(schema.Version, modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}
