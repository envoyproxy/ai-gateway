// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	openaisdk "github.com/openai/openai-go/v2"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// Ensure imageGenerationTracer implements ImageGenerationTracer.
var _ tracing.ImageGenerationTracer = (*imageGenerationTracer)(nil)

func newImageGenerationTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.ImageGenerationRecorder) tracing.ImageGenerationTracer {
	// Check if the tracer is a no-op by checking its type.
	if _, ok := tracer.(noop.Tracer); ok {
		return tracing.NoopImageGenerationTracer{}
	}
	return &imageGenerationTracer{
		tracer:     tracer,
		propagator: propagator,
		recorder:   recorder,
	}
}

type imageGenerationTracer struct {
	tracer     trace.Tracer
	recorder   tracing.ImageGenerationRecorder
	propagator propagation.TextMapPropagator
}

// StartSpanAndInjectHeaders implements ImageGenerationTracer.StartSpanAndInjectHeaders.
func (t *imageGenerationTracer) StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, mutableHeaders *extprocv3.HeaderMutation, req *openaisdk.ImageGenerateParams, body []byte) tracing.ImageGenerationSpan {
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
		return &imageGenerationSpan{span: span, recorder: t.recorder}
	}

	return nil
}
