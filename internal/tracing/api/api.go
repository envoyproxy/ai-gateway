// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package api provides types for OpenTelemetry tracing support, notably to
// reduce chance of cyclic imports. No implementations besides no-op are here.
package api

import (
	"context"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// Tracing gives access to tracer types needed for endpoints such as OpenAI
// chat completions.
type Tracing interface {
	// ChatCompletionTracer creates spans for OpenAI chat completion requests on /chat/completions endpoint.
	ChatCompletionTracer() ChatCompletionTracer
	// EmbeddingsTracer creates spans for OpenAI embeddings requests on /embeddings endpoint.
	EmbeddingsTracer() EmbeddingsTracer
	// Shutdown shuts down the tracer, flushing any buffered spans.
	Shutdown(context.Context) error
}

// TracingConfig is used when Tracing is not NoopTracing.
//
// Implementations of the Tracing interface.
type TracingConfig struct {
	Tracer                 trace.Tracer
	Propagator             propagation.TextMapPropagator
	ChatCompletionRecorder ChatCompletionRecorder
	EmbeddingsRecorder     EmbeddingsRecorder
}

// NoopTracing is a Tracing that doesn't do anything.
type NoopTracing struct{}

// ChatCompletionTracer implements Tracing.ChatCompletionTracer.
func (NoopTracing) ChatCompletionTracer() ChatCompletionTracer {
	return NoopChatCompletionTracer{}
}

// EmbeddingsTracer implements Tracing.EmbeddingsTracer.
func (NoopTracing) EmbeddingsTracer() EmbeddingsTracer {
	return NoopEmbeddingsTracer{}
}

// Shutdown implements Tracing.Shutdown.
func (NoopTracing) Shutdown(context.Context) error {
	return nil
}

// ChatCompletionTracer creates spans for OpenAI chat completion requests.
type ChatCompletionTracer interface {
	// StartSpanAndInjectHeaders starts a span and injects trace context into
	// the header mutation.
	//
	// Parameters:
	//   - ctx: might include a parent span context.
	//   - headers: Incoming HTTP headers used to extract parent trace context.
	//   - headerMutation: The new LLM Span will have its context written to
	//     these headers unless NoopTracing is used.
	//   - req: The OpenAI chat completion request. Used to detect streaming
	//     and record request attributes.
	//
	// Returns nil unless the span is sampled.
	StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, headerMutation *extprocv3.HeaderMutation, req *openai.ChatCompletionRequest, body []byte) ChatCompletionSpan
}

// ChatCompletionSpan represents an OpenAI chat completion.
type ChatCompletionSpan interface {
	// RecordResponseChunk records the response chunk attributes to the span for streaming response.
	RecordResponseChunk(resp *openai.ChatCompletionResponseChunk)

	// RecordResponse records the response attributes to the span for non-streaming response.
	RecordResponse(resp *openai.ChatCompletionResponse)

	// EndSpanOnError finalizes and ends the span with an error status.
	EndSpanOnError(statusCode int, body []byte)

	// EndSpan finalizes and ends the span.
	EndSpan()
}

// ChatCompletionRecorder records attributes to a span according to a semantic
// convention.
type ChatCompletionRecorder interface {
	// StartParams returns the name and options to start the span with.
	//
	// Parameters:
	//   - req: contains the completion request
	//   - body: contains the complete request body.
	//
	// Note: Do not do any expensive data conversions as the span might not be
	// sampled.
	StartParams(req *openai.ChatCompletionRequest, body []byte) (spanName string, opts []trace.SpanStartOption)

	// RecordRequest records request attributes to the span.
	//
	// Parameters:
	//   - req: contains the completion request
	//   - body: contains the complete request body.
	RecordRequest(span trace.Span, req *openai.ChatCompletionRequest, body []byte)

	// RecordResponseChunks records response chunk attributes to the span for streaming response.
	RecordResponseChunks(span trace.Span, chunks []*openai.ChatCompletionResponseChunk)

	// RecordResponse records response attributes to the span for non-streaming response.
	RecordResponse(span trace.Span, resp *openai.ChatCompletionResponse)

	// RecordResponseOnError ends recording the span with an error status.
	RecordResponseOnError(span trace.Span, statusCode int, body []byte)
}

// NoopChatCompletionTracer is a ChatCompletionTracer that doesn't do anything.
type NoopChatCompletionTracer struct{}

// StartSpanAndInjectHeaders implements ChatCompletionTracer.StartSpanAndInjectHeaders.
func (NoopChatCompletionTracer) StartSpanAndInjectHeaders(context.Context, map[string]string, *extprocv3.HeaderMutation, *openai.ChatCompletionRequest, []byte) ChatCompletionSpan {
	return nil
}

// EmbeddingsTracer creates spans for OpenAI embeddings requests.
type EmbeddingsTracer interface {
	// StartSpanAndInjectHeaders starts a span and injects trace context into
	// the header mutation.
	//
	// Parameters:
	//   - ctx: might include a parent span context.
	//   - headers: Incoming HTTP headers used to extract parent trace context.
	//   - headerMutation: The new Embeddings Span will have its context
	//     written to these headers unless NoopTracing is used.
	//   - req: The OpenAI embeddings request. Used to record request attributes.
	//   - body: contains the original raw request body as a byte slice.
	//
	// Returns nil unless the span is sampled.
	StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, headerMutation *extprocv3.HeaderMutation, req *openai.EmbeddingRequest, body []byte) EmbeddingsSpan
}

// EmbeddingsSpan represents an OpenAI embeddings request.
type EmbeddingsSpan interface {
	// RecordResponse records the response attributes to the span.
	RecordResponse(resp *openai.EmbeddingResponse)

	// EndSpanOnError finalizes and ends the span with an error status.
	EndSpanOnError(statusCode int, body []byte)

	// EndSpan finalizes and ends the span.
	EndSpan()
}

// EmbeddingsRecorder records attributes to a span according to a semantic
// convention.
type EmbeddingsRecorder interface {
	// StartParams returns the name and options to start the span with.
	//
	// Parameters:
	//   - req: contains the embeddings request
	//   - body: contains the complete request body.
	//
	// Note: Do not do any expensive data conversions as the span might not be
	// sampled.
	StartParams(req *openai.EmbeddingRequest, body []byte) (spanName string, opts []trace.SpanStartOption)

	// RecordRequest records request attributes to the span.
	//
	// Parameters:
	//   - req: contains the embeddings request
	//   - body: contains the complete request body.
	RecordRequest(span trace.Span, req *openai.EmbeddingRequest, body []byte)

	// RecordResponse records response attributes to the span.
	RecordResponse(span trace.Span, resp *openai.EmbeddingResponse)

	// RecordResponseOnError ends recording the span with an error status.
	RecordResponseOnError(span trace.Span, statusCode int, body []byte)
}

// NoopEmbeddingsTracer is an EmbeddingsTracer that doesn't do anything.
type NoopEmbeddingsTracer struct{}

// StartSpanAndInjectHeaders implements EmbeddingsTracer.StartSpanAndInjectHeaders.
func (NoopEmbeddingsTracer) StartSpanAndInjectHeaders(context.Context, map[string]string, *extprocv3.HeaderMutation, *openai.EmbeddingRequest, []byte) EmbeddingsSpan {
	return nil
}
