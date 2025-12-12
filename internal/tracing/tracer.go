// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// spanFactory is a function type that creates a new SpanT given a trace.Span and a Recorder.
type spanFactory[ReqT any, RespT any, RespChunkT any] func(trace.Span, tracing.SpanRecorder[ReqT, RespT, RespChunkT]) tracing.Span[RespT, RespChunkT]

// requestTracerImpl implements RequestTracer for various request and span types.
type requestTracerImpl[ReqT any, RespT any, RespChunkT any] struct {
	tracer           trace.Tracer
	propagator       propagation.TextMapPropagator
	recorder         tracing.SpanRecorder[ReqT, RespT, RespChunkT]
	headerAttributes map[string]string
	newSpan          spanFactory[ReqT, RespT, RespChunkT]
}

var (
	_ tracing.ChatCompletionTracer  = (*chatCompletionTracer)(nil)
	_ tracing.EmbeddingsTracer      = (*embeddingsTracer)(nil)
	_ tracing.CompletionTracer      = (*completionTracer)(nil)
	_ tracing.ImageGenerationTracer = (*imageGenerationTracer)(nil)
	_ tracing.ResponsesTracer       = (*responsesTracer)(nil)
	_ tracing.RerankTracer          = (*rerankTracer)(nil)
)

type (
	chatCompletionTracer  = requestTracerImpl[openai.ChatCompletionRequest, openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk]
	embeddingsTracer      = requestTracerImpl[openai.EmbeddingRequest, openai.EmbeddingResponse, struct{}]
	completionTracer      = requestTracerImpl[openai.CompletionRequest, openai.CompletionResponse, openai.CompletionResponse]
	imageGenerationTracer = requestTracerImpl[openai.ImageGenerationRequest, openai.ImageGenerationResponse, struct{}]
	responsesTracer       = requestTracerImpl[openai.ResponseRequest, openai.Response, openai.ResponseCompletedEvent]
	rerankTracer          = requestTracerImpl[cohereschema.RerankV2Request, cohereschema.RerankV2Response, struct{}]
)

func newRequestTracer[ReqT any, RespT any, RespChunkT any](
	tracer trace.Tracer,
	propagator propagation.TextMapPropagator,
	recorder tracing.SpanRecorder[ReqT, RespT, RespChunkT],
	headerAttributes map[string]string,
	newSpan spanFactory[ReqT, RespT, RespChunkT],
) tracing.RequestTracer[ReqT, RespT, RespChunkT] {
	if _, ok := tracer.(noop.Tracer); ok {
		return tracing.NoopTracer[ReqT, RespT, RespChunkT]{}
	}
	return &requestTracerImpl[ReqT, RespT, RespChunkT]{
		tracer:           tracer,
		propagator:       propagator,
		recorder:         recorder,
		headerAttributes: headerAttributes,
		newSpan:          newSpan,
	}
}

func (t *requestTracerImpl[ReqT, RespT, ChunkT]) StartSpanAndInjectHeaders(
	ctx context.Context,
	headers map[string]string,
	carrier propagation.TextMapCarrier,
	req *ReqT,
	body []byte,
) tracing.Span[RespT, ChunkT] {
	parentCtx := t.propagator.Extract(ctx, propagation.MapCarrier(headers))
	spanName, opts := t.recorder.StartParams(req, body)
	newCtx, span := t.tracer.Start(parentCtx, spanName, opts...)

	t.propagator.Inject(newCtx, carrier)

	var zero tracing.Span[RespT, ChunkT]
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

func newResponsesTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.ResponsesRecorder, headerAttributes map[string]string) tracing.ResponsesTracer {
	return newRequestTracer(
		tracer,
		propagator,
		recorder,
		headerAttributes,
		func(span trace.Span, recorder tracing.ResponsesRecorder) tracing.ResponsesSpan {
			return &responsesSpan{span: span, recorder: recorder}
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

func newMessageTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, recorder tracing.MessageRecorder, headerAttributes map[string]string) tracing.MessageTracer {
	return newRequestTracer(
		tracer,
		propagator,
		recorder,
		headerAttributes,
		func(span trace.Span, recorder tracing.MessageRecorder) tracing.MessageSpan {
			return &messageSpan{span: span, recorder: recorder}
		},
	)
}
