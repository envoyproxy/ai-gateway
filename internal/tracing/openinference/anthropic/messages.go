// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"encoding/json"
	"fmt"

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
func (r *MessageRecorder) RecordResponseChunks(span trace.Span, chunks []*anthropic.MessagesStreamEventMessageDelta) {
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
	Messages []openai.ChatCompletionMessageParamUnion `json:"messages,omitempty"`
	Tools    []openai.Tool                            `json:"tools,omitempty"`
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
			attrs = append(attrs, attribute.String(openinference.InputMessageAttribute(i, openinference.MessageRole), role))
			switch content := msg.Content; {
			case content.Text != "":
				if config.HideInputText {
					content = openinference.RedactedValue
				}
				attrs = append(attrs, attribute.String(openinference.InputMessageAttribute(i, openinference.MessageContent), content))
			case content.Array != nil:
				for j, part := range content {
					switch {
					case part.OfText != nil:
						text := part.OfText.Text
						if config.HideInputText {
							text = openinference.RedactedValue
						}
						attrs = append(attrs,
							attribute.String(openinference.InputMessageContentAttribute(i, j, "text"), text),
							attribute.String(openinference.InputMessageContentAttribute(i, j, "type"), "text"),
						)
					case part.OfImageURL != nil && part.OfImageURL.ImageURL.URL != "":
						if !config.HideInputImages {
							urlKey := openinference.InputMessageContentAttribute(i, j, "image.image.url")
							typeKey := openinference.InputMessageContentAttribute(i, j, "type")
							url := part.OfImageURL.ImageURL.URL
							if isBase64URL(url) && len(url) > config.Base64ImageMaxLength {
								url = openinference.RedactedValue
							}
							attrs = append(attrs,
								attribute.String(urlKey, url),
								attribute.String(typeKey, "image"),
							)
						}
					case part.OfInputAudio != nil:
						// Skip recording audio content attributes to match Python OpenInference behavior.
						// Audio data is already included in input.value as part of the full request.
					case part.OfFile != nil:
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
