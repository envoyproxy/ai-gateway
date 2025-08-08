// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

// NewChatCompletionTracer creates OpenTelemetry tracing with the provided
// tracer and propagator. When tracerProvider is a noop.TracerProvider, this
// returns NoopChatCompletionTracer.
func NewChatCompletionTracer(tracerProvider trace.TracerProvider, propagator propagation.TextMapPropagator) ChatCompletionTracer {
	// Currently, we only support OpenInference format for chat completions.
	return newChatCompletionTracer(tracerProvider, propagator, openinference.ChatCompletionRecorder{}, openinference.ChatCompletionStreamingRecorder{})
}

func newChatCompletionTracer(tracerProvider trace.TracerProvider, propagator propagation.TextMapPropagator, recorder, streamRecorder ChatCompletionRecorder) ChatCompletionTracer {
	// Check if the tracer is a no-op by checking its type.
	if _, ok := tracerProvider.(noop.TracerProvider); ok {
		return NoopChatCompletionTracer{}
	}
	return &chatCompletionTracer{
		recorder:       recorder,
		streamRecorder: streamRecorder,
		otelTracer:     tracerProvider.Tracer("envoyproxy/ai-gateway"),
		propagator:     propagator,
	}
}

// Ensure chatCompletionTracer implements ChatCompletionTracer.
var _ ChatCompletionTracer = (*chatCompletionTracer)(nil)

type chatCompletionTracer struct {
	otelTracer     trace.Tracer
	recorder       ChatCompletionRecorder
	streamRecorder ChatCompletionRecorder
	propagator     propagation.TextMapPropagator
}

// StartSpanAndInjectHeaders implements ChatCompletionTracer.StartSpanAndInjectHeaders.
func (t *chatCompletionTracer) StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, mutableHeaders *extprocv3.HeaderMutation, req *openai.ChatCompletionRequest, body []byte) ChatCompletionSpan {
	var recorder ChatCompletionRecorder
	if req.Stream {
		recorder = t.streamRecorder
	} else {
		recorder = t.recorder
	}

	// Extract trace context from incoming headers.
	parentCtx := t.propagator.Extract(ctx, propagation.MapCarrier(headers))

	// Start the span with options appropriate for the semantic convention.
	spanName, opts := recorder.StartParams(req, body)
	newCtx, span := t.otelTracer.Start(parentCtx, spanName, opts...)

	// Always inject trace context into the header mutation if provided.
	// This ensures trace propagation works even for unsampled spans.
	t.propagator.Inject(newCtx, &headerMutationCarrier{m: mutableHeaders})

	// Only record request attributes if span is recording (sampled).
	// This avoids expensive body processing for unsampled spans.
	if span.IsRecording() {
		recorder.RecordRequest(span, req, body)
		return &chatCompletionSpan{span: span, recorder: recorder}
	}

	return nil
}

type headerMutationCarrier struct {
	m *extprocv3.HeaderMutation
}

// Get implements the same method as defined on propagation.TextMapCarrier.
func (c *headerMutationCarrier) Get(string) string {
	panic("unexpected as this carrier is write-only for injection")
}

// Set adds a key-value pair to the HeaderMutation.
func (c *headerMutationCarrier) Set(key, value string) {
	if c.m.SetHeaders == nil {
		c.m.SetHeaders = make([]*corev3.HeaderValueOption, 0, 4)
	}
	c.m.SetHeaders = append(c.m.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: key, RawValue: []byte(value)},
	})
}

// Keys implements the same method as defined on propagation.TextMapCarrier.
func (c *headerMutationCarrier) Keys() []string {
	panic("unexpected as this carrier is write-only for injection")
}
