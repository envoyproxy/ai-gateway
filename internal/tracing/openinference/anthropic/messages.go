// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"encoding/json"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
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
