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

const (
	genaiOperationResponses = "responses"
)

// responses is the implementation for the responses AI Gateway metrics.
type responses struct {
	baseMetrics
	firstTokenSent    bool
	timeToFirstToken  time.Duration // Duration to first token/output.
	interTokenLatency time.Duration // Average time per token after first.
	totalOutputTokens uint32
}

// ResponsesMetrics is the interface for recording metrics for the Responses API.
type ResponsesMetrics interface {
	// StartRequest initializes timing for a new request.
	StartRequest(headers map[string]string)
	// SetOriginalModel sets the original model from the incoming request body before any virtualization applies.
	// This is usually called after parsing the request body. Example: text-embedding-3-small
	SetOriginalModel(originalModel internalapi.OriginalModel)
	// SetRequestModel sets the model from the request. This is usually called after parsing the request body.
	// Example: text-embedding-3-small
	SetRequestModel(requestModel internalapi.RequestModel)
	// SetResponseModel sets the model that ultimately generated the response.
	// Example: text-embedding-3-small-2025-02-18
	SetResponseModel(responseModel internalapi.ResponseModel)
	// SetBackend sets the selected backend when the routing decision has been made. This is usually called
	// after parsing the request body to determine the model and invoke the routing logic.
	SetBackend(backend *filterapi.Backend)

	// RecordRequestCompletion records the completion of a request to the Responses API.
	RecordRequestCompletion(ctx context.Context, success bool, requestHeaderLabelMapping map[string]string)
	// RecordTokenUsage records token usage for a Responses API request.
	RecordTokenUsage(ctx context.Context, inputTokens, CachedInputTokens, outputTokens uint32, requestHeaders map[string]string)
	// RecordTokenLatency records latency metrics for token generation in streaming mode.
	RecordTokenLatency(ctx context.Context, tokens uint32, endOfStream bool, requestHeaderLabelMapping map[string]string)
	// GetTimeToFirstTokenMs returns the time to first token/output in stream mode in milliseconds.
	GetTimeToFirstTokenMs() float64
	// GetInterTokenLatencyMs returns the inter token latency in stream mode in milliseconds.
	GetInterTokenLatencyMs() float64
}

// NewResponses creates a new ResponsesMetrics instance.
func NewResponses(meter metric.Meter, requestHeaderLabelMapping map[string]string) ResponsesMetrics {
	return &responses{
		baseMetrics: newBaseMetrics(meter, genaiOperationResponses, requestHeaderLabelMapping),
	}
}

// StartRequest initializes timing for a new request.
func (r *responses) StartRequest(headers map[string]string) {
	r.baseMetrics.StartRequest(headers)
	r.firstTokenSent = false
	r.totalOutputTokens = 0
	r.timeToFirstToken = 0
	r.interTokenLatency = 0
}

// SetOriginalModel sets the original model from the incoming request body before any virtualization applies.
func (r *responses) SetOriginalModel(originalModel internalapi.OriginalModel) {
	r.baseMetrics.SetOriginalModel(originalModel)
}

// SetRequestModel sets the model from the request.
func (r *responses) SetRequestModel(requestModel internalapi.RequestModel) {
	r.baseMetrics.SetRequestModel(requestModel)
}

// SetResponseModel sets the model that ultimately generated the response.
func (r *responses) SetResponseModel(responseModel internalapi.ResponseModel) {
	r.baseMetrics.SetResponseModel(responseModel)
}

// SetBackend sets the selected backend.
func (r *responses) SetBackend(backend *filterapi.Backend) {
	r.baseMetrics.SetBackend(backend)
}

// RecordTokenUsage implements [ResponsesMetrics.RecordTokenUsage].
func (r *responses) RecordTokenUsage(ctx context.Context, inputTokens, cachedInputTokens, outputTokens uint32, requestHeaders map[string]string) {
	// TODO: Record reasoning tokens
	attrs := r.buildBaseAttributes(requestHeaders)
	r.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
	r.metrics.tokenUsage.Record(ctx, float64(cachedInputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeCachedInput)),
	)
	r.metrics.tokenUsage.Record(ctx, float64(outputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput)),
	)
	// Note: We don't record totalTokens separately as it causes double counting.
	// The OTEL spec only defines "input" and "output" token types.
}

// RecordTokenLatency implements [ResponsesMetrics.RecordTokenLatency].
func (r *responses) RecordTokenLatency(ctx context.Context, tokens uint32, endOfStream bool, requestHeaders map[string]string) {
	attrs := r.buildBaseAttributes(requestHeaders)

	// Record time to first token/output on the first call for streaming responses.
	// This captures the time including any reasoning or tool execution that happened before first output.
	if !r.firstTokenSent {
		r.firstTokenSent = true
		r.timeToFirstToken = time.Since(r.requestStart)
		r.metrics.firstTokenLatency.Record(ctx, r.timeToFirstToken.Seconds(), metric.WithAttributeSet(attrs))
		return
	}

	// Track max cumulative tokens across the stream.
	if tokens > r.totalOutputTokens {
		r.totalOutputTokens = tokens
	}

	// Record once at end-of-stream using average from first token.
	// Per OTEL spec: time_per_output_token = (request_duration - time_to_first_token) / (output_tokens - 1).
	if endOfStream && r.totalOutputTokens > 1 {
		// Calculate time elapsed since first token was sent.
		currentElapsed := time.Since(r.requestStart)
		timeSinceFirstToken := currentElapsed - r.timeToFirstToken
		// Divide by (total_tokens - 1) as per spec, not by tokens after first chunk.
		r.interTokenLatency = timeSinceFirstToken / time.Duration(r.totalOutputTokens-1)
		r.metrics.outputTokenLatency.Record(ctx, r.interTokenLatency.Seconds(), metric.WithAttributeSet(attrs))
	}
}

// GetTimeToFirstTokenMs implements [ResponsesMetrics.GetTimeToFirstTokenMs].
func (r *responses) GetTimeToFirstTokenMs() float64 {
	return float64(r.timeToFirstToken.Milliseconds())
}

// GetInterTokenLatencyMs implements [ResponsesMetrics.GetInterTokenLatencyMs].
func (r *responses) GetInterTokenLatencyMs() float64 {
	return float64(r.interTokenLatency.Milliseconds())
}
