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
	timeToFirstToken  float64
	interTokenLatency float64
	totalOutputTokens uint32
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
	RecordTokenUsage(ctx context.Context, inputTokens, outputTokens, totalTokens uint32, requestHeaderLabelMapping map[string]string)
	// RecordRequestCompletion records latency metrics for the entire request.
	RecordRequestCompletion(ctx context.Context, success bool, requestHeaderLabelMapping map[string]string)
	// RecordTokenLatency records latency metrics for token generation.
	RecordTokenLatency(ctx context.Context, tokens uint32, endOfStream bool, requestHeaderLabelMapping map[string]string)
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
	c.totalOutputTokens = 0
}

// RecordTokenUsage implements [ChatCompletion.RecordTokenUsage].
func (c *chatCompletion) RecordTokenUsage(ctx context.Context, inputTokens, outputTokens, totalTokens uint32, requestHeaders map[string]string) {
	attrs := c.buildBaseAttributes(requestHeaders)

	c.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
	c.metrics.tokenUsage.Record(ctx, float64(outputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput)),
	)
	c.metrics.tokenUsage.Record(ctx, float64(totalTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeTotal)),
	)
}

// RecordTokenLatency implements [ChatCompletion.RecordTokenLatency].
func (c *chatCompletion) RecordTokenLatency(ctx context.Context, tokens uint32, endOfStream bool, requestHeaders map[string]string) {
	attrs := c.buildBaseAttributes(requestHeaders)

	if !c.firstTokenSent {
		c.firstTokenSent = true
		c.timeToFirstToken = time.Since(c.requestStart).Seconds()
		c.metrics.firstTokenLatency.Record(ctx, c.timeToFirstToken, metric.WithAttributeSet(attrs))
		return
	}

	// Track max cumulative tokens across the stream.
	if tokens > c.totalOutputTokens {
		c.totalOutputTokens = tokens
	}

	// Record once at end-of-stream using average from first token.
	if endOfStream && c.totalOutputTokens > 0 {
		firstTokenTime := c.requestStart.Add(time.Duration(c.timeToFirstToken * float64(time.Second)))
		c.interTokenLatency = time.Since(firstTokenTime).Seconds() / float64(c.totalOutputTokens)
		c.metrics.outputTokenLatency.Record(ctx, c.interTokenLatency, metric.WithAttributeSet(attrs))
	}
}

// GetTimeToFirstTokenMs implements [x.ChatCompletionMetrics.GetTimeToFirstTokenMs].
func (c *chatCompletion) GetTimeToFirstTokenMs() float64 {
	return c.timeToFirstToken * 1000 // Convert seconds to milliseconds.
}

// GetInterTokenLatencyMs implements [x.ChatCompletionMetrics.GetInterTokenLatencyMs].
func (c *chatCompletion) GetInterTokenLatencyMs() float64 {
	return c.interTokenLatency * 1000 // Convert seconds to milliseconds.
}
