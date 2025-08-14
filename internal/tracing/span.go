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
