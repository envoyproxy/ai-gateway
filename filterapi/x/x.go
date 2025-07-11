// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package x is an experimental package that provides the customizability of the AI Gateway filter.
package x

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// NewCustomChatCompletionMetrics is the function to create a custom chat completion AI Gateway metrics over
// the default metrics. This is nil by default and can be set by the custom build of external processor.
var NewCustomChatCompletionMetrics NewCustomChatCompletionMetricsFn

// NewCustomChatCompletionMetricsFn is the function to create a custom chat completion AI Gateway metrics.
type NewCustomChatCompletionMetricsFn func(meter metric.Meter) ChatCompletionMetrics

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
	RecordTokenUsage(ctx context.Context, inputTokens, outputTokens, totalTokens uint32, extraAttrs ...attribute.KeyValue)
	// RecordRequestCompletion records latency metrics for the entire request.
	RecordRequestCompletion(ctx context.Context, success bool, extraAttrs ...attribute.KeyValue)
	// RecordTokenLatency records latency metrics for token generation.
	RecordTokenLatency(ctx context.Context, tokens uint32, extraAttrs ...attribute.KeyValue)
	// GetTimeToFirstTokenMs returns the time to first token in stream mode in milliseconds.
	GetTimeToFirstTokenMs() float64
	// GetInterTokenLatencyMs returns the inter token latency in stream mode in milliseconds.
	GetInterTokenLatencyMs() float64
}

// EmbeddingsMetrics is the interface for the embeddings AI Gateway metrics.
type EmbeddingsMetrics interface {
	// StartRequest initializes timing for a new request.
	StartRequest(headers map[string]string)
	// SetModel sets the model the request. This is usually called after parsing the request body .
	SetModel(model string)
	// SetBackend sets the selected backend when the routing decision has been made. This is usually called
	// after parsing the request body to determine the model and invoke the routing logic.
	SetBackend(backend *filterapi.Backend)

	// RecordTokenUsage records token usage metrics for embeddings (only input and total tokens are relevant).
	RecordTokenUsage(ctx context.Context, inputTokens, totalTokens uint32, extraAttrs ...attribute.KeyValue)
	// RecordRequestCompletion records latency metrics for the entire request.
	RecordRequestCompletion(ctx context.Context, success bool, extraAttrs ...attribute.KeyValue)
}
