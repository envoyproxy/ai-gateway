// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import "github.com/prometheus/client_golang/prometheus"

// genAI holds all prometheus metrics. See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/
type genAI struct {
	// Number of tokens processed.
	// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiclienttokenusage
	tokenUsage *prometheus.HistogramVec
	// requestLatency is the total latency of the request.
	// Measured from the start of the received request headers in extproc to the end of the processed response body in extproc.
	// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiserverrequestduration
	requestLatency *prometheus.HistogramVec
	// firstTokenLatency is the latency to receive the first token.
	// Measured from the start of the received request headers in extproc to the receiving of the first token in the response body in extproc.
	// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiservertime_to_first_token
	firstTokenLatency *prometheus.HistogramVec
	// outputTokenLatency is the latency between consecutive tokens, if supported, or by chunks/tokens otherwise, by backend, model.
	// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiservertime_per_output_token
	outputTokenLatency *prometheus.HistogramVec
}

// newGenAI creates a new genAI metrics instance.
func newGenAI(registry prometheus.Registerer) *genAI {
	m := &genAI{
		tokenUsage: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gen_ai.client.token.usage",
				Help:    "Number of tokens processed.",
				Buckets: []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864},
			},
			[]string{
				"gen_ai.operation.name",
				"gen_ai.system",
				"gen_ai.token.type",
				"gen_ai.request.model",
				"gen_ai.response.model",
			},
		),
		requestLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gen_ai.server.request.duration",
				Help:    "Time spent processing request.",
				Buckets: []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92},
			},
			[]string{
				"gen_ai.operation.name",
				"gen_ai.system",
				"gen_ai.request.model",
				"gen_ai.response.model",
				"error.type",
			},
		),
		firstTokenLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gen_ai.server.time_to_first_token",
				Help:    "Time to receive first token in streaming responses.",
				Buckets: []float64{0.001, 0.005, 0.01, 0.02, 0.04, 0.06, 0.08, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0},
			},
			[]string{
				"gen_ai.operation.name",
				"gen_ai.system",
				"gen_ai.request.model",
				"gen_ai.response.model",
			},
		),
		outputTokenLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gen_ai.server.time_per_output_token",
				Help:    "Time between consecutive tokens in streaming responses.",
				Buckets: []float64{0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2, 0.3, 0.4, 0.5, 0.75, 1.0, 2.5},
			},
			[]string{
				"gen_ai.operation.name",
				"gen_ai.system",
				"gen_ai.request.model",
				"gen_ai.response.model",
			},
		),
	}

	registry.MustRegister(m.tokenUsage)
	registry.MustRegister(m.requestLatency)
	registry.MustRegister(m.firstTokenLatency)
	registry.MustRegister(m.outputTokenLatency)

	return m
}
