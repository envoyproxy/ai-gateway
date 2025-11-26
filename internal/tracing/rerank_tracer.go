// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// Ensure rerankTracer implements [tracing.RerankTracer].
var _ tracing.RerankTracer = (*rerankTracer)(nil)

func newRerankTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.RerankRecorder, headerAttributes map[string]string) tracing.RerankTracer {
	// Check if the tracer is a no-op by checking its type.
	if _, ok := tracer.(noop.Tracer); ok {
		return tracing.NoopTracer[cohereschema.RerankV2Request, tracing.RerankSpan]{}
	}
	return &rerankTracer{
		tracer:           tracer,
		propagator:       propagator,
		recorder:         recorder,
		headerAttributes: headerAttributes,
	}
}

type rerankTracer struct {
	tracer           trace.Tracer
	recorder         tracing.RerankRecorder
	propagator       propagation.TextMapPropagator
	headerAttributes map[string]string
}

// StartSpanAndInjectHeaders implements [tracing.RerankTracer.StartSpanAndInjectHeaders].
func (t *rerankTracer) StartSpanAndInjectHeaders(ctx context.Context, headers map[string]string, mutableHeaders *extprocv3.HeaderMutation, req *cohereschema.RerankV2Request, body []byte) tracing.RerankSpan {
	// Extract trace context from incoming headers.
	parentCtx := t.propagator.Extract(ctx, propagation.MapCarrier(headers))

	// Start the span with options appropriate for the semantic convention.
	spanName, opts := t.recorder.StartParams(req, body)
	newCtx, span := t.tracer.Start(parentCtx, spanName, opts...)

	// Always inject trace context into the header mutation if provided.
	// This ensures trace propagation works even for unsampled spans.
	t.propagator.Inject(newCtx, &headerMutationCarrier{m: mutableHeaders})

	// Only record request attributes if span is recording (sampled).
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
		return &rerankSpan{span: span, recorder: t.recorder}
	}

	return nil
}
