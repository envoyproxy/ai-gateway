// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package openai provides OpenInference semantic conventions hooks for
// OpenAI instrumentation used by the ExtProc router filter.
package openai

import (
	"bytes"
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

// RecordChunk implements the same method as defined in tracing.ChatCompletionRecorder.
func (r *ChatCompletionRecorder) RecordChunk(span trace.Span, chunkIdx int) {
	if chunkIdx == 0 {
		span.AddEvent("First Token Stream Event")
	}
}

// RecordResponse implements the same method as defined in tracing.ChatCompletionRecorder.
func (r *ChatCompletionRecorder) RecordResponse(span trace.Span, statusCode int, body []byte) {
	if statusCode < 200 || statusCode >= 300 {
		recordResponseError(span, statusCode, string(body))
		return
	}

	// Only convert if the body looks like SSE data (contains "data: " prefix).
	if bytes.Contains(body, []byte("data: ")) {
		// Convert SSE to a single completion response.
		if jsonBody, convErr := convertSSEToJSON(body); convErr == nil && len(jsonBody) > 0 {
			body = jsonBody
		}
	}

	// Set output attributes.
	var attrs []attribute.KeyValue
	// Attempt to parse response for additional attributes.
	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(body, &resp); err == nil {
		attrs = buildResponseAttributes(&resp, r.traceConfig)
	}

	bodyString := string(body) // Use the potentially converted body.
	if r.traceConfig.HideOutputs {
		bodyString = openinference.RedactedValue
	}
	attrs = append(attrs, attribute.String(openinference.OutputValue, bodyString))

	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}
