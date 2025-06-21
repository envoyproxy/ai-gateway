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

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
)

const (
	genaiOperationEmbedding = "embedding"
)

// embedding is the implementation for the embedding AI Gateway metrics.
type embedding struct {
	metrics      *genAI
	requestStart time.Time
	model        string
	backend      string
}

// NewEmbedding creates a new Embedding instance.
func NewEmbedding(meter metric.Meter, newCustomFn x.NewCustomEmbeddingMetricsFn) x.EmbeddingMetrics {
	if newCustomFn != nil {
		return newCustomFn(meter)
	}
	return DefaultEmbedding(meter)
}

// DefaultEmbedding creates a new default Embedding instance.
func DefaultEmbedding(meter metric.Meter) x.EmbeddingMetrics {
	return &embedding{
		metrics: newGenAI(meter),
		model:   "unknown",
		backend: "unknown",
	}
}

// StartRequest initializes timing for a new request.
func (e *embedding) StartRequest(_ map[string]string) {
	e.requestStart = time.Now()
}

// SetModel sets the model for the request.
func (e *embedding) SetModel(model string) {
	e.model = model
}

// SetBackend sets the name of the backend to be reported in the metrics according to:
// https://opentelemetry.io/docs/specs/semconv/attributes-registry/gen-ai/#gen-ai-system
func (e *embedding) SetBackend(backend *filterapi.Backend) {
	switch backend.Schema.Name {
	case filterapi.APISchemaOpenAI:
		e.backend = genaiSystemOpenAI
	case filterapi.APISchemaAWSBedrock:
		e.backend = genAISystemAWSBedrock
	default:
		e.backend = backend.Name
	}
}

// RecordTokenUsage implements [EmbeddingMetrics.RecordTokenUsage].
func (e *embedding) RecordTokenUsage(ctx context.Context, inputTokens, totalTokens uint32, extraAttrs ...attribute.KeyValue) {
	attrs := make([]attribute.KeyValue, 0, 3+len(extraAttrs))
	attrs = append(attrs,
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeSystemName).String(e.backend),
		attribute.Key(genaiAttributeRequestModel).String(e.model),
	)
	attrs = append(attrs, extraAttrs...)

	e.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributes(attrs...),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
	e.metrics.tokenUsage.Record(ctx, float64(totalTokens),
		metric.WithAttributes(attrs...),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeTotal)),
	)
}

// RecordRequestCompletion implements [EmbeddingMetrics.RecordRequestCompletion].
func (e *embedding) RecordRequestCompletion(ctx context.Context, success bool, extraAttrs ...attribute.KeyValue) {
	attrs := make([]attribute.KeyValue, 0, 3+len(extraAttrs))
	attrs = append(attrs,
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeSystemName).String(e.backend),
		attribute.Key(genaiAttributeRequestModel).String(e.model),
	)
	attrs = append(attrs, extraAttrs...)

	if success {
		// According to the semantic conventions, the error attribute should not be added for successful operations
		e.metrics.requestLatency.Record(ctx, time.Since(e.requestStart).Seconds(), metric.WithAttributes(attrs...))
	} else {
		// We don't have a set of typed errors yet, or a set of low-cardinality values, so we can just set the value to the
		// placeholder one. See: https://opentelemetry.io/docs/specs/semconv/attributes-registry/error/#error-type
		e.metrics.requestLatency.Record(ctx, time.Since(e.requestStart).Seconds(),
			metric.WithAttributes(attrs...),
			metric.WithAttributes(attribute.Key(genaiAttributeErrorType).String(genaiErrorTypeFallback)),
		)
	}
}
