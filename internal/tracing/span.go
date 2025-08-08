// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"go.opentelemetry.io/otel/trace"
)

// Ensure chatCompletionSpan implements ChatCompletionSpan.
var _ ChatCompletionSpan = (*chatCompletionSpan)(nil)

type chatCompletionSpan struct {
	span     trace.Span
	recorder ChatCompletionRecorder
	chunkIdx int
}

// RecordChunk invokes ChatCompletionRecorder.RecordChunk.
func (s *chatCompletionSpan) RecordChunk() {
	s.recorder.RecordChunk(s.span, s.chunkIdx)
	s.chunkIdx++
}

// EndSpan invokes ChatCompletionRecorder.RecordResponse.
func (s *chatCompletionSpan) EndSpan(statusCode int, body []byte) {
	s.recorder.RecordResponse(s.span, statusCode, body)
	s.span.End()
}
