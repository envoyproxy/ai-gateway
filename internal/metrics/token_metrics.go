// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import "time"

// Status constants for request completion.
const (
	requestStatusSuccess = "success"
	requestStatusError   = "error"
)

// ProcessorMetrics tracks metrics for request processing.
type ProcessorMetrics struct {
	metrics        *Metrics
	firstTokenSent bool
	requestStart   time.Time
	lastTokenTime  time.Time
}

// NewProcessorMetrics creates a new ProcessorMetrics instance.
func NewProcessorMetrics() *ProcessorMetrics {
	return &ProcessorMetrics{
		metrics: GetOrCreate(),
	}
}

// StartRequest initializes timing for a new request.
func (p *ProcessorMetrics) StartRequest() {
	p.requestStart = time.Now()
	p.firstTokenSent = false
}

// UpdateTokenMetrics updates token usage metrics.
func (p *ProcessorMetrics) UpdateTokenMetrics(backendName, modelName string, outputTokens, inputTokens, totalTokens uint32) {
	p.metrics.TokensTotal.WithLabelValues(backendName, modelName, "completion").Add(float64(outputTokens))
	p.metrics.TokensTotal.WithLabelValues(backendName, modelName, "prompt").Add(float64(inputTokens))
	p.metrics.TokensTotal.WithLabelValues(backendName, modelName, "total").Add(float64(totalTokens))
}

// UpdateLatencyMetrics updates latency metrics for token generation.
func (p *ProcessorMetrics) UpdateLatencyMetrics(backendName, modelName string, outputTokens uint32) {
	now := time.Now()
	if !p.firstTokenSent {
		p.firstTokenSent = true
		p.metrics.FirstTokenLatency.WithLabelValues(backendName, modelName).
			Observe(now.Sub(p.requestStart).Seconds())
	} else if outputTokens > 0 {
		// Calculate time between tokens.
		itl := now.Sub(p.lastTokenTime).Seconds() / float64(outputTokens)
		p.metrics.InterTokenLatency.WithLabelValues(backendName, modelName).
			Observe(itl)
	}
	p.lastTokenTime = now
}

// RecordRequestCompletion records metrics for a completed request.
func (p *ProcessorMetrics) RecordRequestCompletion(backendName, modelName string, success bool) {
	status := requestStatusError
	if success {
		status = requestStatusSuccess
		p.metrics.TotalLatency.WithLabelValues(backendName, modelName, status).
			Observe(time.Since(p.requestStart).Seconds())
	}
	p.metrics.RequestsTotal.WithLabelValues(backendName, modelName, status).Inc()
}
