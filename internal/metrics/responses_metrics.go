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
	reasoningDuration time.Duration // Total reasoning time.
	toolCallCount     uint32        // Number of tool calls in this request.
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
	RecordTokenUsage(ctx context.Context, inputTokens, outputTokens uint32, requestHeaders map[string]string)
	// RecordTokenLatency records latency metrics for token generation in streaming mode.
	RecordTokenLatency(ctx context.Context, tokens uint32, endOfStream bool, requestHeaderLabelMapping map[string]string)
	// RecordReasoningLatency records time spent in reasoning/thinking phases.
	RecordReasoningLatency(ctx context.Context, duration time.Duration, requestHeaders map[string]string)
	// RecordToolExecution records metrics for tool call execution.
	RecordToolExecution(ctx context.Context, toolType string, duration time.Duration, success bool, requestHeaders map[string]string)
	// RecordError records an error for a Responses API request.
	RecordError(ctx context.Context, model string, backend string, errorType string)
	// GetTimeToFirstTokenMs returns the time to first token/output in stream mode in milliseconds.
	GetTimeToFirstTokenMs() float64
	// GetInterTokenLatencyMs returns the inter token latency in stream mode in milliseconds.
	GetInterTokenLatencyMs() float64
	// GetReasoningDurationMs returns the total reasoning duration in milliseconds.
	GetReasoningDurationMs() float64
	// GetToolCallCount returns the number of tool calls made during this request.
	GetToolCallCount() uint32
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
	r.reasoningDuration = 0
	r.toolCallCount = 0
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

// RecordResponse implements [ResponsesMetrics.RecordResponse].
// func (r *responses) RecordResponse(ctx context.Context, model string, backend string, status string, duration time.Duration) {
// 	// Record completion through the base metrics
// 	success := status == "success"
// 	r.RecordRequestCompletion(ctx, success, nil)
// 	_, _ = backend, duration // Backend and duration tracking could be added to base metrics
// }

// RecordTokenUsage implements [ResponsesMetrics.RecordTokenUsage].
func (r *responses) RecordTokenUsage(ctx context.Context, inputTokens, outputTokens uint32, requestHeaders map[string]string) {
	attrs := r.buildBaseAttributes(requestHeaders)

	r.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
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
		currentElapsed := time.Since(r.requestStart)
		timeSinceFirstToken := currentElapsed - r.timeToFirstToken
		r.interTokenLatency = timeSinceFirstToken / time.Duration(r.totalOutputTokens-1)
		r.metrics.outputTokenLatency.Record(ctx, r.interTokenLatency.Seconds(), metric.WithAttributeSet(attrs))
	}
}

// RecordReasoningLatency implements [ResponsesMetrics.RecordReasoningLatency].
func (r *responses) RecordReasoningLatency(_ context.Context, duration time.Duration, requestHeaders map[string]string) {
	attrs := r.buildBaseAttributes(requestHeaders)
	r.reasoningDuration += duration

	// Record reasoning latency as a custom metric
	// This could be extended to use a dedicated histogram metric in the future
	_ = attrs // Use attrs when we add custom reasoning metrics
}

// RecordToolExecution implements [ResponsesMetrics.RecordToolExecution].
func (r *responses) RecordToolExecution(_ context.Context, toolType string, _ time.Duration, success bool, requestHeaders map[string]string) {
	attrs := r.buildBaseAttributes(requestHeaders)
	r.toolCallCount++

	// Record tool execution metrics
	// This could be extended to use dedicated counter/histogram metrics in the future
	_, _, _ = toolType, success, attrs // Use when we add custom tool execution metrics
}

// RecordError implements [ResponsesMetrics.RecordError].
func (r *responses) RecordError(ctx context.Context, model string, backend string, errorType string) {
	// Record as a failed request completion
	r.RecordRequestCompletion(ctx, false, nil)
	_, _, _ = model, backend, errorType // Use when we add error type tracking
}

// GetTimeToFirstTokenMs implements [ResponsesMetrics.GetTimeToFirstTokenMs].
func (r *responses) GetTimeToFirstTokenMs() float64 {
	return float64(r.timeToFirstToken.Milliseconds())
}

// GetInterTokenLatencyMs implements [ResponsesMetrics.GetInterTokenLatencyMs].
func (r *responses) GetInterTokenLatencyMs() float64 {
	return float64(r.interTokenLatency.Milliseconds())
}

// GetReasoningDurationMs implements [ResponsesMetrics.GetReasoningDurationMs].
func (r *responses) GetReasoningDurationMs() float64 {
	return float64(r.reasoningDuration.Milliseconds())
}

// GetToolCallCount implements [ResponsesMetrics.GetToolCallCount].
func (r *responses) GetToolCallCount() uint32 {
	return r.toolCallCount
}
