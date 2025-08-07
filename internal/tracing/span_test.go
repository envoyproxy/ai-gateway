// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/tests/testotel"
)

func TestChatCompletionSpan_RecordChunk(t *testing.T) {
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s := &chatCompletionSpan{span: span, recorder: testChatCompletionStreamRecorder{}}
		s.RecordChunk()
		s.RecordChunk()
		return false // Recording of chunks shouldn't end the span.
	})

	require.Equal(t, []trace.Event{
		{
			Name: "chunk.0",
			Time: time.Time{},
		},
		{
			Name: "chunk.1",
			Time: time.Time{},
		},
	}, actualSpan.Events)
}

func TestChatCompletionSpan_EndSpan(t *testing.T) {
	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		s := &chatCompletionSpan{span: span, recorder: testChatCompletionStreamRecorder{}}
		s.EndSpan(200, []byte(respBody))
		return true // EndSpan ends the underlying span.
	})

	require.Equal(t, []attribute.KeyValue{
		attribute.Int("statusCode", 200),
		attribute.Int("respBodyLen", len(respBody)),
	}, actualSpan.Attributes)
}
