// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// CreateFileRecorder implements recorders for OpenInference speech synthesis spans.
type CreateFileRecorder struct {
	// Embedding NoopChunkRecorder since file uploads are not streamed
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewCreateFileRecorderFromEnv creates a tracingapi.CreateFileRecorder
// from environment variables using the OpenInference configuration specification.
func NewCreateFileRecorderFromEnv() tracingapi.CreateFileRecorder {
	return NewCreateFileRecorder(nil)
}

// NewCreateFileRecorder creates a tracingapi.CreateFileRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
func NewCreateFileRecorder(config *openinference.TraceConfig) tracingapi.CreateFileRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &CreateFileRecorder{traceConfig: config}
}

// createFileStartOpts sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var createFileStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.CreateFileRecorder.
func (r *CreateFileRecorder) StartParams(*openai.FileNewParams, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "CreateFile", createFileStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.CreateFileRecorder.
func (r *CreateFileRecorder) RecordRequest(span trace.Span, req *openai.FileNewParams, body []byte) {
	span.SetAttributes(buildCreateFileRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracingapi.CreateFileRecorder.
func (r *CreateFileRecorder) RecordResponse(span trace.Span, resp *openai.FileObject) {
	// Set output attributes.
	var attrs []attribute.KeyValue

	if !r.traceConfig.HideOutputs && resp != nil {
		attrs = append(attrs,
			attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
			attribute.String("output.file_id", resp.ID),
		)
	}

	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracingapi.CreateFileRecorder.
func (r *CreateFileRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

// buildCreateFileRequestAttributes builds OpenInference attributes from the speech request.
func buildCreateFileRequestAttributes(req *openai.FileNewParams, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}

	// if req.Model != "" {
	// 	attrs = append(attrs, attribute.String(openinference.LLMModelName, req.Model))
	// }

	if config.HideInputs {
		attrs = append(attrs, attribute.String(openinference.InputValue, openinference.RedactedValue))
	} else {
		// For speech, we want to record the text input and other parameters
		// inputJSON, err := json.Marshal(map[string]interface{}{
		// 	"purpose": req.Purpose,
		// 	"expiry": req.ExpiresAfter.
		// })
		// if err == nil {
			attrs = append(attrs,
				attribute.String(openinference.InputValue, body),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			)
		// }
	}

	if !config.HideLLMInvocationParameters {
		attrs = append(attrs, attribute.String(openinference.LLMInvocationParameters, body))
	}

	return attrs
}
