// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"go.opentelemetry.io/otel/trace"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// Ensure rerankSpan implements RerankSpan.
var _ tracing.RerankSpan = (*rerankSpan)(nil)

type rerankSpan struct {
	span     trace.Span
	recorder tracing.RerankRecorder
}

// RecordResponse invokes [tracing.RerankRecorder.RecordResponse].
func (s *rerankSpan) RecordResponse(resp *cohereschema.RerankV2Response) {
	s.recorder.RecordResponse(s.span, resp)
}

// EndSpan invokes span.End.
func (s *rerankSpan) EndSpan() {
	s.span.End()
}

// EndSpanOnError invokes [tracing.RerankRecorder.RecordResponseOnError] and ends the span.
func (s *rerankSpan) EndSpanOnError(statusCode int, body []byte) {
	s.recorder.RecordResponseOnError(s.span, statusCode, body)
	s.span.End()
}
