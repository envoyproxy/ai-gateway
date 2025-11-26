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
	openaisdk "github.com/openai/openai-go/v2"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

var _ Tracing = NoopTracing{}

// Tracing gives access to tracer types needed for endpoints such as OpenAI
// chat completions, image generation, embeddings, and MCP requests.
type Tracing interface {
	// ChatCompletionTracer creates spans for OpenAI chat completion requests on /chat/completions endpoint.
	ChatCompletionTracer() ChatCompletionTracer
	// ImageGenerationTracer creates spans for OpenAI image generation requests.
	ImageGenerationTracer() ImageGenerationTracer
	// CompletionTracer creates spans for OpenAI completion requests on /completions endpoint.
	CompletionTracer() CompletionTracer
	// EmbeddingsTracer creates spans for OpenAI embeddings requests on /embeddings endpoint.
	EmbeddingsTracer() EmbeddingsTracer
	// RerankTracer creates spans for rerank requests.
	RerankTracer() RerankTracer
	// MCPTracer creates spans for MCP requests.
	MCPTracer() MCPTracer
	// Shutdown shuts down the tracer, flushing any buffered spans.
	Shutdown(context.Context) error
}

// TracingConfig is used when Tracing is not NoopTracing.
//
// Implementations of the Tracing interface.
type TracingConfig struct {
	Tracer                  trace.Tracer
	Propagator              propagation.TextMapPropagator
	ChatCompletionRecorder  ChatCompletionRecorder
	CompletionRecorder      CompletionRecorder
	ImageGenerationRecorder ImageGenerationRecorder
	EmbeddingsRecorder      EmbeddingsRecorder
	RerankRecorder          RerankRecorder
}

// NoopTracing is a Tracing that doesn't do anything.
type NoopTracing struct{}

func (t NoopTracing) MCPTracer() MCPTracer {
	return NoopMCPTracer{}
}

// ChatCompletionTracer implements Tracing.ChatCompletionTracer.
func (NoopTracing) ChatCompletionTracer() ChatCompletionTracer {
	return NoopChatCompletionTracer{}
}

// CompletionTracer implements Tracing.CompletionTracer.
func (NoopTracing) CompletionTracer() CompletionTracer {
	return NoopCompletionTracer{}
}

// EmbeddingsTracer implements Tracing.EmbeddingsTracer.
func (NoopTracing) EmbeddingsTracer() EmbeddingsTracer {
	return NoopEmbeddingsTracer{}
}

// ImageGenerationTracer implements Tracing.ImageGenerationTracer.
func (NoopTracing) ImageGenerationTracer() ImageGenerationTracer {
	return NoopImageGenerationTracer{}
}

// RerankTracer implements Tracing.RerankTracer.
func (NoopTracing) RerankTracer() RerankTracer {
	return NoopRerankTracer{}
}

// Shutdown implements Tracing.Shutdown.
func (NoopTracing) Shutdown(context.Context) error {
	return nil
}

// TraceSpan captures the shared lifecycle contract for LLM tracing spans.
type TraceSpan interface {
	// EndSpanOnError finalizes and ends the span with an error status.
	EndSpanOnError(statusCode int, body []byte)

	// EndSpan finalizes and ends the span.
	EndSpan()
}

// ResponseSpan augments TraceSpan with a typed response recorder.
type ResponseSpan[RespT any] interface {
	TraceSpan

	// RecordResponse records the response attributes to the span.
	RecordResponse(resp *RespT)
}

// StreamSpan augments TraceSpan with streaming response chunk recording.
type StreamSpan[ChunkT any] interface {
	TraceSpan

	// RecordResponseChunk records the response chunk attributes to the span for streaming response.
	RecordResponseChunk(resp *ChunkT)
}

// StreamResponseSpan combines streaming chunks with a final response recorder.
type StreamResponseSpan[ChunkT any, RespT any] interface {
	StreamSpan[ChunkT]
	ResponseSpan[RespT]
}

// RequestTracer standardizes tracer implementations for non-MCP requests.
type RequestTracer[ReqT any, SpanT TraceSpan] interface {
	// StartSpanAndInjectHeaders starts a span and injects trace context into the header mutation.
	//
	// Parameters:
	//   - ctx: might include a parent span context.
	//   - headers: Incoming HTTP headers used to extract parent trace context.
	//   - headerMutation: The new span will have its context written to these headers unless NoopTracing is used.
	//   - req: The typed request used to detect streaming and record request attributes.
	//   - body: contains the original raw request body as a byte slice.
	//
	// Returns nil unless the span is sampled.
	StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, headerMutation *extprocv3.HeaderMutation, req *ReqT, body []byte) SpanT
}

// SpanRecorder standardizes recorder implementations for non-MCP tracers.
type SpanRecorder[ReqT any] interface {
	// StartParams returns the name and options to start the span with.
	//
	// Parameters:
	//   - req: contains the typed request.
	//   - body: contains the complete request body.
	//
	// Note: Avoid expensive data conversions since the span might not be sampled.
	StartParams(req *ReqT, body []byte) (spanName string, opts []trace.SpanStartOption)

	// RecordRequest records request attributes to the span.
	RecordRequest(span trace.Span, req *ReqT, body []byte)

	// RecordResponseOnError ends recording the span with an error status.
	RecordResponseOnError(span trace.Span, statusCode int, body []byte)
}

// ResponseRecorder augments SpanRecorder with a typed response recorder.
type ResponseRecorder[ReqT any, RespT any] interface {
	SpanRecorder[ReqT]

	// RecordResponse records response attributes to the span.
	RecordResponse(span trace.Span, resp *RespT)
}

// StreamRecorder augments SpanRecorder with streaming response chunk recording.
type StreamRecorder[ReqT any, ChunkT any] interface {
	SpanRecorder[ReqT]

	// RecordResponseChunks records response chunk attributes to the span for streaming response.
	RecordResponseChunks(span trace.Span, chunks []*ChunkT)
}

// StreamResponseRecorder combines streaming chunks with a final response recorder.
type StreamResponseRecorder[ReqT any, ChunkT any, RespT any] interface {
	ResponseRecorder[ReqT, RespT]
	RecordResponseChunks(span trace.Span, chunks []*ChunkT)
}

// ChatCompletionTracer creates spans for OpenAI chat completion requests.
type ChatCompletionTracer = RequestTracer[openai.ChatCompletionRequest, ChatCompletionSpan]

// ChatCompletionSpan represents an OpenAI chat completion.
type ChatCompletionSpan = StreamResponseSpan[openai.ChatCompletionResponseChunk, openai.ChatCompletionResponse]

// ChatCompletionRecorder records attributes to a span according to a semantic convention.
type ChatCompletionRecorder = StreamResponseRecorder[openai.ChatCompletionRequest, openai.ChatCompletionResponseChunk, openai.ChatCompletionResponse]

// NoopChatCompletionTracer is a ChatCompletionTracer that doesn't do anything.
type NoopChatCompletionTracer struct{}

// StartSpanAndInjectHeaders implements ChatCompletionTracer.StartSpanAndInjectHeaders.
func (NoopChatCompletionTracer) StartSpanAndInjectHeaders(context.Context, map[string]string, *extprocv3.HeaderMutation, *openai.ChatCompletionRequest, []byte) ChatCompletionSpan {
	return nil
}

// CompletionTracer creates spans for OpenAI completion requests.
type CompletionTracer = RequestTracer[openai.CompletionRequest, CompletionSpan]

// CompletionSpan represents an OpenAI completion request.
// Note: Completion streaming chunks are full CompletionResponse objects, not deltas like chat completions.
type CompletionSpan = StreamResponseSpan[openai.CompletionResponse, openai.CompletionResponse]

// CompletionRecorder records attributes to a span according to a semantic convention.
// Note: Completion streaming chunks are full CompletionResponse objects, not deltas like chat completions.
type CompletionRecorder = StreamResponseRecorder[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse]

// NoopCompletionTracer is a CompletionTracer that doesn't do anything.
type NoopCompletionTracer struct{}

// StartSpanAndInjectHeaders implements CompletionTracer.StartSpanAndInjectHeaders.
func (NoopCompletionTracer) StartSpanAndInjectHeaders(context.Context, map[string]string, *extprocv3.HeaderMutation, *openai.CompletionRequest, []byte) CompletionSpan {
	return nil
}

// EmbeddingsTracer creates spans for OpenAI embeddings requests.
type EmbeddingsTracer = RequestTracer[openai.EmbeddingRequest, EmbeddingsSpan]

// EmbeddingsSpan represents an OpenAI embeddings request.
type EmbeddingsSpan = ResponseSpan[openai.EmbeddingResponse]

// ImageGenerationTracer creates spans for OpenAI image generation requests.
type ImageGenerationTracer = RequestTracer[openaisdk.ImageGenerateParams, ImageGenerationSpan]

// ImageGenerationSpan represents an OpenAI image generation.
type ImageGenerationSpan = ResponseSpan[openaisdk.ImagesResponse]

// ImageGenerationRecorder records attributes to a span according to a semantic convention.
type ImageGenerationRecorder = ResponseRecorder[openaisdk.ImageGenerateParams, openaisdk.ImagesResponse]

// EmbeddingsRecorder records attributes to a span according to a semantic convention.
type EmbeddingsRecorder = ResponseRecorder[openai.EmbeddingRequest, openai.EmbeddingResponse]

// NoopImageGenerationTracer is a ImageGenerationTracer that doesn't do anything.
type NoopImageGenerationTracer struct{}

// StartSpanAndInjectHeaders implements ImageGenerationTracer.StartSpanAndInjectHeaders.
func (NoopImageGenerationTracer) StartSpanAndInjectHeaders(context.Context, map[string]string, *extprocv3.HeaderMutation, *openaisdk.ImageGenerateParams, []byte) ImageGenerationSpan {
	return nil
}

// NoopEmbeddingsTracer is an EmbeddingsTracer that doesn't do anything.
type NoopEmbeddingsTracer struct{}

// StartSpanAndInjectHeaders implements EmbeddingsTracer.StartSpanAndInjectHeaders.
func (NoopEmbeddingsTracer) StartSpanAndInjectHeaders(context.Context, map[string]string, *extprocv3.HeaderMutation, *openai.EmbeddingRequest, []byte) EmbeddingsSpan {
	return nil
}

// RerankTracer creates spans for rerank requests.
type RerankTracer = RequestTracer[cohere.RerankV2Request, RerankSpan]

// RerankSpan represents a rerank request span.
type RerankSpan = ResponseSpan[cohere.RerankV2Response]

// RerankRecorder records attributes to a span according to a semantic convention.
type RerankRecorder = ResponseRecorder[cohere.RerankV2Request, cohere.RerankV2Response]

// NoopRerankTracer is a RerankTracer that doesn't do anything.
type NoopRerankTracer struct{}

// StartSpanAndInjectHeaders implements RerankTracer.StartSpanAndInjectHeaders.
func (NoopRerankTracer) StartSpanAndInjectHeaders(context.Context, map[string]string, *extprocv3.HeaderMutation, *cohere.RerankV2Request, []byte) RerankSpan {
	return nil
}
