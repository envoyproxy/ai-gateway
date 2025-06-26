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
)

// baseMetrics provides shared functionality for AI Gateway metrics implementations.
type baseMetrics struct {
	metrics      *genAI
	operation    string
	requestStart time.Time
	model        string
	backend      string
}

// newBaseMetrics creates a new baseMetrics instance with the specified operation.
func newBaseMetrics(meter metric.Meter, operation string) baseMetrics {
	return baseMetrics{
		metrics:   newGenAI(meter),
		operation: operation,
		model:     "unknown",
		backend:   "unknown",
	}
}

// StartRequest initializes timing for a new request.
func (b *baseMetrics) StartRequest(_ map[string]string) {
	b.requestStart = time.Now()
}

// SetModel sets the model for the request.
func (b *baseMetrics) SetModel(model string) {
	b.model = model
}

// SetBackend sets the name of the backend to be reported in the metrics according to:
// https://opentelemetry.io/docs/specs/semconv/attributes-registry/gen-ai/#gen-ai-system
func (b *baseMetrics) SetBackend(backend *filterapi.Backend) {
	switch backend.Schema.Name {
	case filterapi.APISchemaOpenAI:
		b.backend = genaiSystemOpenAI
	case filterapi.APISchemaAWSBedrock:
		b.backend = genAISystemAWSBedrock
	default:
		b.backend = backend.Name
	}
}

// buildBaseAttributes creates the base attributes for metrics recording.
func (b *baseMetrics) buildBaseAttributes(extraAttrs ...attribute.KeyValue) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 3+len(extraAttrs))
	attrs = append(attrs,
		attribute.Key(genaiAttributeOperationName).String(b.operation),
		attribute.Key(genaiAttributeSystemName).String(b.backend),
		attribute.Key(genaiAttributeRequestModel).String(b.model),
	)
	attrs = append(attrs, extraAttrs...)
	return attrs
}

// RecordRequestCompletion records the completion of a request with success/failure status.
func (b *baseMetrics) RecordRequestCompletion(ctx context.Context, success bool, extraAttrs ...attribute.KeyValue) {
	attrs := b.buildBaseAttributes(extraAttrs...)

	if success {
		// According to the semantic conventions, the error attribute should not be added for successful operations
		b.metrics.requestLatency.Record(ctx, time.Since(b.requestStart).Seconds(), metric.WithAttributes(attrs...))
	} else {
		// We don't have a set of typed errors yet, or a set of low-cardinality values, so we can just set the value to the
		// placeholder one. See: https://opentelemetry.io/docs/specs/semconv/attributes-registry/error/#error-type
		b.metrics.requestLatency.Record(ctx, time.Since(b.requestStart).Seconds(),
			metric.WithAttributes(attrs...),
			metric.WithAttributes(attribute.Key(genaiAttributeErrorType).String(genaiErrorTypeFallback)),
		)
	}
}
