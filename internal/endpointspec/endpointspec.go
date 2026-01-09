// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package endpointspec defines the EndpointSpec which is to bundle the translator, tracing
// and most importantly request and response types for different API endpoints.
package endpointspec

import (
	"fmt"
	"hash/crc32"

	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
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
		GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.Translator[ReqT, tracingapi.Span[RespT, RespChunkT]], error)
		// RedactSensitiveInfoFromRequest creates a redacted copy of the request for safe debug logging.
		// Sensitive content (messages, images, audio, tool parameters, etc.) is replaced with placeholders
		// containing length and hash information to aid in debugging cache hits/misses and correlation.
		//
		// The returned request preserves the structure but removes actual sensitive data, making it
		// safe to include in logs. It should NOT be used for actual AI provider requests.
		//
		// Parameters:
		// * req: The original request to redact.
		//
		// Returns:
		// * redactedReq: A copy with sensitive fields replaced by [REDACTED LENGTH=n HASH=xxxx] placeholders.
		// * err: An error if redaction fails (implementation-specific).
		RedactSensitiveInfoFromRequest(req *ReqT) (redactedReq *ReqT, err error)
	}
	// ChatCompletionsEndpointSpec implements EndpointSpec for /v1/chat/completions.
	ChatCompletionsEndpointSpec struct{}
	// CompletionsEndpointSpec implements EndpointSpec for /v1/completions.
	CompletionsEndpointSpec struct{}
	// EmbeddingsEndpointSpec implements EndpointSpec for /v1/embeddings.
	EmbeddingsEndpointSpec struct{}
	// ImageGenerationEndpointSpec implements EndpointSpec for /v1/images/generations.
	ImageGenerationEndpointSpec struct{}
	// ResponsesEndpointSpec implements EndpointSpec for /v1/responses.
	ResponsesEndpointSpec struct{}
	// MessagesEndpointSpec implements EndpointSpec for /v1/messages.
	MessagesEndpointSpec struct{}
	// RerankEndpointSpec implements EndpointSpec for /v2/rerank.
	RerankEndpointSpec struct{}
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

// RedactSensitiveInfoFromRequest implements [EndpointSpec.RedactSensitiveInfoFromRequest].
func (ChatCompletionsEndpointSpec) RedactSensitiveInfoFromRequest(req *openai.ChatCompletionRequest) (redactedReq *openai.ChatCompletionRequest, err error) {
	// Create a shallow copy of the request
	redacted := *req

	// Redact all message content (user prompts, assistant responses, system messages, etc.)
	redacted.Messages = make([]openai.ChatCompletionMessageParamUnion, len(req.Messages))
	for i, msg := range req.Messages {
		redacted.Messages[i] = redactMessage(msg)
	}

	// Redact tool definitions (function descriptions and parameter schemas may contain sensitive information)
	if len(req.Tools) > 0 {
		redacted.Tools = make([]openai.Tool, len(req.Tools))
		for i, tool := range req.Tools {
			redacted.Tools[i] = tool
			if tool.Function != nil {
				redactedFunc := *tool.Function
				redactedFunc.Description = redactString(redactedFunc.Description)
				// Redact parameters by replacing with a placeholder map to preserve type safety
				// while hiding sensitive schema information
				if redactedFunc.Parameters != nil {
					hash := computeContentHash(fmt.Sprintf("%v", redactedFunc.Parameters))
					redactedFunc.Parameters = map[string]any{
						"_redacted": fmt.Sprintf("REDACTED HASH=%s", hash),
					}
				}
				redacted.Tools[i].Function = &redactedFunc
			}
		}
	}

	// Redact prediction content if present (cached prompts, prefill content)
	if req.PredictionContent != nil {
		redactedPrediction := *req.PredictionContent
		redactedPrediction.Content = redactContentUnion(req.PredictionContent.Content)
		redacted.PredictionContent = &redactedPrediction
	}

	// Redact response format schema (may contain sensitive structure information)
	if req.ResponseFormat != nil {
		redacted.ResponseFormat = redactResponseFormat(req.ResponseFormat)
	}

	// Redact guided JSON schema (contains raw JSON schema definition)
	if len(req.GuidedJSON) > 0 {
		hash := computeContentHash(string(req.GuidedJSON))
		redacted.GuidedJSON = []byte(fmt.Sprintf(`{"_redacted":"REDACTED LENGTH=%d HASH=%s"}`, len(req.GuidedJSON), hash))
	}

	return &redacted, nil
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

// RedactSensitiveInfoFromRequest implements [EndpointSpec.RedactSensitiveInfoFromRequest].
func (CompletionsEndpointSpec) RedactSensitiveInfoFromRequest(req *openai.CompletionRequest) (redactedReq *openai.CompletionRequest, err error) {
	// Placeholder if redaction is required in future
	return req, nil
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
	case filterapi.APISchemaGCPVertexAI:
		return translator.NewEmbeddingOpenAIToGCPVertexAITranslator("", modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

// RedactSensitiveInfoFromRequest implements [EndpointSpec.RedactSensitiveInfoFromRequest].
func (EmbeddingsEndpointSpec) RedactSensitiveInfoFromRequest(req *openai.EmbeddingRequest) (redactedReq *openai.EmbeddingRequest, err error) {
	// Placeholder if redaction is required in future
	return req, nil
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

// RedactSensitiveInfoFromRequest implements [EndpointSpec.RedactSensitiveInfoFromRequest].
func (ImageGenerationEndpointSpec) RedactSensitiveInfoFromRequest(req *openai.ImageGenerationRequest) (redactedReq *openai.ImageGenerationRequest, err error) {
	// Placeholder if redaction is required in future
	return req, nil
}

// ParseBody implements [EndpointSpec.ParseBody].
func (ResponsesEndpointSpec) ParseBody(
	body []byte,
	_ bool,
) (internalapi.OriginalModel, *openai.ResponseRequest, bool, []byte, error) {
	var openAIReq openai.ResponseRequest
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return "", nil, false, nil, fmt.Errorf("failed to unmarshal responses request: %w", err)
	}
	return openAIReq.Model, &openAIReq, openAIReq.Stream, nil, nil
}

// GetTranslator implements [EndpointSpec.GetTranslator].
func (ResponsesEndpointSpec) GetTranslator(schema filterapi.VersionedAPISchema, modelNameOverride string) (translator.OpenAIResponsesTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return translator.NewResponsesOpenAIToOpenAITranslator(schema.OpenAIPrefix(), modelNameOverride), nil
	default:
		return nil, fmt.Errorf("unsupported API schema: backend=%s", schema)
	}
}

// RedactSensitiveInfoFromRequest implements [EndpointSpec.RedactSensitiveInfoFromRequest].
func (ResponsesEndpointSpec) RedactSensitiveInfoFromRequest(req *openai.ResponseRequest) (redactedReq *openai.ResponseRequest, err error) {
	// Placeholder if redaction is required in future
	return req, nil
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

// RedactSensitiveInfoFromRequest implements [EndpointSpec.RedactSensitiveInfoFromRequest].
func (MessagesEndpointSpec) RedactSensitiveInfoFromRequest(req *anthropic.MessagesRequest) (redactedReq *anthropic.MessagesRequest, err error) {
	// Placeholder if redaction is required in future
	return req, nil
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

// RedactSensitiveInfoFromRequest implements [EndpointSpec.RedactSensitiveInfoFromRequest].
func (RerankEndpointSpec) RedactSensitiveInfoFromRequest(req *cohereschema.RerankV2Request) (redactedReq *cohereschema.RerankV2Request, err error) {
	// Placeholder if redaction is required in future
	return req, nil
}

// computeContentHash computes a fast, non-cryptographic hash for content uniqueness tracking.
// This hash is used for debugging purposes, particularly for:
// - Tracking cache hits/misses by correlating identical content across requests
// - Identifying duplicate or similar requests without exposing actual content
// - Debugging issues by matching redacted logs to specific content patterns
//
// We use CRC32 instead of cryptographic hashes (SHA256) because:
// - Much faster computation (important when redacting large messages with many parts)
// - Sufficient collision resistance for debugging and uniqueness tracking
// - Not used for security purposes, only for correlation and debugging
//
// Returns an 8-character hex string representation of the CRC32 hash.
func computeContentHash(s string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(s)))
}

// redactString replaces sensitive string content with a placeholder containing length and hash.
// The hash allows correlating logs with specific content for debugging without exposing
// the actual sensitive data.
//
// Format: [REDACTED LENGTH=n HASH=xxxxxxxx]
//
// Example: "secret API key 12345" becomes "[REDACTED LENGTH=19 HASH=a3f5e8c2]"
func redactString(s string) string {
	if s == "" {
		return ""
	}
	hash := computeContentHash(s)
	return fmt.Sprintf("[REDACTED LENGTH=%d HASH=%s]", len(s), hash)
}

// redactMessage redacts sensitive content from a chat message while preserving its type and structure.
// This dispatches to role-specific redaction functions based on the message type.
func redactMessage(msg openai.ChatCompletionMessageParamUnion) openai.ChatCompletionMessageParamUnion {
	switch {
	case msg.OfUser != nil:
		return redactUserMessage(msg)
	case msg.OfAssistant != nil:
		return redactAssistantMessage(msg)
	case msg.OfSystem != nil:
		return redactSystemMessage(msg)
	case msg.OfDeveloper != nil:
		return redactDeveloperMessage(msg)
	case msg.OfTool != nil:
		return redactToolMessage(msg)
	default:
		return msg
	}
}

// redactUserMessage redacts content from a user message, including text, images, audio, and files.
func redactUserMessage(msg openai.ChatCompletionMessageParamUnion) openai.ChatCompletionMessageParamUnion {
	redactedMsg := *msg.OfUser
	redactedMsg.Content = redactStringOrUserRoleContentUnion(msg.OfUser.Content)
	return openai.ChatCompletionMessageParamUnion{OfUser: &redactedMsg}
}

// redactAssistantMessage redacts content from an assistant message, including message text and tool call arguments.
func redactAssistantMessage(msg openai.ChatCompletionMessageParamUnion) openai.ChatCompletionMessageParamUnion {
	redactedMsg := *msg.OfAssistant
	redactedMsg.Content = redactStringOrAssistantRoleContentUnion(msg.OfAssistant.Content)
	// Redact tool call arguments (may contain sensitive data extracted from user messages)
	if len(msg.OfAssistant.ToolCalls) > 0 {
		redactedMsg.ToolCalls = make([]openai.ChatCompletionMessageToolCallParam, len(msg.OfAssistant.ToolCalls))
		for i, tc := range msg.OfAssistant.ToolCalls {
			redactedToolCall := tc
			redactedToolCall.Function.Name = redactString(tc.Function.Name)
			redactedToolCall.Function.Arguments = redactString(tc.Function.Arguments)
			redactedMsg.ToolCalls[i] = redactedToolCall
		}
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &redactedMsg}
}

// redactSystemMessage redacts content from a system message.
func redactSystemMessage(msg openai.ChatCompletionMessageParamUnion) openai.ChatCompletionMessageParamUnion {
	redactedMsg := *msg.OfSystem
	redactedMsg.Content = redactContentUnion(msg.OfSystem.Content)
	return openai.ChatCompletionMessageParamUnion{OfSystem: &redactedMsg}
}

// redactDeveloperMessage redacts content from a developer message.
func redactDeveloperMessage(msg openai.ChatCompletionMessageParamUnion) openai.ChatCompletionMessageParamUnion {
	redactedMsg := *msg.OfDeveloper
	redactedMsg.Content = redactContentUnion(msg.OfDeveloper.Content)
	return openai.ChatCompletionMessageParamUnion{OfDeveloper: &redactedMsg}
}

// redactToolMessage redacts content from a tool message.
func redactToolMessage(msg openai.ChatCompletionMessageParamUnion) openai.ChatCompletionMessageParamUnion {
	redactedMsg := *msg.OfTool
	redactedMsg.Content = redactContentUnion(msg.OfTool.Content)
	return openai.ChatCompletionMessageParamUnion{OfTool: &redactedMsg}
}

// redactContentUnion redacts content from a ContentUnion, handling both string and structured content parts.
func redactContentUnion(content openai.ContentUnion) openai.ContentUnion {
	switch v := content.Value.(type) {
	case string:
		return openai.ContentUnion{Value: redactString(v)}
	case []openai.ChatCompletionContentPartTextParam:
		redactedParts := make([]openai.ChatCompletionContentPartTextParam, len(v))
		for i, part := range v {
			redactedPart := part
			redactedPart.Text = redactString(part.Text)
			redactedParts[i] = redactedPart
		}
		return openai.ContentUnion{Value: redactedParts}
	default:
		return content
	}
}

// redactStringOrUserRoleContentUnion redacts content from a StringOrUserRoleContentUnion.
func redactStringOrUserRoleContentUnion(content openai.StringOrUserRoleContentUnion) openai.StringOrUserRoleContentUnion {
	switch v := content.Value.(type) {
	case string:
		return openai.StringOrUserRoleContentUnion{Value: redactString(v)}
	case []openai.ChatCompletionContentPartUserUnionParam:
		redactedParts := make([]openai.ChatCompletionContentPartUserUnionParam, len(v))
		for i, part := range v {
			redactedParts[i] = redactUserContentPart(part)
		}
		return openai.StringOrUserRoleContentUnion{Value: redactedParts}
	default:
		return content
	}
}

// redactStringOrAssistantRoleContentUnion redacts content from a StringOrAssistantRoleContentUnion.
func redactStringOrAssistantRoleContentUnion(content openai.StringOrAssistantRoleContentUnion) openai.StringOrAssistantRoleContentUnion {
	switch v := content.Value.(type) {
	case string:
		return openai.StringOrAssistantRoleContentUnion{Value: redactString(v)}
	case []openai.ChatCompletionAssistantMessageParamContent:
		redactedParts := make([]openai.ChatCompletionAssistantMessageParamContent, len(v))
		for i, part := range v {
			redactedPart := part
			if part.Text != nil {
				redactedText := redactString(*part.Text)
				redactedPart.Text = &redactedText
			}
			if part.Refusal != nil {
				redactedRefusal := redactString(*part.Refusal)
				redactedPart.Refusal = &redactedRefusal
			}
			redactedParts[i] = redactedPart
		}
		return openai.StringOrAssistantRoleContentUnion{Value: redactedParts}
	case openai.ChatCompletionAssistantMessageParamContent:
		redactedPart := v
		if v.Text != nil {
			redactedText := redactString(*v.Text)
			redactedPart.Text = &redactedText
		}
		if v.Refusal != nil {
			redactedRefusal := redactString(*v.Refusal)
			redactedPart.Refusal = &redactedRefusal
		}
		return openai.StringOrAssistantRoleContentUnion{Value: redactedPart}
	default:
		return content
	}
}

// redactResponseFormat redacts schema information from response format while preserving the type.
// JSON schema details may contain sensitive structure information about the application's data model.
func redactResponseFormat(format *openai.ChatCompletionResponseFormatUnion) *openai.ChatCompletionResponseFormatUnion {
	redactedFormat := *format

	// Only JSON schema contains potentially sensitive schema information
	if format.OfJSONSchema != nil {
		redactedJSONSchema := *format.OfJSONSchema
		redactedInnerSchema := redactedJSONSchema.JSONSchema

		// Redact the schema name and description (may reveal internal structure)
		redactedInnerSchema.Name = redactString(redactedInnerSchema.Name)
		if redactedInnerSchema.Description != "" {
			redactedInnerSchema.Description = redactString(redactedInnerSchema.Description)
		}

		// Redact the actual JSON schema (contains sensitive data model structure)
		if len(redactedInnerSchema.Schema) > 0 {
			hash := computeContentHash(string(redactedInnerSchema.Schema))
			redactedInnerSchema.Schema = []byte(fmt.Sprintf(`{"_redacted":"REDACTED LENGTH=%d HASH=%s"}`, len(redactedInnerSchema.Schema), hash))
		}

		redactedJSONSchema.JSONSchema = redactedInnerSchema
		redactedFormat.OfJSONSchema = &redactedJSONSchema
	}

	// OfText and OfJSONObject don't contain sensitive schema information, just type indicators
	return &redactedFormat
}

// redactUserContentPart redacts sensitive content from a user message content part.
// Handles multiple content types: text, images (URLs or base64), audio, and file attachments.
func redactUserContentPart(part openai.ChatCompletionContentPartUserUnionParam) openai.ChatCompletionContentPartUserUnionParam {
	redacted := part

	// Redact plain text content
	if part.OfText != nil {
		redactedText := *part.OfText
		redactedText.Text = redactString(part.OfText.Text)
		redacted.OfText = &redactedText
	}

	// Redact image URLs (may be data URLs with embedded base64 image data)
	if part.OfImageURL != nil {
		redactedImage := *part.OfImageURL
		redactedImage.ImageURL.URL = redactString(part.OfImageURL.ImageURL.URL)
		redacted.OfImageURL = &redactedImage
	}

	// Redact audio data (typically base64-encoded audio)
	if part.OfInputAudio != nil {
		redactedAudio := *part.OfInputAudio
		redactedAudio.InputAudio.Data = redactString(part.OfInputAudio.InputAudio.Data)
		redacted.OfInputAudio = &redactedAudio
	}

	// Redact file attachments (may contain sensitive documents or data)
	if part.OfFile != nil {
		redactedFile := *part.OfFile
		if part.OfFile.File.FileData != "" {
			redactedFile.File.FileData = redactString(part.OfFile.File.FileData)
		}
		redacted.OfFile = &redactedFile
	}

	return redacted
}
