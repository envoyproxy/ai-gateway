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

// chatCompletion is the implementation for the chat completion AI Gateway metrics.
type chatCompletion struct {
	metrics        *genAI
	firstTokenSent bool
	requestStart   time.Time
	lastTokenTime  time.Time
	model          string
	backend        string
}

// NewChatCompletion creates a new ChatCompletion instance.
func NewChatCompletion(meter metric.Meter, newCustomFn x.NewCustomChatCompletionMetricsFn) x.ChatCompletionMetrics {
	if newCustomFn != nil {
		return newCustomFn(meter)
	}
	return DefaultChatCompletion(meter)
}

// DefaultChatCompletion creates a new default ChatCompletion instance.
func DefaultChatCompletion(meter metric.Meter) x.ChatCompletionMetrics {
	return &chatCompletion{
		metrics: newGenAI(meter),
		model:   "unknown",
		backend: "unknown",
	}
}

// StartRequest initializes timing for a new request.
func (c *chatCompletion) StartRequest(_ map[string]string) {
	c.requestStart = time.Now()
	c.firstTokenSent = false
}

// SetModel sets the model for the request.
func (c *chatCompletion) SetModel(model string) {
	c.model = model
}

// SetBackend sets the name of the backend to be reported in the metrics according to:
// https://opentelemetry.io/docs/specs/semconv/attributes-registry/gen-ai/#gen-ai-system
func (c *chatCompletion) SetBackend(backend *filterapi.Backend) {
	switch backend.Schema.Name {
	case filterapi.APISchemaOpenAI:
		c.backend = genaiSystemOpenAI
	case filterapi.APISchemaAWSBedrock:
		c.backend = genAISystemAWSBedrock
	default:
		c.backend = backend.Name
	}
}

// RecordTokenUsage implements [ChatCompletion.RecordTokenUsage].
func (c *chatCompletion) RecordTokenUsage(ctx context.Context, inputTokens, outputTokens, totalTokens uint32, extraAttrs ...attribute.KeyValue) {
	attrs := make([]attribute.KeyValue, 0, 3+len(extraAttrs))
	attrs = append(attrs,
		attribute.Key(genaiAttributeOperationName).String(genaiOperationChat),
		attribute.Key(genaiAttributeSystemName).String(c.backend),
		attribute.Key(genaiAttributeRequestModel).String(c.model),
	)
	attrs = append(attrs, extraAttrs...)

	c.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributes(attrs...),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
	c.metrics.tokenUsage.Record(ctx, float64(outputTokens),
		metric.WithAttributes(attrs...),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput)),
	)
	c.metrics.tokenUsage.Record(ctx, float64(totalTokens),
		metric.WithAttributes(attrs...),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeTotal)),
	)
}

// RecordRequestCompletion implements [ChatCompletion.RecordRequestCompletion].
func (c *chatCompletion) RecordRequestCompletion(ctx context.Context, success bool, extraAttrs ...attribute.KeyValue) {
	attrs := make([]attribute.KeyValue, 0, 3+len(extraAttrs))
	attrs = append(attrs,
		attribute.Key(genaiAttributeOperationName).String(genaiOperationChat),
		attribute.Key(genaiAttributeSystemName).String(c.backend),
		attribute.Key(genaiAttributeRequestModel).String(c.model),
	)
	attrs = append(attrs, extraAttrs...)

	if success {
		// According to the semantic conventions, the error attribute should not be added for successful operations
		c.metrics.requestLatency.Record(ctx, time.Since(c.requestStart).Seconds(), metric.WithAttributes(attrs...))
	} else {
		// We don't have a set of typed errors yet, or a set of low-cardinality values, so we can just set the value to the
		// placeholder one. See: https://opentelemetry.io/docs/specs/semconv/attributes-registry/error/#error-type
		c.metrics.requestLatency.Record(ctx, time.Since(c.requestStart).Seconds(),
			metric.WithAttributes(attrs...),
			metric.WithAttributes(attribute.Key(genaiAttributeErrorType).String(genaiErrorTypeFallback)),
		)
	}
}

// RecordTokenLatency implements [ChatCompletion.RecordTokenLatency].
func (c *chatCompletion) RecordTokenLatency(ctx context.Context, tokens uint32, extraAttrs ...attribute.KeyValue) {
	attrs := make([]attribute.KeyValue, 0, 3+len(extraAttrs))
	attrs = append(attrs,
		attribute.Key(genaiAttributeOperationName).String(genaiOperationChat),
		attribute.Key(genaiAttributeSystemName).String(c.backend),
		attribute.Key(genaiAttributeRequestModel).String(c.model),
	)
	attrs = append(attrs, extraAttrs...)

	if !c.firstTokenSent {
		c.firstTokenSent = true
		c.metrics.firstTokenLatency.Record(ctx, time.Since(c.requestStart).Seconds(), metric.WithAttributes(attrs...))
	} else if tokens > 0 {
		// Calculate time between tokens.
		itl := time.Since(c.lastTokenTime).Seconds() / float64(tokens)
		c.metrics.outputTokenLatency.Record(ctx, itl, metric.WithAttributes(attrs...))
	}
	c.lastTokenTime = time.Now()
}
