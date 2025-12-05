// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
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
func (r *MessageRecorder) RecordResponseChunks(span trace.Span, chunks []*anthropic.MessagesStreamChunk) {
	if len(chunks) > 0 {
		span.AddEvent("First Token Stream Event")
	}
	converted := convertSSEToResponse(chunks)
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
						// TODO: support for other content types.
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

// convertSSEToResponse converts a complete SSE stream to a single JSON-encoded
// openai.ChatCompletionResponse. This will not serialize zero values including
// fields whose values are zero or empty, or nested objects where all fields
// have zero values.
//
// TODO: This can be refactored in "streaming" in stateful way without asking for all chunks at once.
// That would reduce a slice allocation for events.
// TODO Or, even better, we can make the chunk version of buildResponseAttributes which accepts a single
// openai.ChatCompletionResponseChunk one at a time, and then we won't need to accumulate all chunks
// in memory.
func convertSSEToResponse(chunks []*anthropic.MessagesStreamChunk) *anthropic.MessagesResponse {
	var response anthropic.MessagesResponse
	toolInputs := make(map[int]string)

	for _, event := range chunks {
		switch {
		case event.MessageStart != nil:
			response = *(*anthropic.MessagesResponse)(event.MessageStart)
			// Ensure Content is initialized if nil.
			if response.Content == nil {
				response.Content = []anthropic.MessagesContentBlock{}
			}

		case event.MessageDelta != nil:
			delta := event.MessageDelta
			if response.Usage == nil {
				response.Usage = &delta.Usage
			} else {
				// Usage is cumulative for output tokens in message_delta.
				// Input tokens are usually in message_start.
				response.Usage.OutputTokens = delta.Usage.OutputTokens
			}
			response.StopReason = &delta.Delta.StopReason
			response.StopSequence = &delta.Delta.StopSequence

		case event.ContentBlockStart != nil:
			idx := event.ContentBlockStart.Index
			// Grow slice if needed.
			if idx >= len(response.Content) {
				newContent := make([]anthropic.MessagesContentBlock, idx+1)
				copy(newContent, response.Content)
				response.Content = newContent
			}
			response.Content[idx] = event.ContentBlockStart.ContentBlock

		case event.ContentBlockDelta != nil:
			idx := event.ContentBlockDelta.Index
			if idx < len(response.Content) {
				block := &response.Content[idx]
				delta := event.ContentBlockDelta.Delta

				if block.Text != nil && delta.Text != "" {
					block.Text.Text += delta.Text
				}
				if block.Tool != nil && delta.PartialJSON != "" {
					toolInputs[idx] += delta.PartialJSON
				}
				if block.Thinking != nil {
					if delta.Thinking != "" {
						block.Thinking.Thinking += delta.Thinking
					}
					if delta.Signature != "" {
						block.Thinking.Signature = delta.Signature
					}
				}
			}

		case event.ContentBlockStop != nil:
			idx := event.ContentBlockStop.Index
			if jsonStr, ok := toolInputs[idx]; ok {
				if idx < len(response.Content) && response.Content[idx].Tool != nil {
					var input map[string]any
					if err := json.Unmarshal([]byte(jsonStr), &input); err == nil {
						response.Content[idx].Tool.Input = input
					}
				}
				delete(toolInputs, idx)
			}

		case event.MessageStop != nil:
			// Nothing to do.
		}
	}
	return &response
}
