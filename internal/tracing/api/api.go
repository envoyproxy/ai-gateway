// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package api provides types for OpenTelemetry tracing support, notably to
// reduce chance of cyclic imports. No implementations besides no-op are here.
package api

import (
	"context"

	openaisdk "github.com/openai/openai-go/v2"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

type (
	// Tracing gives access to tracer types needed for endpoints such as OpenAI
	// chat completions, image generation, embeddings, and MCP requests.
	Tracing interface {
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
		// MessageTracer creates spans for Anthropic messages requests.
		MessageTracer() MessageTracer
		// MCPTracer creates spans for MCP requests.
		MCPTracer() MCPTracer
		// Shutdown shuts down the tracer, flushing any buffered spans.
		Shutdown(context.Context) error
	}
	// RequestTracer standardizes tracer implementations for non-MCP requests.
	RequestTracer[ReqT any, RespT any, RespChunkT any] interface {
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
		StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, carrier propagation.TextMapCarrier, req *ReqT, body []byte) Span[RespT, RespChunkT]
	}
	// ChatCompletionTracer creates spans for OpenAI chat completion requests.
	ChatCompletionTracer = RequestTracer[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]
	// CompletionTracer creates spans for OpenAI completion requests.
	CompletionTracer = RequestTracer[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse]
	// EmbeddingsTracer creates spans for OpenAI embeddings requests.
	EmbeddingsTracer = RequestTracer[openai.EmbeddingRequest, openai.EmbeddingResponse, struct{}]
	// ImageGenerationTracer creates spans for OpenAI image generation requests.
	ImageGenerationTracer = RequestTracer[openaisdk.ImageGenerateParams, openaisdk.ImagesResponse, struct{}]
	// RerankTracer creates spans for rerank requests.
	RerankTracer = RequestTracer[cohere.RerankV2Request, cohere.RerankV2Response, struct{}]
	// MessageTracer creates spans for Anthropic messages requests.
	MessageTracer = RequestTracer[anthropicschema.MessagesRequest, anthropicschema.MessagesResponse, anthropicschema.MessagesStreamChunk]
)

type (
	// Span standardizes span interfaces, supporting both streaming and non-streaming endpoints.
	Span[RespT any, RespChunkT any] interface {
		// RecordResponseChunk records streaming response chunks. Implementations that do not support streaming should provide a no-op implementation.
		RecordResponseChunk(resp *RespChunkT)
		// RecordResponse records the response attributes to the span.
		RecordResponse(resp *RespT)
		// EndSpanOnError finalizes and ends the span with an error status.
		EndSpanOnError(statusCode int, body []byte)
		// EndSpan finalizes and ends the span.
		EndSpan()
	}
	// ChatCompletionSpan represents an OpenAI chat completion.
	ChatCompletionSpan = Span[openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]
	// CompletionSpan represents an OpenAI completion request.
	// Note: Completion streaming chunks are full CompletionResponse objects, not deltas like chat completions.
	CompletionSpan = Span[openai.CompletionResponse, openai.CompletionResponse]
	// EmbeddingsSpan represents an OpenAI embeddings request. The chunk type is unused and therefore set to struct{}.
	EmbeddingsSpan = Span[openai.EmbeddingResponse, struct{}]
	// ImageGenerationSpan represents an OpenAI image generation.
	ImageGenerationSpan = Span[openaisdk.ImagesResponse, struct{}]
	// RerankSpan represents a rerank request span.
	RerankSpan = Span[cohere.RerankV2Response, struct{}]
	// MessageSpan represents an Anthropic messages request span.
	MessageSpan = Span[anthropicschema.MessagesResponse, anthropicschema.MessagesStreamChunk]
)

type (
	// SpanRecorder standardizes recorder implementations for non-MCP tracers.
	SpanRecorder[ReqT any, RespT any, RespChunkT any] interface {
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
		// RecordResponse records response attributes to the span.
		RecordResponse(span trace.Span, resp *RespT)
		// RecordResponseOnError ends recording the span with an error status.
		RecordResponseOnError(span trace.Span, statusCode int, body []byte)
		// RecordResponseChunks records response chunk attributes to the span for streaming response.
		RecordResponseChunks(span trace.Span, chunks []*RespChunkT)
	}
	// ChatCompletionRecorder records attributes to a span according to a semantic convention.
	ChatCompletionRecorder = SpanRecorder[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]
	// CompletionRecorder records attributes to a span according to a semantic convention.
	// Note: Completion streaming chunks are full CompletionResponse objects, not deltas like chat completions.
	CompletionRecorder = SpanRecorder[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse]
	// ImageGenerationRecorder records attributes to a span according to a semantic convention.
	ImageGenerationRecorder = SpanRecorder[openaisdk.ImageGenerateParams, openaisdk.ImagesResponse, struct{}]
	// EmbeddingsRecorder records attributes to a span according to a semantic convention.
	EmbeddingsRecorder = SpanRecorder[openai.EmbeddingRequest, openai.EmbeddingResponse, struct{}]
	// RerankRecorder records attributes to a span according to a semantic convention.
	RerankRecorder = SpanRecorder[cohere.RerankV2Request, cohere.RerankV2Response, struct{}]
	// MessageRecorder records attributes to a span according to a semantic convention.
	MessageRecorder = SpanRecorder[anthropicschema.MessagesRequest, anthropicschema.MessagesResponse, anthropicschema.MessagesStreamChunk]
)

// NoopChunkRecorder provides a no-op RecordResponseChunks implementation for recorders that don't emit streaming chunks.
type NoopChunkRecorder[ChunkT any] struct{}

// RecordResponseChunks implements SpanRecorder.RecordResponseChunks as a no-op.
func (NoopChunkRecorder[ChunkT]) RecordResponseChunks(trace.Span, []*ChunkT) {}

// NoopTracing is a Tracing that doesn't do anything.
type NoopTracing struct{}

func (t NoopTracing) MCPTracer() MCPTracer {
	return NoopMCPTracer{}
}

// ChatCompletionTracer implements Tracing.ChatCompletionTracer.
func (NoopTracing) ChatCompletionTracer() ChatCompletionTracer {
	return NoopTracer[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]{}
}

// CompletionTracer implements Tracing.CompletionTracer.
func (NoopTracing) CompletionTracer() CompletionTracer {
	return NoopTracer[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse]{}
}

// EmbeddingsTracer implements Tracing.EmbeddingsTracer.
func (NoopTracing) EmbeddingsTracer() EmbeddingsTracer {
	return NoopTracer[openai.EmbeddingRequest, openai.EmbeddingResponse, struct{}]{}
}

// ImageGenerationTracer implements Tracing.ImageGenerationTracer.
func (NoopTracing) ImageGenerationTracer() ImageGenerationTracer {
	return NoopTracer[openaisdk.ImageGenerateParams, openaisdk.ImagesResponse, struct{}]{}
}

// RerankTracer implements Tracing.RerankTracer.
func (NoopTracing) RerankTracer() RerankTracer {
	return NoopTracer[cohere.RerankV2Request, cohere.RerankV2Response, struct{}]{}
}

func (NoopTracing) MessageTracer() MessageTracer {
	return NoopTracer[anthropicschema.MessagesRequest, anthropicschema.MessagesResponse, anthropicschema.MessagesStreamChunk]{}
}

// Shutdown implements Tracing.Shutdown.
func (NoopTracing) Shutdown(context.Context) error {
	return nil
}

// NoopTracer implements RequestTracer without producing spans.
type NoopTracer[ReqT any, RespT any, RespChunkT any] struct{}

// StartSpanAndInjectHeaders implements RequestTracer.StartSpanAndInjectHeaders.
func (NoopTracer[ReqT, RespT, RespChunkT]) StartSpanAndInjectHeaders(context.Context, map[string]string, propagation.TextMapCarrier, *ReqT, []byte) Span[RespT, RespChunkT] {
	var zero Span[RespT, RespChunkT]
	return zero
}
