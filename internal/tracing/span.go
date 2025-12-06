// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"go.opentelemetry.io/otel/trace"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

type span[RespT, ChunkT any] struct {
	span     trace.Span
	recorder tracing.SpanResponseRecorder[RespT, ChunkT]
	chunks   []*ChunkT
}

// RecordResponseChunk implements [tracing.Span.RecordResponseChunk]
func (s *span[RespT, ChunkT]) RecordResponseChunk(resp *ChunkT) {
	s.chunks = append(s.chunks, resp)
}

// RecordResponse implements [tracing.Span.RecordResponse]
func (s *span[RespT, ChunkT]) RecordResponse(resp *RespT) {
	s.recorder.RecordResponse(s.span, resp)
}

// EndSpan implements [tracing.Span.EndSpan]
func (s *span[RespT, ChunkT]) EndSpan() {
	if len(s.chunks) > 0 {
		s.recorder.RecordResponseChunks(s.span, s.chunks)
	}
	s.span.End()
}

// EndSpanOnError implements [tracing.Span.EndSpanOnError]
func (s *span[RespT, ChunkT]) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}

// Type aliases tying generic implementations to concrete recorder contracts.
type (
	chatCompletionSpan  = span[openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]
	completionSpan      = span[openai.CompletionResponse, openai.CompletionResponse]
	embeddingsSpan      = span[openai.EmbeddingResponse, struct{}]
	imageGenerationSpan = span[openai.ImageGenerationResponse, struct{}]
	rerankSpan          = span[cohereschema.RerankV2Response, struct{}]
	messageSpan         = span[anthropicschema.MessagesResponse, anthropicschema.MessagesStreamChunk]
)
