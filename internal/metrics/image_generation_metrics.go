// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// imageGeneration is the implementation for the image generation AI Gateway metrics.
type imageGeneration struct {
	baseMetrics
}

// ImageGenerationMetrics is the interface for the image generation AI Gateway metrics.
type ImageGenerationMetrics interface {
	// StartRequest initializes timing for a new request.
	StartRequest(headers map[string]string)
	// SetModel sets the model the request. This is usually called after parsing the request body .
	SetModel(model string)
	// SetBackend sets the selected backend when the routing decision has been made. This is usually called
	// after parsing the request body to determine the model and invoke the routing logic.
	SetBackend(backend *filterapi.Backend)

	// RecordTokenUsage records token usage metrics (for image generation, this will typically be 0).
	RecordTokenUsage(ctx context.Context, inputTokens, outputTokens, totalTokens uint32, requestHeaderLabelMapping map[string]string)
	// RecordRequestCompletion records latency metrics for the entire request.
	RecordRequestCompletion(ctx context.Context, success bool, requestHeaderLabelMapping map[string]string)
	// RecordImageGeneration records metrics specific to image generation.
	RecordImageGeneration(ctx context.Context, imageCount int, model, size string, requestHeaderLabelMapping map[string]string)
}

// NewImageGeneration creates a new ImageGenerationMetrics instance.
func NewImageGeneration(meter metric.Meter, requestHeaderLabelMapping map[string]string) ImageGenerationMetrics {
	return &imageGeneration{
		baseMetrics: newBaseMetrics(meter, genaiOperationImageGeneration, requestHeaderLabelMapping),
	}
}

// StartRequest initializes timing for a new request.
func (i *imageGeneration) StartRequest(headers map[string]string) {
	i.baseMetrics.StartRequest(headers)
}

// RecordTokenUsage implements [ImageGeneration.RecordTokenUsage].
func (i *imageGeneration) RecordTokenUsage(ctx context.Context, inputTokens, outputTokens, totalTokens uint32, requestHeaders map[string]string) {
	attrs := i.buildBaseAttributes(requestHeaders)

	// For image generation, token usage is typically 0, but we still record it for consistency
	i.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
	i.metrics.tokenUsage.Record(ctx, float64(outputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput)),
	)
	i.metrics.tokenUsage.Record(ctx, float64(totalTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeTotal)),
	)
}

// RecordImageGeneration implements [ImageGeneration.RecordImageGeneration].
func (i *imageGeneration) RecordImageGeneration(ctx context.Context, imageCount int, model, size string, requestHeaders map[string]string) {
	attrs := i.buildBaseAttributes(requestHeaders)

	// Add image-specific attributes
	extendedAttrs := attribute.NewSet(
		append(attrs.ToSlice(),
			attribute.Key("gen_ai.image.count").Int(imageCount),
			attribute.Key("gen_ai.image.model").String(model),
			attribute.Key("gen_ai.image.size").String(size),
		)...,
	)

	// Record image generation metrics
	i.metrics.requestLatency.Record(ctx, time.Since(i.requestStart).Seconds(), metric.WithAttributeSet(extendedAttrs))
}

// GetTimeToGenerate returns the time taken to generate images.
func (i *imageGeneration) GetTimeToGenerate() time.Duration {
	return time.Since(i.requestStart)
}
