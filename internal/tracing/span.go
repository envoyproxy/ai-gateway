// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	openaisdk "github.com/openai/openai-go/v2"
	"go.opentelemetry.io/otel/trace"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

type responseRecorder[RespT any] interface {
	RecordResponse(trace.Span, *RespT)
	RecordResponseOnError(trace.Span, int, []byte)
}

type streamResponseRecorder[ChunkT any, RespT any] interface {
	responseRecorder[RespT]
	RecordResponseChunks(trace.Span, []*ChunkT)
}

type noopChunkRecorder[ChunkT any] struct{}

func (noopChunkRecorder[ChunkT]) RecordResponseChunk(*ChunkT) {}

type streamingSpan[ChunkT any, RespT any, Recorder streamResponseRecorder[ChunkT, RespT]] struct {
	span     trace.Span
	recorder Recorder
	chunks   []*ChunkT
}

func (s *streamingSpan[ChunkT, RespT, Recorder]) RecordResponseChunk(resp *ChunkT) {
	s.chunks = append(s.chunks, resp)
}

func (s *streamingSpan[ChunkT, RespT, Recorder]) RecordResponse(resp *RespT) {
	s.recorder.RecordResponse(s.span, resp)
}

func (s *streamingSpan[ChunkT, RespT, Recorder]) EndSpan() {
	if len(s.chunks) > 0 {
		s.recorder.RecordResponseChunks(s.span, s.chunks)
	}
	s.span.End()
}

func (s *streamingSpan[ChunkT, RespT, Recorder]) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}

type responseSpan[ChunkT any, RespT any, Recorder responseRecorder[RespT]] struct {
	noopChunkRecorder[ChunkT]
	span     trace.Span
	recorder Recorder
}

func (s *responseSpan[ChunkT, RespT, Recorder]) RecordResponse(resp *RespT) {
	s.recorder.RecordResponse(s.span, resp)
}

func (s *responseSpan[ChunkT, RespT, Recorder]) EndSpan() {
	s.span.End()
}

func (s *responseSpan[ChunkT, RespT, Recorder]) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}

// Type aliases tying generic implementations to concrete recorder contracts.
type (
	chatCompletionSpan  = streamingSpan[openai.ChatCompletionResponseChunk, openai.ChatCompletionResponse, tracing.ChatCompletionRecorder]
	completionSpan      = streamingSpan[openai.CompletionResponse, openai.CompletionResponse, tracing.CompletionRecorder]
	embeddingsSpan      = responseSpan[struct{}, openai.EmbeddingResponse, tracing.EmbeddingsRecorder]
	imageGenerationSpan = responseSpan[struct{}, openaisdk.ImagesResponse, tracing.ImageGenerationRecorder]
	rerankSpan          = responseSpan[struct{}, cohereschema.RerankV2Response, tracing.RerankRecorder]
)

var (
	_ tracing.ChatCompletionSpan  = (*chatCompletionSpan)(nil)
	_ tracing.CompletionSpan      = (*completionSpan)(nil)
	_ tracing.EmbeddingsSpan      = (*embeddingsSpan)(nil)
	_ tracing.ImageGenerationSpan = (*imageGenerationSpan)(nil)
	_ tracing.RerankSpan          = (*rerankSpan)(nil)
)
