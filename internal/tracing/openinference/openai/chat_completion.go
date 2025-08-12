// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package openai provides OpenInference semantic conventions hooks for
// OpenAI instrumentation used by the ExtProc router filter.
package openai

import (
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

// ChatCompletionRecorder implements recorders for OpenInference chat completion spans.
type ChatCompletionRecorder struct {
	traceConfig *openinference.TraceConfig
}

// NewChatCompletionRecorderFromEnv creates an api.ChatCompletionRecorder
// from environment variables using the OpenInference configuration specification.
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewChatCompletionRecorderFromEnv() tracing.ChatCompletionRecorder {
	return NewChatCompletionRecorder(nil)
}

// NewChatCompletionRecorder creates a tracing.ChatCompletionRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewChatCompletionRecorder(config *openinference.TraceConfig) tracing.ChatCompletionRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &ChatCompletionRecorder{traceConfig: config}
}

// startOpts sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var startOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracing.ChatCompletionRecorder.
func (r *ChatCompletionRecorder) StartParams(*openai.ChatCompletionRequest, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "ChatCompletion", startOpts
}

// RecordRequest implements the same method as defined in tracing.ChatCompletionRecorder.
func (r *ChatCompletionRecorder) RecordRequest(span trace.Span, chatReq *openai.ChatCompletionRequest, body []byte) {
	span.SetAttributes(buildRequestAttributes(chatReq, string(body), r.traceConfig)...)
}

// RecordResponseChunk implements the same method as defined in tracing.ChatCompletionRecorder.
func (r *ChatCompletionRecorder) RecordResponseChunk(span trace.Span, chunk *openai.ChatCompletionResponseChunk, chunkIdx int) {
	if chunkIdx == 0 {
		span.AddEvent("First Token Stream Event")
	}
	// Set output attributes.
	var attrs []attribute.KeyValue
	attrs = buildResponseAttributesForChunk(chunk, r.traceConfig)

	// TODO: what to do with the "output" attribute for streaming? Accumulate it? Right now the last chunk
	// 	will overwrite the previous ones.
	bodyString := openinference.RedactedValue
	if !r.traceConfig.HideOutputs {
		marshaled, err := json.Marshal(chunk)
		if err == nil {
			bodyString = string(marshaled)
		}
	}
	attrs = append(attrs, attribute.String(openinference.OutputValue, bodyString))
	span.SetAttributes(attrs...)
}

// RecordResponseOnError implements the same method as defined in tracing.ChatCompletionRecorder.
func (r *ChatCompletionRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	recordResponseError(span, statusCode, string(body))
}

// RecordResponse implements the same method as defined in tracing.ChatCompletionRecorder.
func (r *ChatCompletionRecorder) RecordResponse(span trace.Span, resp *openai.ChatCompletionResponse) {
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
}

// RecordResponseOnOK implements the same method as defined in tracing.ChatCompletionRecorder.
func (r *ChatCompletionRecorder) RecordResponseOnOK(span trace.Span) {
	span.SetStatus(codes.Ok, "")
}
