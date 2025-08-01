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

// chatCompletion is the implementation for the chat completion AI Gateway metrics.
type chatCompletion struct {
	baseMetrics
	firstTokenSent    bool
	lastTokenTime     time.Time
	timeToFirstToken  float64
	interTokenLatency float64
}

// ChatCompletionMetrics is the interface for the chat completion AI Gateway metrics.
type ChatCompletionMetrics interface {
	// StartRequest initializes timing for a new request.
	StartRequest(headers map[string]string)
	// SetModel sets the model the request. This is usually called after parsing the request body .
	SetModel(model string)
	// SetBackend sets the selected backend when the routing decision has been made. This is usually called
	// after parsing the request body to determine the model and invoke the routing logic.
	SetBackend(backend *filterapi.Backend)

	// RecordTokenUsage records token usage metrics.
	RecordTokenUsage(ctx context.Context, inputTokens, outputTokens, totalTokens uint32, requestHeaderLabelMapping map[string]string, extraAttrs ...attribute.KeyValue)
	// RecordRequestCompletion records latency metrics for the entire request.
	RecordRequestCompletion(ctx context.Context, success bool, requestHeaderLabelMapping map[string]string, extraAttrs ...attribute.KeyValue)
	// RecordTokenLatency records latency metrics for token generation.
	RecordTokenLatency(ctx context.Context, tokens uint32, requestHeaderLabelMapping map[string]string, extraAttrs ...attribute.KeyValue)
	// GetTimeToFirstTokenMs returns the time to first token in stream mode in milliseconds.
	GetTimeToFirstTokenMs() float64
	// GetInterTokenLatencyMs returns the inter token latency in stream mode in milliseconds.
	GetInterTokenLatencyMs() float64
}

// NewChatCompletion creates a new x.ChatCompletionMetrics instance.
func NewChatCompletion(meter metric.Meter, requestHeaderLabelMapping map[string]string) ChatCompletionMetrics {
	return &chatCompletion{
		baseMetrics: newBaseMetrics(meter, genaiOperationChat, requestHeaderLabelMapping),
	}
}

// StartRequest initializes timing for a new request.
func (c *chatCompletion) StartRequest(headers map[string]string) {
	c.baseMetrics.StartRequest(headers)
	c.firstTokenSent = false
}

// RecordTokenUsage implements [ChatCompletion.RecordTokenUsage].
func (c *chatCompletion) RecordTokenUsage(ctx context.Context, inputTokens, outputTokens, totalTokens uint32, requestHeaders map[string]string, extraAttrs ...attribute.KeyValue) {
	attrs := c.buildBaseAttributes(requestHeaders, extraAttrs...)

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
func (c *chatCompletion) RecordTokenLatency(ctx context.Context, tokens uint32, requestHeaders map[string]string, extraAttrs ...attribute.KeyValue) {
	attrs := c.buildBaseAttributes(requestHeaders, extraAttrs...)

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
