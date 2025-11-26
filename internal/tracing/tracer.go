// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	openaisdk "github.com/openai/openai-go/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// spanFactory is a function type that creates a new SpanT given a trace.Span and a Recorder.
type spanFactory[ReqT any, ChunkT any, RespT any, Recorder tracing.SpanRecorder[ReqT, ChunkT, RespT], SpanT any] func(trace.Span, Recorder) SpanT

// requestTracerImpl implements RequestTracer for various request and span types.
type requestTracerImpl[ReqT any, ChunkT any, RespT any, Recorder tracing.SpanRecorder[ReqT, ChunkT, RespT], SpanT any] struct {
	tracer           trace.Tracer
	propagator       propagation.TextMapPropagator
	recorder         Recorder
	headerAttributes map[string]string
	newSpan          spanFactory[ReqT, ChunkT, RespT, Recorder, SpanT]
}

var (
	_ tracing.ChatCompletionTracer  = (*chatCompletionTracer)(nil)
	_ tracing.EmbeddingsTracer      = (*embeddingsTracer)(nil)
	_ tracing.CompletionTracer      = (*completionTracer)(nil)
	_ tracing.ImageGenerationTracer = (*imageGenerationTracer)(nil)
	_ tracing.RerankTracer          = (*rerankTracer)(nil)
)

type (
	chatCompletionTracer  = requestTracerImpl[openai.ChatCompletionRequest, openai.ChatCompletionResponseChunk, openai.ChatCompletionResponse, tracing.ChatCompletionRecorder, tracing.ChatCompletionSpan]
	embeddingsTracer      = requestTracerImpl[openai.EmbeddingRequest, struct{}, openai.EmbeddingResponse, tracing.EmbeddingsRecorder, tracing.EmbeddingsSpan]
	completionTracer      = requestTracerImpl[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse, tracing.CompletionRecorder, tracing.CompletionSpan]
	imageGenerationTracer = requestTracerImpl[openaisdk.ImageGenerateParams, struct{}, openaisdk.ImagesResponse, tracing.ImageGenerationRecorder, tracing.ImageGenerationSpan]
	rerankTracer          = requestTracerImpl[cohereschema.RerankV2Request, struct{}, cohereschema.RerankV2Response, tracing.RerankRecorder, tracing.RerankSpan]
)

func newRequestTracer[ReqT any, ChunkT any, RespT any, Recorder tracing.SpanRecorder[ReqT, ChunkT, RespT], SpanT any](
	tracer trace.Tracer,
	propagator propagation.TextMapPropagator,
	recorder Recorder,
	headerAttributes map[string]string,
	newSpan spanFactory[ReqT, ChunkT, RespT, Recorder, SpanT],
) tracing.RequestTracer[ReqT, SpanT] {
	if _, ok := tracer.(noop.Tracer); ok {
		return tracing.NoopTracer[ReqT, SpanT]{}
	}
	return &requestTracerImpl[ReqT, ChunkT, RespT, Recorder, SpanT]{
		tracer:           tracer,
		propagator:       propagator,
		recorder:         recorder,
		headerAttributes: headerAttributes,
		newSpan:          newSpan,
	}
}

func (t *requestTracerImpl[ReqT, ChunkT, RespT, Recorder, SpanT]) StartSpanAndInjectHeaders(
	ctx context.Context,
	headers map[string]string,
	mutableHeaders *extprocv3.HeaderMutation,
	req *ReqT,
	body []byte,
) SpanT {
	parentCtx := t.propagator.Extract(ctx, propagation.MapCarrier(headers))
	spanName, opts := t.recorder.StartParams(req, body)
	newCtx, span := t.tracer.Start(parentCtx, spanName, opts...)

	t.propagator.Inject(newCtx, &headerMutationCarrier{m: mutableHeaders})

	var zero SpanT
	if !span.IsRecording() {
		return zero
	}

	t.recorder.RecordRequest(span, req, body)

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

	return t.newSpan(span, t.recorder)
}

func newChatCompletionTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.ChatCompletionRecorder, headerAttributes map[string]string) tracing.ChatCompletionTracer {
	return newRequestTracer(
		tracer,
		propagator,
		recorder,
		headerAttributes,
		func(span trace.Span, recorder tracing.ChatCompletionRecorder) tracing.ChatCompletionSpan {
			return &chatCompletionSpan{span: span, recorder: recorder}
		},
	)
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

func newEmbeddingsTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.EmbeddingsRecorder, headerAttributes map[string]string) tracing.EmbeddingsTracer {
	return newRequestTracer(
		tracer,
		propagator,
		recorder,
		headerAttributes,
		func(span trace.Span, recorder tracing.EmbeddingsRecorder) tracing.EmbeddingsSpan {
			return &embeddingsSpan{span: span, recorder: recorder}
		},
	)
}

func newCompletionTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.CompletionRecorder, headerAttributes map[string]string) tracing.CompletionTracer {
	return newRequestTracer(
		tracer,
		propagator,
		recorder,
		headerAttributes,
		func(span trace.Span, recorder tracing.CompletionRecorder) tracing.CompletionSpan {
			return &completionSpan{span: span, recorder: recorder}
		},
	)
}

func newImageGenerationTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.ImageGenerationRecorder) tracing.ImageGenerationTracer {
	return newRequestTracer(
		tracer,
		propagator,
		recorder,
		nil,
		func(span trace.Span, recorder tracing.ImageGenerationRecorder) tracing.ImageGenerationSpan {
			return &imageGenerationSpan{span: span, recorder: recorder}
		},
	)
}

func newRerankTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.RerankRecorder, headerAttributes map[string]string) tracing.RerankTracer {
	return newRequestTracer(
		tracer,
		propagator,
		recorder,
		headerAttributes,
		func(span trace.Span, recorder tracing.RerankRecorder) tracing.RerankSpan {
			return &rerankSpan{span: span, recorder: recorder}
		},
	)
}
