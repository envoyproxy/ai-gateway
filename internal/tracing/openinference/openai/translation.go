// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// TranslationRecorder implements recorders for OpenInference audio translation spans.
type TranslationRecorder struct {
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewTranslationRecorderFromEnv creates a tracingapi.TranslationRecorder
// from environment variables using the OpenInference configuration specification.
func NewTranslationRecorderFromEnv() tracingapi.TranslationRecorder {
	return NewTranslationRecorder(nil)
}

// NewTranslationRecorder creates a tracingapi.TranslationRecorder with the
// given config using the OpenInference configuration specification.
func NewTranslationRecorder(config *openinference.TraceConfig) tracingapi.TranslationRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &TranslationRecorder{traceConfig: config}
}

var translationStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.TranslationRecorder.
func (r *TranslationRecorder) StartParams(*openai.TranslationRequest, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "AudioTranslation", translationStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.TranslationRecorder.
func (r *TranslationRecorder) RecordRequest(span trace.Span, req *openai.TranslationRequest, _ []byte) {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}

	if req.Model != "" {
		attrs = append(attrs, attribute.String(openinference.LLMModelName, req.Model))
	}

	if r.traceConfig.HideInputs {
		attrs = append(attrs, attribute.String(openinference.InputValue, openinference.RedactedValue))
	} else {
		attrs = append(attrs,
			attribute.String(openinference.InputValue,
				fmt.Sprintf(`{"model":"%s","file_name":"%s","file_size":%d,"response_format":"%s"}`,
					req.Model, req.FileName, req.FileSize, req.ResponseFormat)),
			attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON))
	}

	span.SetAttributes(attrs...)
}

// RecordResponse implements the same method as defined in tracingapi.TranslationRecorder.
func (r *TranslationRecorder) RecordResponse(span trace.Span, resp *openai.TranslationResponse) {
	var attrs []attribute.KeyValue

	if !r.traceConfig.HideOutputs && resp != nil {
		attrs = append(attrs,
			attribute.String(openinference.OutputValue, resp.Text),
			attribute.String(openinference.OutputMimeType, "text/plain"),
		)
	}

	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracingapi.TranslationRecorder.
func (r *TranslationRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}
