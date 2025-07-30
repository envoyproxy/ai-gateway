// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/envoyproxy/ai-gateway/filterapi/x"
)

// embeddings is the implementation for the embeddings AI Gateway metrics.
type embeddings struct {
	baseMetrics
}

// NewEmbeddings creates a new Embeddings instance.
func NewEmbeddings(meter metric.Meter) x.EmbeddingsMetrics {
	return &embeddings{
		baseMetrics: newBaseMetrics(meter, genaiOperationEmbedding),
	}
}

// NewEmbeddingsWithHeaderMapping creates a new Embeddings instance with header mapping support.
func NewEmbeddingsWithHeaderMapping(meter metric.Meter, metricsRequestHeaderLabelMapping map[string]string) x.EmbeddingsMetrics {
	return &embeddings{
		baseMetrics: newBaseMetricsWithHeaderMapping(meter, genaiOperationEmbedding, metricsRequestHeaderLabelMapping),
	}
}

// RecordTokenUsage implements [EmbeddingsMetrics.RecordTokenUsage].
func (e *embeddings) RecordTokenUsage(ctx context.Context, inputTokens, totalTokens uint32, extraAttrs ...attribute.KeyValue) {
	attrs := e.buildBaseAttributes(extraAttrs...)

	e.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributes(attrs...),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
	e.metrics.tokenUsage.Record(ctx, float64(totalTokens),
		metric.WithAttributes(attrs...),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeTotal)),
	)
}

// RecordTokenUsageWithHeaders implements [EmbeddingsMetrics.RecordTokenUsageWithHeaders].
func (e *embeddings) RecordTokenUsageWithHeaders(ctx context.Context, headers map[string]string, inputTokens, totalTokens uint32, extraAttrs ...attribute.KeyValue) {
	attrs := e.buildBaseAttributesWithHeaders(headers, extraAttrs...)

	e.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributes(attrs...),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
	e.metrics.tokenUsage.Record(ctx, float64(totalTokens),
		metric.WithAttributes(attrs...),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeTotal)),
	)
}
