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
func NewResponsesRecorderFromEnv() tracing.ResponsesRecorder {
	return NewResponsesRecorder(nil)
}

// NewResponsesRecorder creates a tracing.ResponsesRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
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

// RecordResponseChunks implements the same method as defined in tracing.ResponsesRecorder.
// func (r *ResponsesRecorder) RecordResponseChunks(span trace.Span, chunks []*openai.ResponseChunk) {
// 	if len(chunks) > 0 {
// 		span.AddEvent("First Token Stream Event")
// 	}
// 	converted := convertSSEToJSON(chunks)
// 	r.RecordResponse(span, converted)
// }

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

// buildResponsesRequestAttributes builds OpenTelemetry attributes for responses requests.
func buildResponsesRequestAttributes(req *openai.ResponseRequest, body []byte, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}

	if req.Model != "" {
		attrs = append(attrs, attribute.String(openinference.LLMModelName, req.Model))
	}

	// Add input value
	bodyString := openinference.RedactedValue
	if !config.HideInputs {
		bodyString = string(body)
	}
	attrs = append(attrs, attribute.String(openinference.InputValue, bodyString))
	attrs = append(attrs, attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON))

	return attrs
}

// buildResponsesResponseAttributes builds OpenTelemetry attributes for responses responses.
func buildResponsesResponseAttributes(resp *openai.ResponseResponse, config *openinference.TraceConfig) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	if resp.Model != "" {
		attrs = append(attrs, attribute.String(openinference.LLMModelName, resp.Model))
	}

	// Add token usage if available
	if resp.Usage != nil {
		if resp.Usage.InputTokens > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountPrompt, resp.Usage.InputTokens))
		}
		if resp.Usage.OutputTokens > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountCompletion, resp.Usage.OutputTokens))
		}
		if resp.Usage.TotalTokens > 0 {
			attrs = append(attrs, attribute.Int(openinference.LLMTokenCountTotal, resp.Usage.TotalTokens))
		}
	}

	return attrs
}
