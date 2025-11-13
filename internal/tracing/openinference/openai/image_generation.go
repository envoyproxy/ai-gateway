// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package openai provides OpenInference semantic conventions hooks for
// OpenAI instrumentation used by the ExtProc router filter.
package openai

import (
	"encoding/json"

	openaisdk "github.com/openai/openai-go/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

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
func (r *ImageGenerationRecorder) StartParams(*openaisdk.ImageGenerateParams, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "ImagesResponse", imageGenStartOpts
}

// RecordRequest implements the same method as defined in tracing.ImageGenerationRecorder.
func (r *ImageGenerationRecorder) RecordRequest(span trace.Span, req *openaisdk.ImageGenerateParams, body []byte) {
	span.SetAttributes(buildImageGenerationRequestAttributes(req, string(body), r.traceConfig)...)
}

// RecordResponse implements the same method as defined in tracing.ImageGenerationRecorder.
func (r *ImageGenerationRecorder) RecordResponse(span trace.Span, resp *openaisdk.ImagesResponse) {
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
	// Match ChatCompletion recorder: include output MIME type and value
	attrs = append(attrs, attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON))
	attrs = append(attrs, attribute.String(openinference.OutputValue, bodyString))
	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}

// RecordResponseOnError implements the same method as defined in tracing.ImageGenerationRecorder.
func (r *ImageGenerationRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

// buildImageGenerationRequestAttributes builds OpenInference attributes from the image generation request.
func buildImageGenerationRequestAttributes(_ *openaisdk.ImageGenerateParams, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
	}

	if config.HideInputs {
		attrs = append(attrs, attribute.String(openinference.InputValue, openinference.RedactedValue))
	} else {
		attrs = append(attrs, attribute.String(openinference.InputValue, body))
		attrs = append(attrs, attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON))
	}

	if !config.HideLLMInvocationParameters {
		attrs = append(attrs, attribute.String(openinference.LLMInvocationParameters, body))
	}

	return attrs
}

// buildImageGenerationResponseAttributes builds OpenInference attributes from the image generation response.
func buildImageGenerationResponseAttributes(_ *openaisdk.ImagesResponse, _ *openinference.TraceConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{}

	// No image-specific response attributes

	return attrs
}
