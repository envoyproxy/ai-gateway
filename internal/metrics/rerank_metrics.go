// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// rerank is the implementation for the rerank AI Gateway metrics.
type rerank struct {
	baseMetrics
}

// RerankMetricsFactory is a closure that creates a new RerankMetrics instance.
type RerankMetricsFactory func() RerankMetrics

// RerankMetrics is the interface for the rerank AI Gateway metrics.
type RerankMetrics interface {
	// StartRequest initializes timing for a new request.
	StartRequest(headers map[string]string)
	// SetOriginalModel sets the original model from the incoming request body before any virtualization applies.
	// This is usually called after parsing the request body. Example: rerank-english-v3
	SetOriginalModel(originalModel internalapi.OriginalModel)
	// SetRequestModel sets the model from the request. This is usually called after parsing the request body.
	// Example: rerank-english-v3
	SetRequestModel(requestModel internalapi.RequestModel)
	// SetResponseModel sets the model that ultimately generated the response.
	// Example: rerank-english-v3-2025-02-18
	SetResponseModel(responseModel internalapi.ResponseModel)
	// SetBackend sets the selected backend when the routing decision has been made. This is usually called
	// after parsing the request body to determine the model and invoke the routing logic.
	SetBackend(backend *filterapi.Backend)

	// RecordTokenUsage records token usage metrics for rerank (only input tokens are relevant).
	RecordTokenUsage(ctx context.Context, inputTokens uint32, requestHeaderLabelMapping map[string]string)
	// RecordRequestCompletion records latency metrics for the entire request.
	RecordRequestCompletion(ctx context.Context, success bool, requestHeaderLabelMapping map[string]string)
}

// NewRerankFactory returns a closure to create a new Rerank instance.
func NewRerankFactory(meter metric.Meter, requestHeaderAttributeMapping map[string]string) RerankMetricsFactory {
	b := baseMetricsFactory{metrics: newGenAI(meter), requestHeaderAttributeMapping: requestHeaderAttributeMapping}
	return func() RerankMetrics {
		return &rerank{
			baseMetrics: b.newBaseMetrics(genaiOperationRerank),
		}
	}
}

// RecordTokenUsage implements [RerankMetrics.RecordTokenUsage].
func (r *rerank) RecordTokenUsage(ctx context.Context, inputTokens uint32, requestHeaders map[string]string) {
	attrs := r.buildBaseAttributes(requestHeaders)

	// Rerank only consumes input tokens to compute ranking scores.
	r.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
}
