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

// EmbeddingsRecorder implements recorders for OpenInference embeddings spans.
type EmbeddingsRecorder struct {
	traceConfig *openinference.TraceConfig
}

// NewEmbeddingsRecorderFromEnv creates an api.EmbeddingsRecorder
// from environment variables using the OpenInference configuration specification.
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/embedding_spans.md
func NewEmbeddingsRecorderFromEnv() tracing.EmbeddingsRecorder {
	return NewEmbeddingsRecorder(nil)
}

// NewEmbeddingsRecorder creates a tracing.EmbeddingsRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/embedding_spans.md
func NewEmbeddingsRecorder(config *openinference.TraceConfig) tracing.EmbeddingsRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &EmbeddingsRecorder{traceConfig: config}
}

// startOptsEmbeddings sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var startOptsEmbeddings = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracing.EmbeddingsRecorder.
func (r *EmbeddingsRecorder) StartParams(*openai.EmbeddingRequest, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "CreateEmbeddings", startOptsEmbeddings
}

// RecordRequest implements the same method as defined in tracing.EmbeddingsRecorder.
func (r *EmbeddingsRecorder) RecordRequest(span trace.Span, embReq *openai.EmbeddingRequest, body []byte) {
	span.SetAttributes(buildEmbeddingsRequestAttributes(embReq, string(body), r.traceConfig)...)
}

// RecordResponseOnError implements the same method as defined in tracing.EmbeddingsRecorder.
func (r *EmbeddingsRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	recordResponseError(span, statusCode, string(body))
}

// RecordResponse implements the same method as defined in tracing.EmbeddingsRecorder.
func (r *EmbeddingsRecorder) RecordResponse(span trace.Span, resp *openai.EmbeddingResponse) {
	// Add response attributes.
	attrs := buildEmbeddingsResponseAttributes(resp, r.traceConfig)

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
