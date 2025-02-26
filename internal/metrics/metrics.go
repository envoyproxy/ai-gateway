// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds all prometheus metrics
type Metrics struct {
	TotalLatency      *prometheus.HistogramVec
	TokensTotal       *prometheus.CounterVec
	RequestsTotal     *prometheus.CounterVec
	FirstTokenLatency *prometheus.HistogramVec
	InterTokenLatency *prometheus.HistogramVec
	Registry          *prometheus.Registry
}

var (
	instance *Metrics
	once     sync.Once
)

// new creates a new Metrics instance
func new() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		TotalLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "aigateway_total_latency_seconds",
				Help:    "Time spent processing request",
				Buckets: []float64{.1, .5, 1, 2.5, 5, 10, 20, 30, 60},
			},
			[]string{"backend", "model", "status"},
		),
		TokensTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aigateway_model_tokens_total",
				Help: "Total number of tokens processed by model and type",
			},
			[]string{"backend", "model", "type"},
		),
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aigateway_requests_total",
				Help: "Total number of requests processed",
			},
			[]string{"backend", "model", "status"},
		),
		FirstTokenLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "aigateway_first_token_latency_seconds",
				Help:    "Time to receive first token in streaming responses",
				Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"backend", "model"},
		),
		InterTokenLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "aigateway_inter_token_latency_seconds",
				Help:    "Time between consecutive tokens in streaming responses",
				Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"backend", "model"},
		),
	}

	// Register all metrics
	m.Registry.MustRegister(m.TotalLatency)
	m.Registry.MustRegister(m.TokensTotal)
	m.Registry.MustRegister(m.RequestsTotal)
	m.Registry.MustRegister(m.FirstTokenLatency)
	m.Registry.MustRegister(m.InterTokenLatency)

	return m
}

// GetOrCreate returns the singleton metrics instance
func GetOrCreate() *Metrics {
	once.Do(func() {
		instance = new()
	})
	return instance
}

// GetRegistry returns the metrics registry
func GetRegistry() *prometheus.Registry {
	return GetOrCreate().Registry
} 