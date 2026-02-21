// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package openai provides OpenInference semantic conventions hooks for
// OpenAI instrumentation used by the ExtProc router filter.
package openai

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// ImageEditRecorder implements recorders for OpenInference image edit spans.
type ImageEditRecorder struct {
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewImageEditRecorderFromEnv creates an tracingapi.ImageEditRecorder
// from environment variables using the OpenInference configuration specification.
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewImageEditRecorderFromEnv() tracingapi.ImageEditRecorder {
	return NewImageEditRecorder(nil)
}

// NewImageEditRecorder creates a tracingapi.ImageEditRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewImageEditRecorder(config *openinference.TraceConfig) tracingapi.ImageEditRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &ImageEditRecorder{traceConfig: config}
}

// startOpts sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var imageEditStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.ImageEditRecorder.
func (r *ImageEditRecorder) StartParams(*openai.ImageEditRequest, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "ImagesEditResponse", imageEditStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.ImageEditRecorder.
func (r *ImageEditRecorder) RecordRequest(span trace.Span, req *openai.ImageEditRequest, body []byte) {
	span.SetAttributes(buildImageEditRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracingapi.ImageEditRecorder.
func (r *ImageEditRecorder) RecordResponse(span trace.Span, resp *openai.ImageEditResponse) {
	// Set output attributes.
	var attrs []attribute.KeyValue
	bodyString := openinference.RedactedValue
	if !r.traceConfig.HideOutputs {
		marshaled, err := json.Marshal(resp)
		if err == nil {
			bodyString = string(marshaled)
		}
	}
	// Match ChatCompletion recorder: include output MIME type and value
	attrs = append(attrs,
		attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
		attribute.String(openinference.OutputValue, bodyString),
	)
	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracingapi.ImageEditRecorder.
func (r *ImageEditRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

// buildImageEditRequestAttributes builds OpenInference attributes from the image edit request.
func buildImageEditRequestAttributes(_ *openai.ImageEditRequest, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}

	if config.HideInputs {
		attrs = append(attrs, attribute.String(openinference.InputValue, openinference.RedactedValue))
	} else {
		attrs = append(attrs,
			attribute.String(openinference.InputValue, body),
			attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
		)
	}

	return attrs
}
