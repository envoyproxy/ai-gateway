// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testotel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// RecordNewSpan starts a new span with the given name and options and
// immediately ends it. Then, it returns the recorded span.
func RecordNewSpan(t testing.TB, spanName string, opts ...oteltrace.SpanStartOption) tracetest.SpanStub {
	return recordWithSpan(t, func(oteltrace.Span) bool {
		return false
	}, spanName, opts)
}

// RecordWithSpan executes the provided function with a span and returns the
// recorded span. The function should return true if it ended the span.
func RecordWithSpan(t testing.TB, fn func(oteltrace.Span) bool) tracetest.SpanStub {
	spanName := "test"
	opts := []oteltrace.SpanStartOption{oteltrace.WithSpanKind(oteltrace.SpanKindInternal)}
	return recordWithSpan(t, fn, spanName, opts)
}

func recordWithSpan(t testing.TB, fn func(oteltrace.Span) bool, spanName string, opts []oteltrace.SpanStartOption) tracetest.SpanStub {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tracer := tp.Tracer("test")
	_, span := tracer.Start(t.Context(), spanName, opts...)

	if !fn(span) {
		span.End()
	}

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	actualSpan := spans[0]
	// Clear timestamps for comparison.
	actualSpan.StartTime = time.Time{}
	actualSpan.EndTime = time.Time{}
	for i := range actualSpan.Events {
		actualSpan.Events[i].Time = time.Time{}
	}
	return actualSpan
}
