// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// Ensure chatCompletionTracer implements ChatCompletionTracer.
var _ tracing.ChatCompletionTracer = (*chatCompletionTracer)(nil)

func newChatCompletionTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.ChatCompletionRecorder, headerAttributes map[string]string) tracing.ChatCompletionTracer {
	// Check if the tracer is a no-op by checking its type.
	if _, ok := tracer.(noop.Tracer); ok {
		return tracing.NoopChatCompletionTracer{}
	}
	return &chatCompletionTracer{
		tracer:           tracer,
		propagator:       propagator,
		recorder:         recorder,
		headerAttributes: headerAttributes,
	}
}

type chatCompletionTracer struct {
	tracer           trace.Tracer
	recorder         tracing.ChatCompletionRecorder
	propagator       propagation.TextMapPropagator
	headerAttributes map[string]string
}

// StartSpanAndInjectHeaders implements ChatCompletionTracer.StartSpanAndInjectHeaders.
func (t *chatCompletionTracer) StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, mutableHeaders *extprocv3.HeaderMutation, req *openai.ChatCompletionRequest, body []byte) tracing.ChatCompletionSpan {
	// Extract trace context from incoming headers.
	parentCtx := t.propagator.Extract(ctx, propagation.MapCarrier(headers))

	// Start the span with options appropriate for the semantic convention.
	spanName, opts := t.recorder.StartParams(req, body)
	newCtx, span := t.tracer.Start(parentCtx, spanName, opts...)

	// Always inject trace context into the header mutation if provided.
	// This ensures trace propagation works even for unsampled spans.
	t.propagator.Inject(newCtx, &headerMutationCarrier{m: mutableHeaders})

	// Only record request attributes if span is recording (sampled).
	// This avoids expensive body processing for unsampled spans.
	if span.IsRecording() {
		t.recorder.RecordRequest(span, req, body)

		// Apply header-to-attribute mapping if configured.
		if len(t.headerAttributes) > 0 {
			attrs := make([]attribute.KeyValue, 0, len(t.headerAttributes))
			for headerName, attrName := range t.headerAttributes {
				if headerValue, ok := headers[headerName]; ok {
					attrs = append(attrs, attribute.String(attrName, headerValue))
				}
			}
			if len(attrs) > 0 {
				span.SetAttributes(attrs...)
			}
		}

		return &chatCompletionSpan{span: span, recorder: t.recorder}
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

// Ensure embeddingsTracer implements [tracing.EmbeddingsTracer].
var _ tracing.EmbeddingsTracer = (*embeddingsTracer)(nil)

func newEmbeddingsTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.EmbeddingsRecorder, headerAttributes map[string]string) tracing.EmbeddingsTracer {
	// Check if the tracer is a no-op by checking its type.
	if _, ok := tracer.(noop.Tracer); ok {
		return tracing.NoopEmbeddingsTracer{}
	}
	return &embeddingsTracer{
		tracer:           tracer,
		propagator:       propagator,
		recorder:         recorder,
		headerAttributes: headerAttributes,
	}
}

type embeddingsTracer struct {
	tracer           trace.Tracer
	recorder         tracing.EmbeddingsRecorder
	propagator       propagation.TextMapPropagator
	headerAttributes map[string]string
}

// StartSpanAndInjectHeaders implements [tracing.EmbeddingsTracer.StartSpanAndInjectHeaders].
func (t *embeddingsTracer) StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, mutableHeaders *extprocv3.HeaderMutation, req *openai.EmbeddingRequest, body []byte) tracing.EmbeddingsSpan {
	// Extract trace context from incoming headers.
	parentCtx := t.propagator.Extract(ctx, propagation.MapCarrier(headers))

	// Start the span with options appropriate for the semantic convention.
	spanName, opts := t.recorder.StartParams(req, body)
	newCtx, span := t.tracer.Start(parentCtx, spanName, opts...)

	// Always inject trace context into the header mutation if provided.
	// This ensures trace propagation works even for unsampled spans.
	t.propagator.Inject(newCtx, &headerMutationCarrier{m: mutableHeaders})

	// Only record request attributes if span is recording (sampled).
	// This avoids expensive body processing for unsampled spans.
	if span.IsRecording() {
		t.recorder.RecordRequest(span, req, body)

		// Apply header-to-attribute mapping if configured.
		if len(t.headerAttributes) > 0 {
			attrs := make([]attribute.KeyValue, 0, len(t.headerAttributes))
			for headerName, attrName := range t.headerAttributes {
				if headerValue, ok := headers[headerName]; ok {
					attrs = append(attrs, attribute.String(attrName, headerValue))
				}
			}
			if len(attrs) > 0 {
				span.SetAttributes(attrs...)
			}
		}

		return &embeddingsSpan{span: span, recorder: t.recorder}
	}

	return nil
}

// Ensure completionTracer implements [tracing.CompletionTracer].
var _ tracing.CompletionTracer = (*completionTracer)(nil)

func newCompletionTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.CompletionRecorder, headerAttributes map[string]string) tracing.CompletionTracer {
	// Check if the tracer is a no-op by checking its type.
	if _, ok := tracer.(noop.Tracer); ok {
		return tracing.NoopCompletionTracer{}
	}
	return &completionTracer{
		tracer:           tracer,
		propagator:       propagator,
		recorder:         recorder,
		headerAttributes: headerAttributes,
	}
}

type completionTracer struct {
	tracer           trace.Tracer
	recorder         tracing.CompletionRecorder
	propagator       propagation.TextMapPropagator
	headerAttributes map[string]string
}

// StartSpanAndInjectHeaders implements [tracing.CompletionTracer.StartSpanAndInjectHeaders].
func (t *completionTracer) StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, mutableHeaders *extprocv3.HeaderMutation, req *openai.CompletionRequest, body []byte) tracing.CompletionSpan {
	// Extract trace context from incoming headers.
	parentCtx := t.propagator.Extract(ctx, propagation.MapCarrier(headers))

	// Start the span with options appropriate for the semantic convention.
	spanName, opts := t.recorder.StartParams(req, body)
	newCtx, span := t.tracer.Start(parentCtx, spanName, opts...)

	// Always inject trace context into the header mutation if provided.
	// This ensures trace propagation works even for unsampled spans.
	t.propagator.Inject(newCtx, &headerMutationCarrier{m: mutableHeaders})

	// Only record request attributes if span is recording (sampled).
	// This avoids expensive body processing for unsampled spans.
	if span.IsRecording() {
		t.recorder.RecordRequest(span, req, body)

		// Apply header-to-attribute mapping if configured.
		if len(t.headerAttributes) > 0 {
			attrs := make([]attribute.KeyValue, 0, len(t.headerAttributes))
			for headerName, attrName := range t.headerAttributes {
				if headerValue, ok := headers[headerName]; ok {
					attrs = append(attrs, attribute.String(attrName, headerValue))
				}
			}
			if len(attrs) > 0 {
				span.SetAttributes(attrs...)
			}
		}

		return &completionSpan{span: span, recorder: t.recorder}
	}

	return nil
}
