// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"
	"encoding/json"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	cohere "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	apiTracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

var (
	rerankStartOpts = []oteltrace.SpanStartOption{oteltrace.WithSpanKind(oteltrace.SpanKindServer)}
	rerankReq       = &cohere.RerankV2Request{
		Model:     "rerank-english-v3",
		Query:     "reset password",
		TopN:      intPtr(3),
		Documents: []string{"doc1", "doc2"},
	}
)

type testRerankTracerRecorder struct{}

func (testRerankTracerRecorder) StartParams(*cohere.RerankV2Request, []byte) (string, []oteltrace.SpanStartOption) {
	return "Rerank", rerankStartOpts
}

func (testRerankTracerRecorder) RecordRequest(span oteltrace.Span, req *cohere.RerankV2Request, body []byte) {
	span.SetAttributes(
		attribute.String("model", req.Model),
		attribute.String("query", req.Query),
		attribute.Int("top_n", *req.TopN),
		attribute.Int("reqBodyLen", len(body)),
	)
}

func (testRerankTracerRecorder) RecordResponse(span oteltrace.Span, resp *cohere.RerankV2Response) {
	span.SetAttributes(attribute.Int("statusCode", 200))
	b, _ := json.Marshal(resp)
	span.SetAttributes(attribute.Int("respBodyLen", len(b)))
}

func (testRerankTracerRecorder) RecordResponseOnError(span oteltrace.Span, statusCode int, body []byte) {
	span.SetAttributes(attribute.Int("statusCode", statusCode))
	span.SetAttributes(attribute.String("errorBody", string(body)))
}

func TestRerankTracer_StartSpanAndInjectHeaders(t *testing.T) {
	respBody := &cohere.RerankV2Response{
		Results: []*cohere.RerankV2Result{{Index: 1, RelevanceScore: 0.9}},
	}
	respBodyBytes, _ := json.Marshal(respBody)

	reqBody, _ := json.Marshal(rerankReq)

	tests := []struct {
		name             string
		req              *cohere.RerankV2Request
		existingHeaders  map[string]string
		headerAttrs      map[string]string
		expectedSpanName string
		expectedAttrs    []attribute.KeyValue
		expectedTraceID  string
	}{
		{
			name:             "basic rerank request",
			req:              rerankReq,
			existingHeaders:  map[string]string{"x-session-id": "abc"},
			headerAttrs:      map[string]string{"x-session-id": "session.id"},
			expectedSpanName: "Rerank",
			expectedAttrs: []attribute.KeyValue{
				attribute.String("model", rerankReq.Model),
				attribute.String("query", rerankReq.Query),
				attribute.Int("top_n", *rerankReq.TopN),
				attribute.Int("reqBodyLen", len(reqBody)),
				attribute.String("session.id", "abc"),
				attribute.Int("statusCode", 200),
				attribute.Int("respBodyLen", len(respBodyBytes)),
			},
		},
		{
			name: "with existing trace context",
			req:  rerankReq,
			existingHeaders: map[string]string{
				"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
			headerAttrs:      nil,
			expectedSpanName: "Rerank",
			expectedAttrs: []attribute.KeyValue{
				attribute.String("model", rerankReq.Model),
				attribute.String("query", rerankReq.Query),
				attribute.Int("top_n", *rerankReq.TopN),
				attribute.Int("reqBodyLen", len(reqBody)),
				attribute.Int("statusCode", 200),
				attribute.Int("respBodyLen", len(respBodyBytes)),
			},
			expectedTraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			tp := trace.NewTracerProvider(trace.WithSyncer(exporter))

			tracer := newRerankTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(), testRerankTracerRecorder{}, tt.headerAttrs)

			headerMutation := &extprocv3.HeaderMutation{}
			reqBody, _ := json.Marshal(tt.req)

			span := tracer.StartSpanAndInjectHeaders(t.Context(),
				tt.existingHeaders,
				headerMutation,
				tt.req,
				reqBody,
			)
			require.IsType(t, &rerankSpan{}, span)

			// End the span to export it.
			span.RecordResponse(respBody)
			span.EndSpan()

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			actualSpan := spans[0]

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)

			traceID := actualSpan.SpanContext.TraceID().String()
			if tt.expectedTraceID != "" {
				require.Equal(t, tt.expectedTraceID, traceID)
			}
			spanID := actualSpan.SpanContext.SpanID().String()
			require.Equal(t, &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:      "traceparent",
							RawValue: []byte("00-" + traceID + "-" + spanID + "-01"),
						},
					},
				},
			}, headerMutation)
		})
	}
}

func TestNewRerankTracer_Noop(t *testing.T) {
	noopTracer := noop.Tracer{}
	tracer := newRerankTracer(noopTracer, autoprop.NewTextMapPropagator(), testRerankTracerRecorder{}, nil)
	require.IsType(t, apiTracing.NoopRerankTracer{}, tracer)

	headers := map[string]string{}
	headerMutation := &extprocv3.HeaderMutation{}
	req := &cohere.RerankV2Request{Model: "rerank-english-v3", Query: "q", Documents: []string{"a"}}

	span := tracer.StartSpanAndInjectHeaders(context.Background(),
		headers,
		headerMutation,
		req,
		[]byte("{}"),
	)
	require.Nil(t, span)
	require.Empty(t, headerMutation.SetHeaders)
}

func TestRerankTracer_UnsampledSpan(t *testing.T) {
	// Use always_off sampler to ensure spans are not sampled.
	tp := trace.NewTracerProvider(trace.WithSampler(trace.NeverSample()))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := newRerankTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(), testRerankTracerRecorder{}, nil)

	headers := map[string]string{}
	headerMutation := &extprocv3.HeaderMutation{}
	req := &cohere.RerankV2Request{Model: "rerank-english-v3", Query: "q", Documents: []string{"a"}}

	span := tracer.StartSpanAndInjectHeaders(context.Background(),
		headers,
		headerMutation,
		req,
		[]byte("{}"),
	)
	require.Nil(t, span)
	// Headers should still be injected for trace propagation.
	require.NotEmpty(t, headerMutation.SetHeaders)
}

func intPtr(v int) *int { return &v }
