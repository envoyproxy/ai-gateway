// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"go.opentelemetry.io/otel/trace"

	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	openaisdk "github.com/openai/openai-go/v2"
)

// Ensure imageGenerationSpan implements ImageGenerationSpan.
var _ tracing.ImageGenerationSpan = (*imageGenerationSpan)(nil)

type imageGenerationSpan struct {
	span     trace.Span
	recorder tracing.ImageGenerationRecorder
}

// RecordResponse invokes [tracing.ImageGenerationRecorder.RecordResponse].
func (s *imageGenerationSpan) RecordResponse(resp *openaisdk.ImagesResponse) {
	s.recorder.RecordResponse(s.span, resp)
}

// EndSpan invokes [tracing.ImageGenerationRecorder.RecordResponse] and ends the span.
func (s *imageGenerationSpan) EndSpan() {
	s.span.End()
}

// EndSpanOnError invokes [tracing.ImageGenerationRecorder.RecordResponseOnError] and ends the span.
func (s *imageGenerationSpan) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}
