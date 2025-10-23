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
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// imageGeneration is the implementation for the image generation AI Gateway metrics.
type imageGeneration struct {
	baseMetrics
}

// ImageGenerationMetrics is the interface for the image generation AI Gateway metrics.
type ImageGenerationMetrics interface {
	// StartRequest initializes timing for a new request.
	StartRequest(headers map[string]string)
	// SetOriginalModel sets the original model from the incoming request body before any virtualization applies.
	// This is usually called after parsing the request body. Example: dall-e-3
	SetOriginalModel(originalModel internalapi.OriginalModel)
	// SetRequestModel sets the request model name.
	SetRequestModel(requestModel internalapi.RequestModel)
	// SetResponseModel sets the response model name.
	SetResponseModel(responseModel internalapi.ResponseModel)
	// SetBackend sets the selected backend when the routing decision has been made. This is usually called
	// after parsing the request body to determine the model and invoke the routing logic.
	SetBackend(backend *filterapi.Backend)

	// RecordTokenUsage records token usage metrics (image gen typically 0, but supported).
	RecordTokenUsage(ctx context.Context, inputTokens, outputTokens uint32, requestHeaderLabelMapping map[string]string)
	// RecordRequestCompletion records latency metrics for the entire request.
	RecordRequestCompletion(ctx context.Context, success bool, requestHeaderLabelMapping map[string]string)
	// RecordImageGeneration records metrics specific to image generation (request duration only).
	RecordImageGeneration(ctx context.Context, requestHeaderLabelMapping map[string]string)
}

// ImageGenerationMetricsFactory is a closure that creates a new ImageGenerationMetrics instance.
type ImageGenerationMetricsFactory func() ImageGenerationMetrics

// NewImageGenerationFactory returns a closure to create a new ImageGenerationMetrics instance.
func NewImageGenerationFactory(meter metric.Meter, requestHeaderLabelMapping map[string]string) ImageGenerationMetricsFactory {
	b := baseMetricsFactory{metrics: newGenAI(meter), requestHeaderAttributeMapping: requestHeaderLabelMapping}
	return func() ImageGenerationMetrics {
		return &imageGeneration{
			baseMetrics: b.newBaseMetrics(genaiOperationImageGeneration),
		}
	}
}

// StartRequest initializes timing for a new request.
func (i *imageGeneration) StartRequest(headers map[string]string) {
	i.baseMetrics.StartRequest(headers)
}

// SetOriginalModel sets the original model from the incoming request body before any virtualization applies.
func (i *imageGeneration) SetOriginalModel(originalModel internalapi.OriginalModel) {
	i.baseMetrics.SetOriginalModel(originalModel)
}

// SetRequestModel sets the request model for the request.
func (i *imageGeneration) SetRequestModel(requestModel internalapi.RequestModel) {
	i.baseMetrics.SetRequestModel(requestModel)
}

// SetResponseModel sets the response model for the request.
func (i *imageGeneration) SetResponseModel(responseModel internalapi.ResponseModel) {
	i.baseMetrics.SetResponseModel(responseModel)
}

// RecordTokenUsage implements [ImageGeneration.RecordTokenUsage].
func (i *imageGeneration) RecordTokenUsage(ctx context.Context, inputTokens, outputTokens uint32, requestHeaders map[string]string) {
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
	// Note: We don't record totalTokens separately as it causes double counting.
	// The OTEL spec only defines "input" and "output" token types.
}

// RecordImageGeneration implements [ImageGeneration.RecordImageGeneration].
func (i *imageGeneration) RecordImageGeneration(ctx context.Context, requestHeaders map[string]string) {
	attrs := i.buildBaseAttributes(requestHeaders)
	// Record request duration with base attributes only for consistency with other operations/tests.
	i.metrics.requestLatency.Record(ctx, time.Since(i.requestStart).Seconds(), metric.WithAttributeSet(attrs))
}

// GetTimeToGenerate returns the time taken to generate images.
func (i *imageGeneration) GetTimeToGenerate() time.Duration {
	return time.Since(i.requestStart)
}
