// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// Ensure chatCompletionSpan implements ChatCompletionSpan.
var _ tracing.ChatCompletionSpan = (*chatCompletionSpan)(nil)

type chatCompletionSpan struct {
	span     trace.Span
	recorder tracing.ChatCompletionRecorder
	chunks   []*openai.ChatCompletionResponseChunk
}

// RecordResponseChunk invokes [tracing.ChatCompletionRecorder.RecordResponseChunk].
func (s *chatCompletionSpan) RecordResponseChunk(resp *openai.ChatCompletionResponseChunk) {
	s.chunks = append(s.chunks, resp) // Delay recording until EndSpan to collect all events.
}

// RecordResponse invokes [tracing.ChatCompletionRecorder.RecordResponse].
func (s *chatCompletionSpan) RecordResponse(resp *openai.ChatCompletionResponse) {
	s.recorder.RecordResponse(s.span, resp)
}

// EndSpan invokes [tracing.ChatCompletionRecorder.RecordResponse].
func (s *chatCompletionSpan) EndSpan() {
	if len(s.chunks) > 0 {
		s.recorder.RecordResponseChunks(s.span, s.chunks)
	}
	s.span.End()
}

// EndSpanOnError invokes [tracing.ChatCompletionRecorder.RecordResponse].
func (s *chatCompletionSpan) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}

// Ensure embeddingsSpan implements EmbeddingsSpan.
var _ tracing.EmbeddingsSpan = (*embeddingsSpan)(nil)

type embeddingsSpan struct {
	span     trace.Span
	recorder tracing.EmbeddingsRecorder
}

// RecordResponse invokes [tracing.EmbeddingsRecorder.RecordResponse].
func (s *embeddingsSpan) RecordResponse(resp *openai.EmbeddingResponse) {
	s.recorder.RecordResponse(s.span, resp)
}

// EndSpan finalizes and ends the span.
func (s *embeddingsSpan) EndSpan() {
	s.span.End()
}

// EndSpanOnError invokes [tracing.EmbeddingsRecorder.RecordResponseOnError].
func (s *embeddingsSpan) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}

// Ensure completionSpan implements CompletionSpan.
var _ tracing.CompletionSpan = (*completionSpan)(nil)

type completionSpan struct {
	span     trace.Span
	recorder tracing.CompletionRecorder
	chunks   []*openai.CompletionResponse
}

// RecordResponseChunk invokes [tracing.CompletionRecorder.RecordResponseChunk].
func (s *completionSpan) RecordResponseChunk(resp *openai.CompletionResponse) {
	s.chunks = append(s.chunks, resp) // Delay recording until EndSpan to collect all events.
}

// RecordResponse invokes [tracing.CompletionRecorder.RecordResponse].
func (s *completionSpan) RecordResponse(resp *openai.CompletionResponse) {
	s.recorder.RecordResponse(s.span, resp)
}

// EndSpan invokes [tracing.CompletionRecorder.RecordResponseChunks].
func (s *completionSpan) EndSpan() {
	if len(s.chunks) > 0 {
		s.recorder.RecordResponseChunks(s.span, s.chunks)
	}
	s.span.End()
}

// EndSpanOnError invokes [tracing.CompletionRecorder.RecordResponseOnError].
func (s *completionSpan) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}

type responsesSpan struct {
	span                   trace.Span
	recorder               tracing.ResponsesRecorder
	responseCompletedChunk *openai.ResponseCompletedEvent
}

// RecordResponse implements [tracing.ResponsesSpan.RecordResponse].
func (s *responsesSpan) RecordResponse(resp *openai.ResponseResponse) {
	s.recorder.RecordResponse(s.span, resp)
}

// RecordResponseChunk implements [tracing.ResponsesSpan.RecordResponseChunk].
func (s *responsesSpan) RecordResponseChunk(resp *openai.ResponseCompletedEvent) {
	s.responseCompletedChunk = resp
}

// EndSpanOnError implements [tracing.ResponsesSpan.EndSpanOnError].
func (s *responsesSpan) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}

// EndSpan implements [tracing.ResponsesSpan.EndSpan].
func (s *responsesSpan) EndSpan() {
	s.span.End()
}
