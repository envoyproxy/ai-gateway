// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

// MessageRecorder implements recorders for OpenInference chat completion spans.
type MessageRecorder struct {
	traceConfig *openinference.TraceConfig
}

// NewMessageRecorderFromEnv creates an api.MessageRecorder
// from environment variables using the OpenInference configuration specification.
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewMessageRecorderFromEnv() tracing.MessageRecorder {
	return NewMessageRecorder(nil)
}

// NewMessageRecorder creates a tracing.MessageRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewMessageRecorder(config *openinference.TraceConfig) tracing.MessageRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &MessageRecorder{traceConfig: config}
}

// startOpts sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var startOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracing.MessageRecorder.
func (r *MessageRecorder) StartParams(*anthropic.MessagesRequest, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "Message", startOpts
}

// RecordRequest implements the same method as defined in tracing.MessageRecorder.
func (r *MessageRecorder) RecordRequest(span trace.Span, chatReq *anthropic.MessagesRequest, body []byte) {
	span.SetAttributes(buildRequestAttributes(chatReq, string(body), r.traceConfig)...)
}

// RecordResponseChunks implements the same method as defined in tracing.MessageRecorder.
func (r *MessageRecorder) RecordResponseChunks(span trace.Span, chunks []*anthropic.MessagesStreamEvent) {
	if len(chunks) > 0 {
		span.AddEvent("First Token Stream Event")
	}
	converted := convertSSEToJSON(chunks)
	r.RecordResponse(span, converted)
}

// RecordResponseOnError implements the same method as defined in tracing.MessageRecorder.
func (r *MessageRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

// RecordResponse implements the same method as defined in tracing.MessageRecorder.
func (r *MessageRecorder) RecordResponse(span trace.Span, resp *anthropic.MessagesResponse) {
	// Set output attributes.
	var attrs []attribute.KeyValue
	attrs = buildResponseAttributes(resp, r.traceConfig)

	bodyString := openinference.RedactedValue
	if !r.traceConfig.HideOutputs {
		marshaled, err := json.Marshal(resp)
		if err == nil {
			bodyString = string(marshaled)
		}
	}
	attrs = append(attrs, attribute.String(openinference.OutputValue, bodyString))
	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}

// llmInvocationParameters is the representation of LLMInvocationParameters,
// which includes all parameters except messages and tools, which have their
// own attributes.
// See: openinference-instrumentation-openai _request_attributes_extractor.py.
type llmInvocationParameters struct {
	anthropic.MessagesRequest
	Messages []anthropic.MessageParam `json:"messages,omitempty"`
	Tools    []anthropic.Tool         `json:"tools,omitempty"`
}

// buildRequestAttributes builds OpenInference attributes from the request.
func buildRequestAttributes(req *anthropic.MessagesRequest, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemAnthropic),
		attribute.String(openinference.LLMModelName, req.Model),
	}

	if config.HideInputs {
		attrs = append(attrs, attribute.String(openinference.InputValue, openinference.RedactedValue))
	} else {
		attrs = append(attrs, attribute.String(openinference.InputValue, body))
		attrs = append(attrs, attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON))
	}

	if !config.HideLLMInvocationParameters {
		if invocationParamsJSON, err := json.Marshal(llmInvocationParameters{
			MessagesRequest: *req,
		}); err == nil {
			attrs = append(attrs, attribute.String(openinference.LLMInvocationParameters, string(invocationParamsJSON)))
		}
	}

	// Note: compound match here is from Python OpenInference OpenAI config.py.
	if !config.HideInputs && !config.HideInputMessages {
		for i, msg := range req.Messages {
			role := msg.Role
			attrs = append(attrs, attribute.String(openinference.InputMessageAttribute(i, openinference.MessageRole), string(role)))
			switch content := msg.Content; {
			case content.Text != "":
				maybeRedacted := content.Text
				if config.HideInputText {
					maybeRedacted = openinference.RedactedValue
				}
				attrs = append(attrs, attribute.String(openinference.InputMessageAttribute(i, openinference.MessageContent), maybeRedacted))
			case content.Array != nil:
				for j, param := range content.Array {
					switch {
					case param.Text != nil:
						text := param.Text.Text
						if config.HideInputText {
							text = openinference.RedactedValue
						}
						attrs = append(attrs,
							attribute.String(openinference.InputMessageContentAttribute(i, j, "text"), text),
							attribute.String(openinference.InputMessageContentAttribute(i, j, "type"), "text"),
						)
					default:
						// TODO: skip file content for now.
					}
				}
			}
		}
	}

	// Add indexed attributes for each tool.
	for i, tool := range req.Tools {
		if toolJSON, err := json.Marshal(tool); err == nil {
			attrs = append(attrs,
				attribute.String(fmt.Sprintf("%s.%d.tool.json_schema", openinference.LLMTools, i), string(toolJSON)),
			)
		}
	}
	return attrs
}

func buildResponseAttributes(resp *anthropic.MessagesResponse, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.LLMModelName, resp.Model),
	}

	if !config.HideOutputs {
		attrs = append(attrs, attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON))
	}

	// Note: compound match here is from Python OpenInference OpenAI config.py.
	role := resp.Role
	if !config.HideOutputs && !config.HideOutputMessages {
		for i, content := range resp.Content {
			attrs = append(attrs, attribute.String(openinference.OutputMessageAttribute(i, openinference.MessageRole), string(role)))

			switch {
			case content.Text != nil:
				txt := content.Text.Text
				if config.HideOutputText {
					txt = openinference.RedactedValue
				}
				attrs = append(attrs, attribute.String(openinference.OutputMessageAttribute(i, openinference.MessageContent), txt))
			case content.Tool != nil:
				tool := content.Tool
				attrs = append(attrs,
					attribute.String(openinference.OutputMessageToolCallAttribute(i, 0, openinference.ToolCallID), tool.ID),
					attribute.String(openinference.OutputMessageToolCallAttribute(i, 0, openinference.ToolCallFunctionName), tool.Name),
				)
				inputStr, err := json.Marshal(tool.Input)
				if err == nil {
					attrs = append(attrs,
						attribute.String(openinference.OutputMessageToolCallAttribute(i, 0, openinference.ToolCallFunctionArguments), string(inputStr)),
					)
				}
			}
		}
	}

	// Token counts are considered metadata and are still included even when output content is hidden.
	u := resp.Usage
	// Calculate total input tokens as per Anthropic API documentation
	totalInputTokens := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	attrs = append(attrs,
		attribute.Int(openinference.LLMTokenCountPrompt, int(totalInputTokens)),
		attribute.Int(openinference.LLMTokenCountPromptCacheHit, int(u.CacheCreationInputTokens+u.CacheReadInputTokens)),
		attribute.Int(openinference.LLMTokenCountCompletion, int(u.OutputTokens)),
		attribute.Int(openinference.LLMTokenCountTotal, int(totalInputTokens+u.OutputTokens)),
	)
	return attrs
}

// convertSSEToJSON converts a complete SSE stream to a single JSON-encoded
// openai.ChatCompletionResponse. This will not serialize zero values including
// fields whose values are zero or empty, or nested objects where all fields
// have zero values.
//
// TODO: This can be refactored in "streaming" in stateful way without asking for all chunks at once.
// That would reduce a slice allocation for events.
// TODO Or, even better, we can make the chunk version of buildResponseAttributes which accepts a single
// openai.ChatCompletionResponseChunk one at a time, and then we won't need to accumulate all chunks
// in memory.
func convertSSEToJSON(chunks []*anthropic.MessagesStreamEvent) *anthropic.MessagesResponse {
	var (
		content      strings.Builder
		usage        *anthropic.Usage
		role         string
		obfuscation  string
		finishReason anthropic.StopReason
	)

	for _, chunk := range chunks {

		// Accumulate content, role, and annotations from delta (assuming single choice at index 0).
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta != nil {
				if chunk.Choices[0].Delta.Content != nil {
					content.WriteString(*chunk.Choices[0].Delta.Content)
				}
				if chunk.Choices[0].Delta.Role != "" {
					role = chunk.Choices[0].Delta.Role
				}
				if as := chunk.Choices[0].Delta.Annotations; as != nil && len(*as) > 0 {
					annotations = append(annotations, *as...)
				}
			}
			// Capture finish_reason from any chunk that has it.
			if chunk.Choices[0].FinishReason != "" {
				finishReason = chunk.Choices[0].FinishReason
			}
		}

		// Capture usage from the last chunk that has it.
		if chunk.Usage != nil {
			usage = chunk.Usage
		}

		// Capture obfuscation from the last chunk that has it.
		if chunk.Obfuscation != "" {
			obfuscation = chunk.Obfuscation
		}
	}

	// Build the response as a chunk with accumulated content.
	contentStr := content.String()

	// Default to "stop" if no finish reason was captured.
	if finishReason == "" {
		finishReason = openai.ChatCompletionChoicesFinishReasonStop
	}

	// Create a ChatCompletionResponse with all accumulated content.
	response := &anthropic.MessagesResponse{
		//ID:                firstChunk.ID,
		//Object:            "chat.completion.chunk", // Keep chunk object type for streaming.
		//Created:           firstChunk.Created,
		//Model:             firstChunk.Model,
		//ServiceTier:       firstChunk.ServiceTier,
		//SystemFingerprint: firstChunk.SystemFingerprint,
		//Obfuscation:       obfuscation,
		//Choices: []openai.ChatCompletionResponseChoice{{
		//	Message: openai.ChatCompletionResponseChoiceMessage{
		//		Role:        role,
		//		Content:     &contentStr,
		//		Annotations: annotationsPtr,
		//	},
		//	Index:        0,
		//	FinishReason: finishReason,
		//}},
	}

	if usage != nil {
		response.Usage = usage
	}
	return response
}
