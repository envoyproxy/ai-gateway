// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	cohere "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

type testRerankRecorder struct{}

func (testRerankRecorder) StartParams(*cohere.RerankV2Request, []byte) (string, []oteltrace.SpanStartOption) {
	return "Rerank", []oteltrace.SpanStartOption{oteltrace.WithSpanKind(oteltrace.SpanKindInternal)}
}

func (testRerankRecorder) RecordRequest(oteltrace.Span, *cohere.RerankV2Request, []byte) {}

func (testRerankRecorder) RecordResponse(span oteltrace.Span, resp *cohere.RerankV2Response) {
	span.SetAttributes(attribute.Int("statusCode", 200))
	b, _ := json.Marshal(resp)
	span.SetAttributes(attribute.Int("respBodyLen", len(b)))
}

func (testRerankRecorder) RecordResponseOnError(span oteltrace.Span, statusCode int, body []byte) {
	span.SetAttributes(attribute.Int("statusCode", statusCode))
	span.SetAttributes(attribute.String("errorBody", string(body)))
}

func TestRerankSpan_RecordResponse(t *testing.T) {
	resp := &cohere.RerankV2Response{
		Results: []*cohere.RerankV2Result{{Index: 1, RelevanceScore: 0.9}},
	}

	s := &rerankSpan{recorder: testRerankRecorder{}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.RecordResponse(resp)
		return false
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 200),
		attribute.Int("respBodyLen", len(mustJSON(resp))),
	}, actualSpan.Attributes)
	require.Empty(t, actualSpan.Events)
	require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
}

func TestRerankSpan_EndSpan(t *testing.T) {
	s := &rerankSpan{recorder: testRerankRecorder{}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.EndSpan()
		return true
	})
	require.Empty(t, actualSpan.Attributes)
	require.Empty(t, actualSpan.Events)
	require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
}

func TestRerankSpan_EndSpanOnError(t *testing.T) {
	errorMsg := "rerank failed"
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s := &rerankSpan{span: span, recorder: testRerankRecorder{}}
		s.EndSpanOnError(500, []byte(errorMsg))
		return true
	})
	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 500),
		attribute.String("errorBody", errorMsg),
	}, actualSpan.Attributes)
	require.Empty(t, actualSpan.Events)
	require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
