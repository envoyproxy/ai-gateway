// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	openaisdk "github.com/openai/openai-go/v2"
	openaiparam "github.com/openai/openai-go/v2/packages/param"
)

func TestImageGenerationRecorder_RequestAttributes_SDK(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tr := tp.Tracer("test")

	recorder := NewImageGenerationRecorder(nil) // default config

	params := &openaisdk.ImageGenerateParams{
		Model:          openaisdk.ImageModelGPTImage1,
		Prompt:         "a hummingbird",
		Size:           openaisdk.ImageGenerateParamsSize1024x1024,
		Quality:        openaisdk.ImageGenerateParamsQualityHigh,
		ResponseFormat: openaisdk.ImageGenerateParamsResponseFormatB64JSON,
		N:              openaiparam.NewOpt[int64](2),
	}

	spanName, opts := recorder.StartParams(params, []byte("{}"))
	require.Equal(t, "ImageGeneration", spanName)

	_, span := tr.Start(t.Context(), spanName, opts...)
	recorder.RecordRequest(span, params, []byte(`{"prompt":"a hummingbird"}`))
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	got := spans[0]

	// Verify a subset of attributes were recorded
	attrs := attributesToMap(got.Attributes)
	require.Equal(t, "openai", attrs["llm.system"])                                  // LLMSystemOpenAI
	require.Equal(t, string(openaisdk.ImageModelGPTImage1), attrs["llm.model_name"]) // model
	require.Equal(t, "{\"prompt\":\"a hummingbird\"}", attrs["input.value"])         // input body json
	require.Equal(t, "image_generation", attrs["gen_ai.operation.name"])             // operation name
	require.Equal(t, "1024x1024", attrs["gen_ai.image.size"])                        // size
	require.Equal(t, "high", attrs["gen_ai.image.quality"])                          // quality
	require.Equal(t, "b64_json", attrs["gen_ai.image.response_format"])              // response_format
	require.Equal(t, "2", attrs["gen_ai.image.n"])                                   // n
}

func TestImageGenerationRecorder_ResponseAttributes_SDK(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tr := tp.Tracer("test")

	recorder := NewImageGenerationRecorder(nil)

	_, span := tr.Start(t.Context(), "ImageGeneration")
	resp := &openaisdk.ImagesResponse{
		Data: []openaisdk.Image{{URL: "https://example.com/img.png"}},
		Size: openaisdk.ImagesResponseSize1024x1024,
	}
	recorder.RecordResponse(span, resp)
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	got := spans[0]
	attrs := attributesToMap(got.Attributes)
	// Count and urls should be present
	require.Equal(t, "1", attrs["gen_ai.image.count"])
	require.Equal(t, "https://example.com/img.png", attrs["gen_ai.image.urls"])
}

// attributesToMap converts attribute KeyValue to a simple map for assertions.
func attributesToMap(kvs []attribute.KeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		switch kv.Value.Type() {
		case attribute.STRING:
			m[string(kv.Key)] = kv.Value.AsString()
		case attribute.BOOL:
			m[string(kv.Key)] = strconv.FormatBool(kv.Value.AsBool())
		case attribute.INT64:
			m[string(kv.Key)] = strconv.FormatInt(kv.Value.AsInt64(), 10)
		case attribute.FLOAT64:
			m[string(kv.Key)] = strconv.FormatFloat(kv.Value.AsFloat64(), 'f', -1, 64)
		default:
			m[string(kv.Key)] = kv.Value.AsString()
		}
	}
	return m
}

var _ tracing.ImageGenerationRecorder = (*ImageGenerationRecorder)(nil)
