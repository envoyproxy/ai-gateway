// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	openaisdk "github.com/openai/openai-go/v2"
	"go.opentelemetry.io/otel/trace"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

type responseRecorder[RespT any] interface {
	RecordResponse(trace.Span, *RespT)
	RecordResponseOnError(trace.Span, int, []byte)
}

type streamResponseRecorder[RespT, ChunkT any] interface {
	responseRecorder[RespT]
	RecordResponseChunks(trace.Span, []*ChunkT)
}

type noopChunkRecorder[ChunkT any] struct{}

func (noopChunkRecorder[ChunkT]) RecordResponseChunk(*ChunkT) {}

type streamingSpan[RespT, ChunkT any] struct {
	span     trace.Span
	recorder streamResponseRecorder[RespT, ChunkT]
	chunks   []*ChunkT
}

func (s *streamingSpan[RespT, ChunkT]) RecordResponseChunk(resp *ChunkT) {
	s.chunks = append(s.chunks, resp)
}

func (s *streamingSpan[RespT, ChunkT]) RecordResponse(resp *RespT) {
	s.recorder.RecordResponse(s.span, resp)
}

func (s *streamingSpan[RespT, ChunkT]) EndSpan() {
	if len(s.chunks) > 0 {
		s.recorder.RecordResponseChunks(s.span, s.chunks)
	}
	s.span.End()
}

func (s *streamingSpan[RespT, ChunkT]) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}

type responseSpan[RespT, ChunkT any] struct {
	noopChunkRecorder[ChunkT]
	span     trace.Span
	recorder streamResponseRecorder[RespT, ChunkT]
}

func (s *responseSpan[RespT, ChunkT]) RecordResponse(resp *RespT) {
	s.recorder.RecordResponse(s.span, resp)
}

func (s *responseSpan[RespT, ChunkT]) EndSpan() {
	s.span.End()
}

func (s *responseSpan[RespT, ChunkT]) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}

// Type aliases tying generic implementations to concrete recorder contracts.
type (
	chatCompletionSpan  = streamingSpan[openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]
	completionSpan      = streamingSpan[openai.CompletionResponse, openai.CompletionResponse]
	embeddingsSpan      = responseSpan[openai.EmbeddingResponse, struct{}]
	imageGenerationSpan = responseSpan[openaisdk.ImagesResponse, struct{}]
	rerankSpan          = responseSpan[cohereschema.RerankV2Response, struct{}]
	messageSpan         = responseSpan[anthropicschema.MessagesResponse, anthropicschema.MessagesStreamEvent]
)

var (
	_ tracing.ChatCompletionSpan  = (*chatCompletionSpan)(nil)
	_ tracing.CompletionSpan      = (*completionSpan)(nil)
	_ tracing.EmbeddingsSpan      = (*embeddingsSpan)(nil)
	_ tracing.ImageGenerationSpan = (*imageGenerationSpan)(nil)
	_ tracing.RerankSpan          = (*rerankSpan)(nil)
)
