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
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

func TestChatCompletionSpan_RecordResponseChunk(t *testing.T) {
	chunks := []*openai.ChatCompletionResponseChunk{{}, {}}
	s := &chatCompletionSpan{}
	_ = testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.RecordResponseChunk(chunks[0])
		s.RecordResponseChunk(chunks[1])
		return false // Recording of chunks shouldn't end the span.
	})
	require.Len(t, s.chunks, 2)
	require.Equal(t, chunks, s.chunks)
}

func TestChatCompletionSpan_RecordResponse(t *testing.T) {
	resp := &openai.ChatCompletionResponse{ID: "chatcmpl-abc123"}
	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)
	s := &chatCompletionSpan{recorder: testChatCompletionRecorder{}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.RecordResponse(resp)
		return false // Recording of chunks shouldn't end the span.
	})
	require.Empty(t, s.chunks)
	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 200),
		attribute.Int("respBodyLen", len(respBytes)),
	}, actualSpan.Attributes)
}

func TestChatCompletionSpan_EndSpanOnError(t *testing.T) {
	msg := "why did you do that?"
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s := &chatCompletionSpan{span: span, recorder: testChatCompletionRecorder{}}
		s.EndSpanOnError(500, []byte(msg))
		return true // EndSpan ends the underlying span.
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 500),
		attribute.String("errorBody", msg),
	}, actualSpan.Attributes)
}

func TestChatCompletionSpan_EndSpan(t *testing.T) {
	s := &chatCompletionSpan{recorder: testChatCompletionRecorder{}, chunks: []*openai.ChatCompletionResponseChunk{{}, {}}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.EndSpan()
		return true // EndSpan ends the underlying span.
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("eventCount", 2),
	}, actualSpan.Attributes)
}
