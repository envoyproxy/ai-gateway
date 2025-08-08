// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openinference

import (
	"bytes"
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// ChatCompletionRecorder implements recorders for OpenInference chat completion spans.
type ChatCompletionRecorder struct{}

// startOpts sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var startOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracing.ChatCompletionRecorder.
func (ChatCompletionRecorder) StartParams(*openai.ChatCompletionRequest, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "ChatCompletion", startOpts
}

// RecordRequest implements the same method as defined in tracing.ChatCompletionRecorder.
func (ChatCompletionRecorder) RecordRequest(span trace.Span, chatReq *openai.ChatCompletionRequest, body []byte) {
	span.SetAttributes(buildRequestAttributes(chatReq, string(body))...)
}

// RecordChunk implements the same method as defined in tracing.ChatCompletionRecorder.
func (ChatCompletionRecorder) RecordChunk(trace.Span, int) {
	// No-op for non-streaming responses.
}

// RecordResponse implements the same method as defined in tracing.ChatCompletionRecorder.
func (ChatCompletionRecorder) RecordResponse(span trace.Span, statusCode int, body []byte) {
	bodyString := string(body)
	if statusCode < 200 || statusCode >= 300 {
		recordResponseError(span, statusCode, bodyString)
		return
	}

	// Set output attributes.
	var attrs []attribute.KeyValue
	// Attempt to parse response for additional attributes.
	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(body, &resp); err == nil {
		attrs = buildResponseAttributes(&resp)
	}
	// Always record the body even if it is not JSON.
	span.SetAttributes(append(attrs, attribute.String(OutputValue, bodyString))...)
	span.SetStatus(codes.Ok, "")
}

// ChatCompletionStreamingRecorder extends ChatCompletionRecorder for streaming
// responses.
type ChatCompletionStreamingRecorder struct {
	ChatCompletionRecorder
}

// RecordChunk implements the same method as defined in tracing.ChatCompletionRecorder.
func (ChatCompletionStreamingRecorder) RecordChunk(span trace.Span, chunkIdx int) {
	if chunkIdx == 0 {
		span.AddEvent("First Token Stream Event")
	}
}

// RecordResponse implements the same method as defined in tracing.ChatCompletionRecorder.
// It converts SSE to JSON before calling the base implementation.
func (ChatCompletionStreamingRecorder) RecordResponse(span trace.Span, statusCode int, body []byte) {
	// Only convert if the body looks like SSE data (contains "data: " prefix).
	if bytes.Contains(body, []byte("data: ")) {
		// Convert SSE to a single completion response.
		if jsonBody, convErr := convertSSEToJSON(body); convErr == nil && len(jsonBody) > 0 {
			body = jsonBody
		}
	}

	// Call the base implementation with the (possibly converted) body.
	ChatCompletionRecorder{}.RecordResponse(span, statusCode, body)
}
