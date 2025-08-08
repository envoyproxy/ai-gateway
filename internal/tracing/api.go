// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package tracing provides OpenTelemetry tracing support for the AI Gateway.
package tracing

import (
	"context"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// ChatCompletionTracer creates spans for OpenAI chat completion requests.
type ChatCompletionTracer interface {
	// StartSpanAndInjectHeaders starts a span and injects trace context into
	// the header mutation.
	//
	// Parameters:
	//   - ctx: might include a parent span context.
	//   - headers: Incoming HTTP headers used to extract parent trace context.
	//   - headerMutation: The new LLM Span will have its context written to
	//     these headers unless NoopTracer is used.
	//   - req: The OpenAI chat completion request. Used to detect streaming
	//     and record request attributes.
	//
	// Returns:
	//   - A NoopChatCompletionSpan unless the span is sampled.
	StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, headerMutation *extprocv3.HeaderMutation, req *openai.ChatCompletionRequest, body []byte) ChatCompletionSpan
}

// ChatCompletionSpan represents an OpenAI chat completion.
type ChatCompletionSpan interface {
	// RecordChunk records timing events for streaming responses.
	RecordChunk()

	// EndSpan finalizes and ends the span with response data.
	//
	// Parameters:
	//   - statusCode: HTTP status code of the response or zero if unknown.
	//   - body: the entire buffered response body, which is SSE chunks when streaming.
	EndSpan(statusCode int, body []byte)
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

	// RecordChunk is only called when streaming and records the timing of a
	// streaming chunk.
	RecordChunk(span trace.Span, chunkIdx int)

	// RecordResponse records response attributes to the span.
	//
	// Parameters:
	//   - statusCode: is the HTTP status code of the response or zero if unknown.
	//   - body: contains the complete response body.
	RecordResponse(span trace.Span, statusCode int, body []byte)
}

// NoopChatCompletionTracer is a ChatCompletionTracer that doesn't do anything.
type NoopChatCompletionTracer struct{}

// StartSpanAndInjectHeaders implements ChatCompletionTracer.StartSpanAndInjectHeaders.
func (NoopChatCompletionTracer) StartSpanAndInjectHeaders(context.Context, map[string]string, *extprocv3.HeaderMutation, *openai.ChatCompletionRequest, []byte) ChatCompletionSpan {
	return nil
}
