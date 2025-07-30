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

	"github.com/envoyproxy/ai-gateway/filterapi/x"
)

// chatCompletion is the implementation for the chat completion AI Gateway metrics.
type chatCompletion struct {
	baseMetrics
	firstTokenSent    bool
	lastTokenTime     time.Time
	timeToFirstToken  float64
	interTokenLatency float64
}

// NewChatCompletion creates a new x.ChatCompletionMetrics instance.
func NewChatCompletion(meter metric.Meter, newCustomFn x.NewCustomChatCompletionMetricsFn) x.ChatCompletionMetrics {
	if newCustomFn != nil {
		return newCustomFn(meter)
	}
	return DefaultChatCompletion(meter)
}

// NewChatCompletionWithHeaderMapping creates a new x.ChatCompletionMetrics instance with header mapping.
func NewChatCompletionWithHeaderMapping(meter metric.Meter, headerMapping map[string]string, newCustomFn x.NewCustomChatCompletionMetricsFn) x.ChatCompletionMetrics {
	if newCustomFn != nil {
		return newCustomFn(meter)
	}
	return DefaultChatCompletionWithHeaderMapping(meter, headerMapping)
}

// DefaultChatCompletion creates a new default x.ChatCompletionMetrics instance.
func DefaultChatCompletion(meter metric.Meter) x.ChatCompletionMetrics {
	return &chatCompletion{
		baseMetrics: newBaseMetrics(meter, genaiOperationChat),
	}
}

// DefaultChatCompletionWithHeaderMapping creates a new default x.ChatCompletionMetrics instance with header mapping.
func DefaultChatCompletionWithHeaderMapping(meter metric.Meter, headerMapping map[string]string) x.ChatCompletionMetrics {
	return &chatCompletion{
		baseMetrics: newBaseMetricsWithHeaderMapping(meter, genaiOperationChat, headerMapping),
	}
}

// StartRequest initializes timing for a new request.
func (c *chatCompletion) StartRequest(headers map[string]string) {
	c.baseMetrics.StartRequest(headers)
	c.firstTokenSent = false
}

// RecordTokenUsage implements [ChatCompletion.RecordTokenUsage].
func (c *chatCompletion) RecordTokenUsage(ctx context.Context, inputTokens, outputTokens, totalTokens uint32, extraAttrs ...attribute.KeyValue) {
	attrs := c.buildBaseAttributes(extraAttrs...)

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

// RecordTokenUsageWithHeaders records token usage with header values as attributes.
func (c *chatCompletion) RecordTokenUsageWithHeaders(ctx context.Context, headers map[string]string, inputTokens, outputTokens, totalTokens uint32, extraAttrs ...attribute.KeyValue) {
	attrs := c.buildBaseAttributesWithHeaders(headers, extraAttrs...)

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

// RecordTokenLatency implements [ChatCompletion.RecordTokenLatency].
func (c *chatCompletion) RecordTokenLatency(ctx context.Context, tokens uint32, extraAttrs ...attribute.KeyValue) {
	attrs := c.buildBaseAttributes(extraAttrs...)

	if !c.firstTokenSent {
		c.firstTokenSent = true
		c.timeToFirstToken = time.Since(c.requestStart).Seconds()
		c.metrics.firstTokenLatency.Record(ctx, c.timeToFirstToken, metric.WithAttributes(attrs...))
	} else if tokens > 0 {
		// Calculate time between tokens.
		c.interTokenLatency = time.Since(c.lastTokenTime).Seconds() / float64(tokens)
		c.metrics.outputTokenLatency.Record(ctx, c.interTokenLatency, metric.WithAttributes(attrs...))
	}
	c.lastTokenTime = time.Now()
}

// GetTimeToFirstTokenMs implements [x.ChatCompletionMetrics.GetTimeToFirstTokenMs].
func (c *chatCompletion) GetTimeToFirstTokenMs() float64 {
	return c.timeToFirstToken * 1000 // Convert seconds to milliseconds.
}

// GetInterTokenLatencyMs implements [x.ChatCompletionMetrics.GetInterTokenLatencyMs].
func (c *chatCompletion) GetInterTokenLatencyMs() float64 {
	return c.interTokenLatency * 1000 // Convert seconds to milliseconds.
}
