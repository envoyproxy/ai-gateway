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

// CreateBatchRecorder implements recorders for OpenInference create batch spans.
type CreateBatchRecorder struct {
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewCreateBatchRecorderFromEnv creates a tracingapi.CreateBatchRecorder
// from environment variables using the OpenInference configuration specification.
func NewCreateBatchRecorderFromEnv() tracingapi.CreateBatchRecorder {
	return NewCreateBatchRecorder(nil)
}

// NewCreateBatchRecorder creates a tracingapi.CreateBatchRecorder with the
// given config using the OpenInference configuration specification.
func NewCreateBatchRecorder(config *openinference.TraceConfig) tracingapi.CreateBatchRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &CreateBatchRecorder{traceConfig: config}
}

var createBatchStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.CreateBatchRecorder.
func (r *CreateBatchRecorder) StartParams(*openai.BatchNewParams, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "CreateBatch", createBatchStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.CreateBatchRecorder.
func (r *CreateBatchRecorder) RecordRequest(span trace.Span, req *openai.BatchNewParams, body []byte) {
	span.SetAttributes(buildCreateBatchRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracingapi.CreateBatchRecorder.
func (r *CreateBatchRecorder) RecordResponse(span trace.Span, _ *openai.Batch) {
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracingapi.CreateBatchRecorder.
func (r *CreateBatchRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

func buildCreateBatchRequestAttributes(_ *openai.BatchNewParams, _ string, _ *openinference.TraceConfig) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}
}

// ListBatchesRecorder implements recorders for OpenInference list batches spans.
type ListBatchesRecorder struct {
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewListBatchesRecorderFromEnv creates a tracingapi.ListBatchesRecorder
// from environment variables using the OpenInference configuration specification.
func NewListBatchesRecorderFromEnv() tracingapi.ListBatchesRecorder {
	return NewListBatchesRecorder(nil)
}

// NewListBatchesRecorder creates a tracingapi.ListBatchesRecorder with the
// given config using the OpenInference configuration specification.
func NewListBatchesRecorder(config *openinference.TraceConfig) tracingapi.ListBatchesRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &ListBatchesRecorder{traceConfig: config}
}

var listBatchesStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.ListBatchesRecorder.
func (r *ListBatchesRecorder) StartParams(*struct{}, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "ListBatches", listBatchesStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.ListBatchesRecorder.
func (r *ListBatchesRecorder) RecordRequest(span trace.Span, req *struct{}, body []byte) {
	span.SetAttributes(buildListBatchesRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracingapi.ListBatchesRecorder.
func (r *ListBatchesRecorder) RecordResponse(span trace.Span, _ *struct{}) {
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracingapi.ListBatchesRecorder.
func (r *ListBatchesRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

func buildListBatchesRequestAttributes(_ *struct{}, _ string, _ *openinference.TraceConfig) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}
}

// RetrieveBatchRecorder implements recorders for OpenInference retrieve batch spans.
type RetrieveBatchRecorder struct {
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewRetrieveBatchRecorderFromEnv creates a tracingapi.RetrieveBatchRecorder
// from environment variables using the OpenInference configuration specification.
func NewRetrieveBatchRecorderFromEnv() tracingapi.RetrieveBatchRecorder {
	return NewRetrieveBatchRecorder(nil)
}

// NewRetrieveBatchRecorder creates a tracingapi.RetrieveBatchRecorder with the
// given config using the OpenInference configuration specification.
func NewRetrieveBatchRecorder(config *openinference.TraceConfig) tracingapi.RetrieveBatchRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &RetrieveBatchRecorder{traceConfig: config}
}

var retrieveBatchStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.RetrieveBatchRecorder.
func (r *RetrieveBatchRecorder) StartParams(*struct{}, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "RetrieveBatch", retrieveBatchStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.RetrieveBatchRecorder.
func (r *RetrieveBatchRecorder) RecordRequest(span trace.Span, req *struct{}, body []byte) {
	span.SetAttributes(buildRetrieveBatchRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracingapi.RetrieveBatchRecorder.
func (r *RetrieveBatchRecorder) RecordResponse(span trace.Span, _ *openai.Batch) {
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracingapi.RetrieveBatchRecorder.
func (r *RetrieveBatchRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

func buildRetrieveBatchRequestAttributes(_ *struct{}, _ string, _ *openinference.TraceConfig) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}
}

// CancelBatchRecorder implements recorders for OpenInference cancel batch spans.
type CancelBatchRecorder struct {
	tracingapi.NoopChunkRecorder[struct{}]
	traceConfig *openinference.TraceConfig
}

// NewCancelBatchRecorderFromEnv creates a tracingapi.CancelBatchRecorder
// from environment variables using the OpenInference configuration specification.
func NewCancelBatchRecorderFromEnv() tracingapi.CancelBatchRecorder {
	return NewCancelBatchRecorder(nil)
}

// NewCancelBatchRecorder creates a tracingapi.CancelBatchRecorder with the
// given config using the OpenInference configuration specification.
func NewCancelBatchRecorder(config *openinference.TraceConfig) tracingapi.CancelBatchRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &CancelBatchRecorder{traceConfig: config}
}

var cancelBatchStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracingapi.CancelBatchRecorder.
func (r *CancelBatchRecorder) StartParams(*struct{}, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "CancelBatch", cancelBatchStartOpts
}

// RecordRequest implements the same method as defined in tracingapi.CancelBatchRecorder.
func (r *CancelBatchRecorder) RecordRequest(span trace.Span, req *struct{}, body []byte) {
	span.SetAttributes(buildCancelBatchRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracingapi.CancelBatchRecorder.
func (r *CancelBatchRecorder) RecordResponse(span trace.Span, _ *openai.Batch) {
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracingapi.CancelBatchRecorder.
func (r *CancelBatchRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

func buildCancelBatchRequestAttributes(_ *struct{}, _ string, _ *openinference.TraceConfig) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}
}
