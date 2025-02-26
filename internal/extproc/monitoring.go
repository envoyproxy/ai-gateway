// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// TokenMetrics is the interface for recording token and request metrics.
type TokenMetrics interface {
	// RecordTokenMetrics records the number of tokens processed by model and backend.
	RecordTokenMetrics(backendName, modelName string, valueType string, value float64)
	// RecordRequestMetrics records the number of requests processed by model and backend.
	RecordRequestMetrics(backendName, modelName string, success bool, duration time.Duration)
	// RecordTimeToFirstToken records the time to receive the first token.
	RecordTimeToFirstToken(backendName, modelName string, duration time.Duration)
	// RecordInterTokenLatency records the time between consecutive tokens.
	RecordInterTokenLatency(backendName, modelName string, latency float64)
}

var (
	tokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "aigateway_model_tokens_total",
			Help: "Total number of tokens processed by model and type",
		},
		[]string{"backend", "model", "type"},
	)

	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "aigateway_requests_total",
			Help: "Total number of requests processed",
		},
		[]string{"backend", "model", "status"},
	)
	totalLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "aigateway_total_latency_seconds",
			Help:    "Time spent processing request",
			Buckets: []float64{.1, .5, 1, 2.5, 5, 10, 20, 30, 60},
		},
		[]string{"backend", "model", "status"},
	)

	firstTokenLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "aigateway_first_token_latency_seconds",
			Help:    "Time to receive first token in streaming responses",
			Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"backend", "model"},
	)

	interTokenLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "aigateway_inter_token_latency_seconds",
			Help:    "Time between consecutive tokens in streaming responses",
			Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"backend", "model"},
	)
)

func init() {
	metrics.Registry.MustRegister(tokensTotal)
	metrics.Registry.MustRegister(requestsTotal)
	metrics.Registry.MustRegister(totalLatency)
	metrics.Registry.MustRegister(firstTokenLatency)
	metrics.Registry.MustRegister(interTokenLatency)
}

var _ TokenMetrics = (*tokenMetrics)(nil)

type tokenMetrics struct{}

// NewTokenMetrics creates a new TokenMetrics.
func NewTokenMetrics() TokenMetrics {
	return &tokenMetrics{}
}

// RecordTokenMetrics implements [TokenMetrics].
func (t tokenMetrics) RecordTokenMetrics(backendName, modelName string, valueType string, value float64) {
	tokensTotal.WithLabelValues(backendName, modelName, valueType).Add(value)
}

// RecordRequestMetrics implements [TokenMetrics].
func (t tokenMetrics) RecordRequestMetrics(backendName, modelName string, success bool, duration time.Duration) {
	status := "success"
	if !success {
		status = "error"
	}
	requestsTotal.WithLabelValues(backendName, modelName, status).Inc()
	totalLatency.WithLabelValues(backendName, modelName, status).Observe(duration.Seconds())
}

// RecordTimeToFirstToken implements [TokenMetrics].
func (t tokenMetrics) RecordTimeToFirstToken(backendName, modelName string, duration time.Duration) {
	firstTokenLatency.WithLabelValues(backendName, modelName).Observe(duration.Seconds())
}

// RecordInterTokenLatency implements [TokenMetrics].
func (t tokenMetrics) RecordInterTokenLatency(backendName, modelName string, latency float64) {
	interTokenLatency.WithLabelValues(backendName, modelName).Observe(latency)
}
