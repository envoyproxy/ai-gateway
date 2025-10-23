// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	openaisdk "github.com/openai/openai-go/v2"
	openaiparam "github.com/openai/openai-go/v2/packages/param"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

var (
	imageGenStartOpts = []oteltrace.SpanStartOption{oteltrace.WithSpanKind(oteltrace.SpanKindServer)}

	imageGenReq = &openaisdk.ImageGenerateParams{
		Model:          openaisdk.ImageModelGPTImage1,
		Prompt:         "a beautiful sunset over mountains",
		Size:           openaisdk.ImageGenerateParamsSize1024x1024,
		Quality:        openaisdk.ImageGenerateParamsQualityHigh,
		ResponseFormat: openaisdk.ImageGenerateParamsResponseFormatURL,
		N:              openaiparam.NewOpt[int64](1),
	}
)

func TestImageGenerationTracer_StartSpanAndInjectHeaders(t *testing.T) {
	respBody := &openaisdk.ImagesResponse{
		Data: []openaisdk.Image{
			{URL: "https://example.com/generated-image.png"},
		},
		Size: openaisdk.ImagesResponseSize1024x1024,
		Usage: openaisdk.ImagesResponseUsage{
			InputTokens:  8,
			OutputTokens: 1056,
			TotalTokens:  1064,
		},
	}
	respBodyBytes, err := json.Marshal(respBody)
	require.NoError(t, err)
	bodyLen := len(respBodyBytes)

	reqBody, err := json.Marshal(req)
	require.NoError(t, err)
	reqBodyLen := len(reqBody)

	tests := []struct {
		name             string
		req              *openaisdk.ImageGenerateParams
		existingHeaders  map[string]string
		expectedSpanName string
		expectedAttrs    []attribute.KeyValue
		expectedTraceID  string
	}{
		{
			name:             "basic image generation request",
			req:              imageGenReq,
			existingHeaders:  map[string]string{},
			expectedSpanName: "ImageGeneration",
			expectedAttrs: []attribute.KeyValue{
				attribute.String("model", imageGenReq.Model),
				attribute.String("prompt", imageGenReq.Prompt),
				attribute.String("size", string(imageGenReq.Size)),
				attribute.String("quality", string(imageGenReq.Quality)),
				attribute.String("response_format", string(imageGenReq.ResponseFormat)),
				attribute.String("n", "1"),
				attribute.Int("reqBodyLen", reqBodyLen),
				attribute.Int("statusCode", 200),
				attribute.Int("respBodyLen", bodyLen),
			},
		},
		{
			name: "with existing trace context",
			req:  imageGenReq,
			existingHeaders: map[string]string{
				"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
			expectedSpanName: "ImageGeneration",
			expectedAttrs: []attribute.KeyValue{
				attribute.String("model", imageGenReq.Model),
				attribute.String("prompt", imageGenReq.Prompt),
				attribute.String("size", string(imageGenReq.Size)),
				attribute.String("quality", string(imageGenReq.Quality)),
				attribute.String("response_format", string(imageGenReq.ResponseFormat)),
				attribute.String("n", "1"),
				attribute.Int("reqBodyLen", reqBodyLen),
				attribute.Int("statusCode", 200),
				attribute.Int("respBodyLen", bodyLen),
			},
			expectedTraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		},
		{
			name: "multiple images request",
			req: &openaisdk.ImageGenerateParams{
				Model:          openaisdk.ImageModelGPTImage1,
				Prompt:         "a cat and a dog",
				Size:           openaisdk.ImageGenerateParamsSize512x512,
				Quality:        openaisdk.ImageGenerateParamsQualityStandard,
				ResponseFormat: openaisdk.ImageGenerateParamsResponseFormatB64JSON,
				N:              openaiparam.NewOpt[int64](2),
			},
			existingHeaders:  map[string]string{},
			expectedSpanName: "ImageGeneration",
			expectedAttrs: []attribute.KeyValue{
				attribute.String("model", openaisdk.ImageModelGPTImage1),
				attribute.String("prompt", "a cat and a dog"),
				attribute.String("size", string(openaisdk.ImageGenerateParamsSize512x512)),
				attribute.String("quality", string(openaisdk.ImageGenerateParamsQualityStandard)),
				attribute.String("response_format", string(openaisdk.ImageGenerateParamsResponseFormatB64JSON)),
				attribute.String("n", "2"),
				attribute.Int("reqBodyLen", 0), // Will be calculated in test
				attribute.Int("statusCode", 200),
				attribute.Int("respBodyLen", bodyLen),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			tp := trace.NewTracerProvider(trace.WithSyncer(exporter))

			tracer := newImageGenerationTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(), testImageGenTracerRecorder{})

			headerMutation := &extprocv3.HeaderMutation{}
			reqBody, err := json.Marshal(tt.req)
			require.NoError(t, err)

			// Update expected attributes with actual request body length
			expectedAttrs := make([]attribute.KeyValue, len(tt.expectedAttrs))
			copy(expectedAttrs, tt.expectedAttrs)
			for i, attr := range expectedAttrs {
				if attr.Key == "reqBodyLen" {
					expectedAttrs[i] = attribute.Int("reqBodyLen", len(reqBody))
					break
				}
			}

			span := tracer.StartSpanAndInjectHeaders(t.Context(),
				tt.existingHeaders,
				headerMutation,
				tt.req,
				reqBody,
			)
			require.IsType(t, &imageGenerationSpan{}, span)

			// End the span to export it.
			span.RecordResponse(respBody)
			span.EndSpan()

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			actualSpan := spans[0]

			// Check span state.
			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)

			// Check header mutation.
			traceID := actualSpan.SpanContext.TraceID().String()
			if tt.expectedTraceID != "" {
				require.Equal(t, tt.expectedTraceID, actualSpan.SpanContext.TraceID().String())
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

func TestNewImageGenerationTracer_Noop(t *testing.T) {
	// Use noop tracer.
	noopTracer := noop.Tracer{}

	tracer := newImageGenerationTracer(noopTracer, autoprop.NewTextMapPropagator(), testImageGenTracerRecorder{})

	// Verify it returns NoopTracer.
	require.IsType(t, tracing.NoopImageGenerationTracer{}, tracer)

	// Test that noop tracer doesn't create spans.
	headers := map[string]string{}
	headerMutation := &extprocv3.HeaderMutation{}
	req := &openaisdk.ImageGenerateParams{
		Model:  openaisdk.ImageModelGPTImage1,
		Prompt: "test prompt",
	}

	span := tracer.StartSpanAndInjectHeaders(t.Context(),
		headers,
		headerMutation,
		req,
		[]byte("{}"),
	)

	require.Nil(t, span)

	// Verify no headers were injected.
	require.Empty(t, headerMutation.SetHeaders)
}

func TestImageGenerationTracer_UnsampledSpan(t *testing.T) {
	// Use always_off sampler to ensure spans are not sampled.
	tracerProvider := trace.NewTracerProvider(
		trace.WithSampler(trace.NeverSample()),
	)
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })

	tracer := newImageGenerationTracer(tracerProvider.Tracer("test"), autoprop.NewTextMapPropagator(), testImageGenTracerRecorder{})

	// Start a span that won't be sampled.
	headers := map[string]string{}
	headerMutation := &extprocv3.HeaderMutation{}
	req := &openaisdk.ImageGenerateParams{
		Model:  openaisdk.ImageModelGPTImage1,
		Prompt: "test prompt",
	}

	span := tracer.StartSpanAndInjectHeaders(t.Context(),
		headers,
		headerMutation,
		req,
		[]byte("{}"),
	)

	// Span should be nil when not sampled.
	require.Nil(t, span)

	// Headers should still be injected for trace propagation.
	require.NotEmpty(t, headerMutation.SetHeaders)
}

func TestImageGenerationTracer_ErrorHandling(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))

	tracer := newImageGenerationTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(), testImageGenTracerRecorder{})

	headerMutation := &extprocv3.HeaderMutation{}
	reqBody, err := json.Marshal(imageGenReq)
	require.NoError(t, err)

	span := tracer.StartSpanAndInjectHeaders(t.Context(),
		map[string]string{},
		headerMutation,
		imageGenReq,
		reqBody,
	)
	require.IsType(t, &imageGenerationSpan{}, span)

	// Test error handling
	errorBody := []byte(`{"error":{"message":"Invalid request","type":"invalid_request_error"}}`)
	span.EndSpanOnError(400, errorBody)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	actualSpan := spans[0]

	// Check that error attributes are recorded
	expectedAttrs := []attribute.KeyValue{
		attribute.String("model", imageGenReq.Model),
		attribute.String("prompt", imageGenReq.Prompt),
		attribute.String("size", string(imageGenReq.Size)),
		attribute.String("quality", string(imageGenReq.Quality)),
		attribute.String("response_format", string(imageGenReq.ResponseFormat)),
		attribute.String("n", "1"),
		attribute.Int("reqBodyLen", len(reqBody)),
		attribute.Int("statusCode", 400),
		attribute.String("errorBody", string(errorBody)),
	}

	require.Equal(t, expectedAttrs, actualSpan.Attributes)
}

func TestImageGenerationTracer_MultipleImagesResponse(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))

	tracer := newImageGenerationTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(), testImageGenTracerRecorder{})

	headerMutation := &extprocv3.HeaderMutation{}
	reqBody, err := json.Marshal(imageGenReq)
	require.NoError(t, err)

	span := tracer.StartSpanAndInjectHeaders(t.Context(),
		map[string]string{},
		headerMutation,
		imageGenReq,
		reqBody,
	)
	require.IsType(t, &imageGenerationSpan{}, span)

	// Test with multiple images response
	multiImageResp := &openaisdk.ImagesResponse{
		Data: []openaisdk.Image{
			{URL: "https://example.com/img1.png"},
			{URL: "https://example.com/img2.png"},
			{URL: "https://example.com/img3.png"},
		},
		Size: openaisdk.ImagesResponseSize1024x1024,
		Usage: openaisdk.ImagesResponseUsage{
			InputTokens:  10,
			OutputTokens: 200,
			TotalTokens:  210,
		},
	}

	span.RecordResponse(multiImageResp)
	span.EndSpan()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	actualSpan := spans[0]

	// Check that image count is recorded correctly
	expectedAttrs := []attribute.KeyValue{
		attribute.String("model", imageGenReq.Model),
		attribute.String("prompt", imageGenReq.Prompt),
		attribute.String("size", string(imageGenReq.Size)),
		attribute.String("quality", string(imageGenReq.Quality)),
		attribute.String("response_format", string(imageGenReq.ResponseFormat)),
		attribute.String("n", "1"),
		attribute.Int("reqBodyLen", len(reqBody)),
		attribute.Int("statusCode", 200),
		attribute.Int("respBodyLen", 0), // Will be calculated

	}

	// Update respBodyLen with actual value
	for i, attr := range expectedAttrs {
		if attr.Key == "respBodyLen" {
			respBytes, _ := json.Marshal(multiImageResp)
			expectedAttrs[i] = attribute.Int("respBodyLen", len(respBytes))
			break
		}
	}

	require.Equal(t, expectedAttrs, actualSpan.Attributes)
}

var _ tracing.ImageGenerationRecorder = testImageGenTracerRecorder{}

type testImageGenTracerRecorder struct{}

func (r testImageGenTracerRecorder) StartParams(_ *openaisdk.ImageGenerateParams, _ []byte) (spanName string, opts []oteltrace.SpanStartOption) {
	return "ImageGeneration", imageGenStartOpts
}

func (r testImageGenTracerRecorder) RecordRequest(span oteltrace.Span, req *openaisdk.ImageGenerateParams, body []byte) {
	n := int64(1)
	if req.N.Valid() {
		n = req.N.Value
	}
	span.SetAttributes(
		attribute.String("model", req.Model),
		attribute.String("prompt", req.Prompt),
		attribute.String("size", string(req.Size)),
		attribute.String("quality", string(req.Quality)),
		attribute.String("response_format", string(req.ResponseFormat)),
		attribute.String("n", fmt.Sprintf("%d", n)),
		attribute.Int("reqBodyLen", len(body)),
	)
}

func (r testImageGenTracerRecorder) RecordResponse(span oteltrace.Span, resp *openaisdk.ImagesResponse) {
	span.SetAttributes(attribute.Int("statusCode", 200))
	body, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	span.SetAttributes(attribute.Int("respBodyLen", len(body)))
}

func (r testImageGenTracerRecorder) RecordResponseOnError(span oteltrace.Span, statusCode int, body []byte) {
	span.SetAttributes(attribute.Int("statusCode", statusCode))
	span.SetAttributes(attribute.String("errorBody", string(body)))
}
