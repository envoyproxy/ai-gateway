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
	"go.opentelemetry.io/otel/trace"

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
		// MCPTracer creates spans for MCP requests.
		MCPTracer() MCPTracer
		// Shutdown shuts down the tracer, flushing any buffered spans.
		Shutdown(context.Context) error
	}
	// RequestTracer standardizes tracer implementations for non-MCP requests.
	RequestTracer[ReqT any, SpanT any] interface {
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
	// ChatCompletionTracer creates spans for OpenAI chat completion requests.
	ChatCompletionTracer = RequestTracer[openai.ChatCompletionRequest, ChatCompletionSpan]
	// CompletionTracer creates spans for OpenAI completion requests.
	CompletionTracer = RequestTracer[openai.CompletionRequest, CompletionSpan]
	// EmbeddingsTracer creates spans for OpenAI embeddings requests.
	EmbeddingsTracer = RequestTracer[openai.EmbeddingRequest, EmbeddingsSpan]
	// ImageGenerationTracer creates spans for OpenAI image generation requests.
	ImageGenerationTracer = RequestTracer[openaisdk.ImageGenerateParams, ImageGenerationSpan]
	// RerankTracer creates spans for rerank requests.
	RerankTracer = RequestTracer[cohere.RerankV2Request, RerankSpan]
)

type (
	// Span standardizes span interfaces, supporting both streaming and non-streaming endpoints.
	Span[ChunkT any, RespT any] interface {
		// RecordResponseChunk records streaming response chunks. Implementations that do not support streaming should provide a no-op implementation.
		RecordResponseChunk(resp *ChunkT)
		// RecordResponse records the response attributes to the span.
		RecordResponse(resp *RespT)
		// EndSpanOnError finalizes and ends the span with an error status.
		EndSpanOnError(statusCode int, body []byte)
		// EndSpan finalizes and ends the span.
		EndSpan()
	}
	// ChatCompletionSpan represents an OpenAI chat completion.
	ChatCompletionSpan = Span[openai.ChatCompletionResponseChunk, openai.ChatCompletionResponse]
	// CompletionSpan represents an OpenAI completion request.
	// Note: Completion streaming chunks are full CompletionResponse objects, not deltas like chat completions.
	CompletionSpan = Span[openai.CompletionResponse, openai.CompletionResponse]
	// EmbeddingsSpan represents an OpenAI embeddings request. The chunk type is unused and therefore set to struct{}.
	EmbeddingsSpan = Span[struct{}, openai.EmbeddingResponse]
	// ImageGenerationSpan represents an OpenAI image generation.
	ImageGenerationSpan = Span[struct{}, openaisdk.ImagesResponse]
	// RerankSpan represents a rerank request span.
	RerankSpan = Span[struct{}, cohere.RerankV2Response]
)

type (
	// SpanRecorder standardizes recorder implementations for non-MCP tracers.
	SpanRecorder[ReqT any] interface {
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
	ResponseRecorder[ReqT any, RespT any] interface {
		SpanRecorder[ReqT]

		// RecordResponse records response attributes to the span.
		RecordResponse(span trace.Span, resp *RespT)
	}
	// StreamRecorder augments SpanRecorder with streaming response chunk recording.
	StreamRecorder[ReqT any, ChunkT any] interface {
		SpanRecorder[ReqT]

		// RecordResponseChunks records response chunk attributes to the span for streaming response.
		RecordResponseChunks(span trace.Span, chunks []*ChunkT)
	}
	// StreamResponseRecorder combines streaming chunks with a final response recorder.
	StreamResponseRecorder[ReqT any, ChunkT any, RespT any] interface {
		ResponseRecorder[ReqT, RespT]
		RecordResponseChunks(span trace.Span, chunks []*ChunkT)
	}
	// ChatCompletionRecorder records attributes to a span according to a semantic convention.
	ChatCompletionRecorder = StreamResponseRecorder[openai.ChatCompletionRequest, openai.ChatCompletionResponseChunk, openai.ChatCompletionResponse]
	// CompletionRecorder records attributes to a span according to a semantic convention.
	// Note: Completion streaming chunks are full CompletionResponse objects, not deltas like chat completions.
	CompletionRecorder = StreamResponseRecorder[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse]
	// ImageGenerationRecorder records attributes to a span according to a semantic convention.
	ImageGenerationRecorder = ResponseRecorder[openaisdk.ImageGenerateParams, openaisdk.ImagesResponse]
	// EmbeddingsRecorder records attributes to a span according to a semantic convention.
	EmbeddingsRecorder = ResponseRecorder[openai.EmbeddingRequest, openai.EmbeddingResponse]
	// RerankRecorder records attributes to a span according to a semantic convention.
	RerankRecorder = ResponseRecorder[cohere.RerankV2Request, cohere.RerankV2Response]
)

// NoopTracing is a Tracing that doesn't do anything.
type NoopTracing struct{}

func (t NoopTracing) MCPTracer() MCPTracer {
	return NoopMCPTracer{}
}

// ChatCompletionTracer implements Tracing.ChatCompletionTracer.
func (NoopTracing) ChatCompletionTracer() ChatCompletionTracer {
	return NoopTracer[openai.ChatCompletionRequest, ChatCompletionSpan]{}
}

// CompletionTracer implements Tracing.CompletionTracer.
func (NoopTracing) CompletionTracer() CompletionTracer {
	return NoopTracer[openai.CompletionRequest, CompletionSpan]{}
}

// EmbeddingsTracer implements Tracing.EmbeddingsTracer.
func (NoopTracing) EmbeddingsTracer() EmbeddingsTracer {
	return NoopTracer[openai.EmbeddingRequest, EmbeddingsSpan]{}
}

// ImageGenerationTracer implements Tracing.ImageGenerationTracer.
func (NoopTracing) ImageGenerationTracer() ImageGenerationTracer {
	return NoopTracer[openaisdk.ImageGenerateParams, ImageGenerationSpan]{}
}

// RerankTracer implements Tracing.RerankTracer.
func (NoopTracing) RerankTracer() RerankTracer {
	return NoopTracer[cohere.RerankV2Request, RerankSpan]{}
}

// Shutdown implements Tracing.Shutdown.
func (NoopTracing) Shutdown(context.Context) error {
	return nil
}

// NoopTracer implements RequestTracer without producing spans.
type NoopTracer[ReqT any, SpanT any] struct{}

// StartSpanAndInjectHeaders implements RequestTracer.StartSpanAndInjectHeaders.
func (NoopTracer[ReqT, SpanT]) StartSpanAndInjectHeaders(context.Context, map[string]string, *extprocv3.HeaderMutation, *ReqT, []byte) SpanT {
	var zero SpanT
	return zero
}
