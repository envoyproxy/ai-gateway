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

// chatCompletion is the implementation for the chat completion AI Gateway metrics.
type chatCompletion struct {
	baseMetrics
	firstTokenSent    bool
	timeToFirstToken  time.Duration // Duration to first token.
	interTokenLatency time.Duration // Average time per token after first.
	totalOutputTokens uint32
}

// ChatCompletionMetrics is the interface for the chat completion AI Gateway metrics.
type ChatCompletionMetrics interface {
	// StartRequest initializes timing for a new request.
	StartRequest(headers map[string]string)
	// SetOriginalModel sets the original model from the incoming request body before any virtualization applies.
	// This is usually called after parsing the request body. Example: gpt-5
	SetOriginalModel(originalModel internalapi.OriginalModel)
	// SetRequestModel sets the model from the request. This is usually called after parsing the request body.
	// Example: gpt-5-nano
	SetRequestModel(requestModel internalapi.RequestModel)
	// SetResponseModel sets the model that ultimately generated the response.
	// Example: gpt-5-nano-2025-08-07
	SetResponseModel(responseModel internalapi.ResponseModel)
	// SetBackend sets the selected backend when the routing decision has been made. This is usually called
	// after parsing the request body to determine the model and invoke the routing logic.
	SetBackend(backend *filterapi.Backend)

	// RecordTokenUsage records token usage metrics.
	RecordTokenUsage(ctx context.Context, inputTokens, cachedInputTokens, outputTokens uint32, requestHeaderLabelMapping map[string]string)
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
	c.timeToFirstToken = 0
	c.interTokenLatency = 0
}

// RecordTokenUsage implements [ChatCompletion.RecordTokenUsage].
func (c *chatCompletion) RecordTokenUsage(ctx context.Context, inputTokens, cachedInputTokens, outputTokens uint32, requestHeaders map[string]string) {
	attrs := c.buildBaseAttributes(requestHeaders)

	c.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
	c.metrics.tokenUsage.Record(ctx, float64(cachedInputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeCachedInput)),
	)
	c.metrics.tokenUsage.Record(ctx, float64(outputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput)),
	)
	// Note: We don't record totalTokens separately as it causes double counting.
	// The OTEL spec only defines "input" and "output" token types.
}

// RecordTokenLatency implements [ChatCompletion.RecordTokenLatency].
func (c *chatCompletion) RecordTokenLatency(ctx context.Context, tokens uint32, endOfStream bool, requestHeaders map[string]string) {
	attrs := c.buildBaseAttributes(requestHeaders)

	// Record time to first token on the first call for streaming responses.
	// This ensures we capture the metric even when token counts aren't available in streaming chunks.
	if !c.firstTokenSent {
		c.firstTokenSent = true
		c.timeToFirstToken = time.Since(c.requestStart)
		c.metrics.firstTokenLatency.Record(ctx, c.timeToFirstToken.Seconds(), metric.WithAttributeSet(attrs))
		return
	}

	// Track max cumulative tokens across the stream.
	if tokens > c.totalOutputTokens {
		c.totalOutputTokens = tokens
	}

	// Record once at end-of-stream using average from first token.
	// Per OTEL spec: time_per_output_token = (request_duration - time_to_first_token) / (output_tokens - 1).
	// This measures the average time for ALL tokens after the first one, not just after the first chunk.
	if endOfStream && c.totalOutputTokens > 1 {
		// Calculate time elapsed since first token was sent.
		currentElapsed := time.Since(c.requestStart)
		timeSinceFirstToken := currentElapsed - c.timeToFirstToken
		// Divide by (total_tokens - 1) as per spec, not by tokens after first chunk.
		c.interTokenLatency = timeSinceFirstToken / time.Duration(c.totalOutputTokens-1)
		c.metrics.outputTokenLatency.Record(ctx, c.interTokenLatency.Seconds(), metric.WithAttributeSet(attrs))
	}
}

// GetTimeToFirstTokenMs implements [x.ChatCompletionMetrics.GetTimeToFirstTokenMs].
func (c *chatCompletion) GetTimeToFirstTokenMs() float64 {
	return float64(c.timeToFirstToken.Milliseconds())
}

// GetInterTokenLatencyMs implements [x.ChatCompletionMetrics.GetInterTokenLatencyMs].
func (c *chatCompletion) GetInterTokenLatencyMs() float64 {
	return float64(c.interTokenLatency.Milliseconds())
}
