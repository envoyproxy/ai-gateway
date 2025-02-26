// Copyright Envoy AI Gateway Authors.
// SPDX-License-Identifier: Apache-2.0.
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds all prometheus metrics.
type Metrics struct {
	// TotalLatency is the total latency of the request, by backend, model.
	// Measured from the start of the received request headers in extproc to the end of the processed response body in extproc.
	// Implemented as a histogram of the latency of the request to support query like p99, p95, etc.
	TotalLatency *prometheus.HistogramVec
	// TokensTotal is the total number of tokens processed, by backend, model, and type (prompt, completion, total).
	TokensTotal *prometheus.CounterVec
	// RequestsTotal is the total number of requests processed, by backend, model, and status (success, error).
	RequestsTotal *prometheus.CounterVec
	// FirstTokenLatency is the latency to receive the first token, by backend, model.
	// Measured from the start of the received request headers in extproc to the receiving of the first token in the response body in extproc.
	// Implemented as a histogram of the latency to receive the first token to support query like p99, p95, etc.
	FirstTokenLatency *prometheus.HistogramVec
	// InterTokenLatency is the latency between consecutive tokens, if supported, or by chunks/tokens otherwise, by backend, model.
	// Implemented as a histogram of the latency between consecutive tokens to support query like p99, p95, etc.
	InterTokenLatency *prometheus.HistogramVec
	// Registry is the prometheus registry.
	Registry *prometheus.Registry
}

var (
	instance *Metrics
	once     sync.Once
)

// new creates a new Metrics instance.
func new() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		TotalLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "aigateway_total_latency_seconds",
				Help:    "Time spent processing request.",
				Buckets: []float64{.1, .5, 1, 2.5, 5, 10, 20, 30, 60},
			},
			[]string{"backend", "model", "status"},
		),
		TokensTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aigateway_model_tokens_total",
				Help: "Total number of tokens processed by model and type.",
			},
			[]string{"backend", "model", "type"},
		),
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aigateway_requests_total",
				Help: "Total number of requests processed.",
			},
			[]string{"backend", "model", "status"},
		),
		FirstTokenLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "aigateway_first_token_latency_seconds",
				Help:    "Time to receive first token in streaming responses.",
				Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"backend", "model"},
		),
		InterTokenLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "aigateway_inter_token_latency_seconds",
				Help:    "Time between consecutive tokens in streaming responses.",
				Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"backend", "model"},
		),
	}

	// Register all metrics.
	m.Registry.MustRegister(m.TotalLatency)
	m.Registry.MustRegister(m.TokensTotal)
	m.Registry.MustRegister(m.RequestsTotal)
	m.Registry.MustRegister(m.FirstTokenLatency)
	m.Registry.MustRegister(m.InterTokenLatency)

	return m
}

// GetOrCreate returns the singleton metrics instance.
func GetOrCreate() *Metrics {
	once.Do(func() {
		instance = new()
	})
	return instance
}

// GetRegistry returns the metrics registry.
func GetRegistry() *prometheus.Registry {
	return GetOrCreate().Registry
}
