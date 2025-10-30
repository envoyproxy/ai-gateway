// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

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

// ResponsesRecorder implements recorders for OpenInference responses spans.
type ResponsesRecorder struct {
	traceConfig *openinference.TraceConfig
}

// NewResponsesRecorderFromEnv creates an api.ResponsesRecorder
// from environment variables using the OpenInference configuration specification.
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewResponsesRecorderFromEnv() tracing.ResponsesRecorder {
	return NewResponsesRecorder(nil)
}

// NewResponsesRecorder creates a tracing.ResponsesRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewResponsesRecorder(config *openinference.TraceConfig) tracing.ResponsesRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &ResponsesRecorder{traceConfig: config}
}

// startOptsResponses sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var startOptsResponses = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracing.ResponsesRecorder.
func (r *ResponsesRecorder) StartParams(*openai.ResponseRequest, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "CreateResponse", startOptsResponses
}

// RecordRequest implements the same method as defined in tracing.ResponsesRecorder.
func (r *ResponsesRecorder) RecordRequest(span trace.Span, req *openai.ResponseRequest, body []byte) {
	attrs := buildResponsesRequestAttributes(req, body, r.traceConfig)
	span.SetAttributes(attrs...)
}

// RecordResponseChunk implements the same method as defined in tracing.ResponsesRecorder.
func (r *ResponsesRecorder) RecordResponseChunk(span trace.Span, chunk *openai.ResponseCompletedEvent) {
	if chunk != nil {
		span.AddEvent("Response Completed Event")
		r.RecordResponse(span, &chunk.Response)
	}
}

// RecordResponseOnError implements the same method as defined in tracing.ResponsesRecorder.
func (r *ResponsesRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	recordResponseError(span, statusCode, string(body))
}

// RecordResponse implements the same method as defined in tracing.ResponsesRecorder.
func (r *ResponsesRecorder) RecordResponse(span trace.Span, resp *openai.ResponseResponse) {
	// Add response attributes.
	attrs := buildResponsesResponseAttributes(resp, r.traceConfig)

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
