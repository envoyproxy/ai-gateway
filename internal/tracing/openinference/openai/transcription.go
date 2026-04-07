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

// TranscriptionRecorder implements recorders for OpenInference audio transcription spans.
type TranscriptionRecorder struct {
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewTranscriptionRecorderFromEnv creates a tracingapi.TranscriptionRecorder
// from environment variables using the OpenInference configuration specification.
func NewTranscriptionRecorderFromEnv() tracingapi.TranscriptionRecorder {
	return NewTranscriptionRecorder(nil)
}

// NewTranscriptionRecorder creates a tracingapi.TranscriptionRecorder with the
// given config using the OpenInference configuration specification.
func NewTranscriptionRecorder(config *openinference.TraceConfig) tracingapi.TranscriptionRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &TranscriptionRecorder{traceConfig: config}
}

var transcriptionStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.TranscriptionRecorder.
func (r *TranscriptionRecorder) StartParams(*openai.TranscriptionRequest, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "AudioTranscription", transcriptionStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.TranscriptionRecorder.
func (r *TranscriptionRecorder) RecordRequest(span trace.Span, req *openai.TranscriptionRequest, _ []byte) {
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
				fmt.Sprintf(`{"model":"%s","file_name":"%s","file_size":%d,"language":"%s","response_format":"%s"}`,
					req.Model, req.FileName, req.FileSize, req.Language, req.ResponseFormat)),
			attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON))
	}

	span.SetAttributes(attrs...)
}

// RecordResponse implements the same method as defined in tracingapi.TranscriptionRecorder.
func (r *TranscriptionRecorder) RecordResponse(span trace.Span, resp *openai.TranscriptionResponse) {
	var attrs []attribute.KeyValue

	if !r.traceConfig.HideOutputs && resp != nil {
		attrs = append(attrs,
			attribute.String(openinference.OutputValue, resp.Text),
			attribute.String(openinference.OutputMimeType, "text/plain"),
		)
		if resp.Duration > 0 {
			attrs = append(attrs, attribute.Float64("output.audio_duration", resp.Duration))
		}
		if resp.Language != "" {
			attrs = append(attrs, attribute.String("output.language", resp.Language))
		}
	}

	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracingapi.TranscriptionRecorder.
func (r *TranscriptionRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}
