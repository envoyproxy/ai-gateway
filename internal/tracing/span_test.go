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
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
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

func TestEmbeddingsSpan_EndSpanOnError(t *testing.T) {
	msg := "embeddings error occurred"
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s := &embeddingsSpan{span: span, recorder: testEmbeddingsRecorder{}}
		s.EndSpanOnError(500, []byte(msg))
		return true // EndSpanOnError ends the underlying span.
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 500),
		attribute.String("errorBody", msg),
	}, actualSpan.Attributes)
}

func TestCompletionSpan_RecordResponseChunk(t *testing.T) {
	chunks := []*openai.CompletionResponse{{}, {}}
	s := &completionSpan{}
	_ = testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.RecordResponseChunk(chunks[0])
		s.RecordResponseChunk(chunks[1])
		return false // Recording of chunks shouldn't end the span.
	})
	require.Len(t, s.chunks, 2)
	require.Equal(t, chunks, s.chunks)
}

func TestCompletionSpan_RecordResponse(t *testing.T) {
	resp := &openai.CompletionResponse{ID: "cmpl-abc123"}
	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)
	s := &completionSpan{recorder: testCompletionRecorder{}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.RecordResponse(resp)
		return false // Recording of response shouldn't end the span.
	})
	require.Empty(t, s.chunks)
	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 200),
		attribute.Int("respBodyLen", len(respBytes)),
	}, actualSpan.Attributes)
}

func TestCompletionSpan_EndSpanOnError(t *testing.T) {
	msg := "completion error occurred"
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s := &completionSpan{span: span, recorder: testCompletionRecorder{}}
		s.EndSpanOnError(500, []byte(msg))
		return true // EndSpanOnError ends the underlying span.
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 500),
		attribute.String("errorBody", msg),
	}, actualSpan.Attributes)
}

func TestCompletionSpan_EndSpan(t *testing.T) {
	s := &completionSpan{recorder: testCompletionRecorder{}, chunks: []*openai.CompletionResponse{{}, {}}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.EndSpan()
		return true // EndSpan ends the underlying span.
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("eventCount", 2),
	}, actualSpan.Attributes)
}

// Responses span tests
func TestResponsesSpan_RecordResponseChunk(t *testing.T) {
	chunk := &openai.ResponseCompletedEvent{}
	s := &responsesSpan{}
	_ = testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.RecordResponseChunk(chunk)
		return false // Recording chunk shouldn't end the span.
	})
	require.Equal(t, chunk, s.responseCompletedChunk)
}

func TestResponsesSpan_RecordResponse(t *testing.T) {
	resp := &openai.ResponseResponse{ID: "resp-abc123"}
	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)
	s := &responsesSpan{recorder: testResponsesRecorder{}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.RecordResponse(resp)
		return false // Recording of response shouldn't end the span.
	})
	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 200),
		attribute.Int("respBodyLen", len(respBytes)),
	}, actualSpan.Attributes)
}

func TestResponsesSpan_EndSpanOnError(t *testing.T) {
	msg := "responses error occurred"
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s := &responsesSpan{span: span, recorder: testResponsesRecorder{}}
		s.EndSpanOnError(500, []byte(msg))
		return true // EndSpanOnError ends the underlying span.
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 500),
		attribute.String("errorBody", msg),
	}, actualSpan.Attributes)
}

func TestResponsesSpan_EndSpan(t *testing.T) {
	s := &responsesSpan{recorder: testResponsesRecorder{}, responseCompletedChunk: &openai.ResponseCompletedEvent{}}
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s.span = span
		s.EndSpan()
		return true // EndSpan ends the underlying span.
	})

	// responsesSpan.EndSpan does not record chunks by default, so no attributes
	require.Empty(t, actualSpan.Attributes)
}

// testResponsesRecorder implements tracing.ResponsesRecorder for span tests.
var _ tracing.ResponsesRecorder = testResponsesRecorder{}

type testResponsesRecorder struct{}

func (testResponsesRecorder) StartParams(_ *openai.ResponseRequest, _ []byte) (spanName string, opts []oteltrace.SpanStartOption) {
	return "Responses", startOpts
}

func (testResponsesRecorder) RecordRequest(span oteltrace.Span, req *openai.ResponseRequest, body []byte) {
	span.SetAttributes(attribute.String("model", req.Model))
	span.SetAttributes(attribute.Int("reqBodyLen", len(body)))
}

func (testResponsesRecorder) RecordResponseChunk(_ oteltrace.Span, _ *openai.ResponseCompletedEvent) {
	// For tests, count as one event.
}

func (testResponsesRecorder) RecordResponseOnError(span oteltrace.Span, statusCode int, body []byte) {
	span.SetAttributes(attribute.Int("statusCode", statusCode))
	span.SetAttributes(attribute.String("errorBody", string(body)))
}

func (testResponsesRecorder) RecordResponse(span oteltrace.Span, resp *openai.ResponseResponse) {
	span.SetAttributes(attribute.Int("statusCode", 200))
	body, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	span.SetAttributes(attribute.Int("respBodyLen", len(body)))
}
