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

// CreateFileRecorder implements recorders for OpenInference create file spans.
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

// RetrieveFileRecorder implements recorders for OpenInference retrieve file spans.
type RetrieveFileRecorder struct {
	// Embedding NoopChunkRecorder since file uploads are not streamed
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewRetrieveFileRecorderFromEnv creates a tracingapi.RetrieveFileRecorder
// from environment variables using the OpenInference configuration specification.
func NewRetrieveFileRecorderFromEnv() tracingapi.RetrieveFileRecorder {
	return NewRetrieveFileRecorder(nil)
}

// NewRetrieveFileRecorder creates a tracingapi.RetrieveFileRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
func NewRetrieveFileRecorder(config *openinference.TraceConfig) tracingapi.RetrieveFileRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &RetrieveFileRecorder{traceConfig: config}
}

// retrieveFileStartOpts sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var retrieveFileStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.RetrieveFileRecorder.
func (r *RetrieveFileRecorder) StartParams(*struct{}, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "CreateFile", createFileStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.RetrieveFileRecorder.
func (r *RetrieveFileRecorder) RecordRequest(span trace.Span, req *struct{}, body []byte) {
	span.SetAttributes(buildRetrieveFileRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracingapi.RetrieveFileRecorder.
func (r *RetrieveFileRecorder) RecordResponse(span trace.Span, resp *openai.FileObject) {
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

// RecordResponseOnError implements the same method as defined in tracingapi.RetrieveFileRecorder.
func (r *RetrieveFileRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

// buildRetrieveFileRequestAttributes builds OpenInference attributes from the speech request.
func buildRetrieveFileRequestAttributes(req *struct{}, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}

	return attrs
}

// RetrieveFileContentRecorder implements recorders for OpenInference retrieve file content spans.
type RetrieveFileContentRecorder struct {
	// Embedding NoopChunkRecorder since file uploads are not streamed
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewRetrieveFileContentRecorderFromEnv creates a tracingapi.RetrieveFileContentRecorder
// from environment variables using the OpenInference configuration specification.
func NewRetrieveFileContentRecorderFromEnv() tracingapi.RetrieveFileContentRecorder {
	return NewRetrieveFileContentRecorder(nil)
}

// NewRetrieveFileContentRecorder creates a tracingapi.RetrieveFileContentRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
func NewRetrieveFileContentRecorder(config *openinference.TraceConfig) tracingapi.RetrieveFileContentRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &RetrieveFileContentRecorder{traceConfig: config}
}

// retrieveFileContentStartOpts sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var retrieveFileContentStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.RetrieveFileContentRecorder.
func (r *RetrieveFileContentRecorder) StartParams(*struct{}, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "CreateFile", createFileStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.RetrieveFileContentRecorder.
func (r *RetrieveFileContentRecorder) RecordRequest(span trace.Span, req *struct{}, body []byte) {
	span.SetAttributes(buildRetrieveFileContentRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracingapi.RetrieveFileContentRecorder.
func (r *RetrieveFileContentRecorder) RecordResponse(span trace.Span, resp *struct{}) {
	var attrs []attribute.KeyValue

	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracingapi.RetrieveFileContentRecorder.
func (r *RetrieveFileContentRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

// buildRetrieveFileContentRequestAttributes builds OpenInference attributes from the retrieve file content request.
func buildRetrieveFileContentRequestAttributes(req *struct{}, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}

	return attrs
}

// DeleteFileRecorder implements recorders for OpenInference delete file spans.
type DeleteFileRecorder struct {
	// Embedding NoopChunkRecorder since file uploads are not streamed
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewDeleteFileRecorderFromEnv creates a tracingapi.DeleteFileRecorder
// from environment variables using the OpenInference configuration specification.
func NewDeleteFileRecorderFromEnv() tracingapi.DeleteFileRecorder {
	return NewDeleteFileRecorder(nil)
}

// NewDeleteFileRecorder creates a tracingapi.DeleteFileRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
func NewDeleteFileRecorder(config *openinference.TraceConfig) tracingapi.DeleteFileRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &DeleteFileRecorder{traceConfig: config}
}

// deleteFileStartOpts sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var deleteFileStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.DeleteFileRecorder.
func (r *DeleteFileRecorder) StartParams(*struct{}, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "DeleteFile", deleteFileStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.DeleteFileRecorder.
func (r *DeleteFileRecorder) RecordRequest(span trace.Span, req *struct{}, body []byte) {
	span.SetAttributes(buildDeleteFileRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracingapi.DeleteFileRecorder.
func (r *DeleteFileRecorder) RecordResponse(span trace.Span, resp *openai.FileDeleted) {
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

// RecordResponseOnError implements the same method as defined in tracingapi.DeleteFileRecorder.
func (r *DeleteFileRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

// buildDeleteFileRequestAttributes builds OpenInference attributes from the delete file request.
func buildDeleteFileRequestAttributes(req *struct{}, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}

	return attrs
}
