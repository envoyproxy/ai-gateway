// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package openai provides OpenInference semantic conventions hooks for
// OpenAI instrumentation used by the ExtProc router filter.
package openai

import (
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

// ImageGenerationRecorder implements recorders for OpenInference image generation spans.
type ImageGenerationRecorder struct {
	traceConfig *openinference.TraceConfig
}

// NewImageGenerationRecorderFromEnv creates an api.ImageGenerationRecorder
// from environment variables using the OpenInference configuration specification.
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewImageGenerationRecorderFromEnv() tracing.ImageGenerationRecorder {
	return NewImageGenerationRecorder(nil)
}

// NewImageGenerationRecorder creates a tracing.ImageGenerationRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewImageGenerationRecorder(config *openinference.TraceConfig) tracing.ImageGenerationRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &ImageGenerationRecorder{traceConfig: config}
}

// startOpts sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var imageGenStartOpts = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// StartParams implements the same method as defined in tracing.ImageGenerationRecorder.
func (r *ImageGenerationRecorder) StartParams(*openai.ImageGenerationRequest, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "ImageGeneration", imageGenStartOpts
}

// RecordRequest implements the same method as defined in tracing.ImageGenerationRecorder.
func (r *ImageGenerationRecorder) RecordRequest(span trace.Span, req *openai.ImageGenerationRequest, body []byte) {
	span.SetAttributes(buildImageGenerationRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracing.ImageGenerationRecorder.
func (r *ImageGenerationRecorder) RecordResponse(span trace.Span, resp *openai.ImageGenerationResponse) {
	// Set output attributes.
	var attrs []attribute.KeyValue
	attrs = buildImageGenerationResponseAttributes(resp, r.traceConfig)

	bodyString := openinference.RedactedValue
	if !r.traceConfig.HideOutputs {
		marshaled, err := json.Marshal(resp)
		if err == nil {
			bodyString = string(marshaled)
		}
	}
	attrs = append(attrs, attribute.String(openinference.OutputValue, bodyString))
	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracing.ImageGenerationRecorder.
func (r *ImageGenerationRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	recordImageGenerationResponseError(span, statusCode, string(body))
}

// buildImageGenerationRequestAttributes builds OpenInference attributes from the image generation request.
func buildImageGenerationRequestAttributes(req *openai.ImageGenerationRequest, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
		attribute.String(openinference.LLMModelName, req.Model),
	}

	if config.HideInputs {
		attrs = append(attrs, attribute.String(openinference.InputValue, openinference.RedactedValue))
	} else {
		attrs = append(attrs, attribute.String(openinference.InputValue, body))
		attrs = append(attrs, attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON))
	}

	// Add image generation specific attributes
	attrs = append(attrs, attribute.String("gen_ai.operation.name", "image_generation"))
	attrs = append(attrs, attribute.String("gen_ai.image.prompt", req.Prompt))
	attrs = append(attrs, attribute.String("gen_ai.image.size", req.Size))
	attrs = append(attrs, attribute.String("gen_ai.image.quality", req.Quality))
	attrs = append(attrs, attribute.String("gen_ai.image.style", req.Style))
	attrs = append(attrs, attribute.String("gen_ai.image.response_format", req.ResponseFormat))
	if req.N != nil {
		attrs = append(attrs, attribute.Int("gen_ai.image.n", *req.N))
	}

	return attrs
}

// buildImageGenerationResponseAttributes builds OpenInference attributes from the image generation response.
func buildImageGenerationResponseAttributes(resp *openai.ImageGenerationResponse, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.response.model", resp.Model),
		attribute.Int("gen_ai.image.count", len(resp.Data)),
	}

	// Add image URLs if not hidden
	if !config.HideOutputs && resp.Data != nil {
		urls := make([]string, 0, len(resp.Data))
		for _, data := range resp.Data {
			if data.URL != nil {
				urls = append(urls, *data.URL)
			}
		}
		if len(urls) > 0 {
			// Join URLs with comma for attribute storage
			urlStr := ""
			for i, url := range urls {
				if i > 0 {
					urlStr += ","
				}
				urlStr += url
			}
			attrs = append(attrs, attribute.String("gen_ai.image.urls", urlStr))
		}
	}

	return attrs
}

// recordImageGenerationResponseError records error attributes to the span.
func recordImageGenerationResponseError(span trace.Span, statusCode int, body string) {
	span.SetStatus(codes.Error, "")
	span.SetAttributes(
		attribute.Int("http.status_code", statusCode),
		attribute.String("gen_ai.error.message", body),
	)
}
