// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package testotel provides test utilities for OpenTelemetry tracing and metrics tests.
// This is not internal for use in cmd/extproc/mainlib/main_test.go.
package testotel

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	collectmetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// otlpTimeout is the timeout for spans to read back.
const otlpTimeout = 1 * time.Second // OTEL_BSP_SCHEDULE_DELAY + overhead..

// StartOTLPCollector starts a test OTLP collector server that receives trace and metrics data.
func StartOTLPCollector() *OTLPCollector {
	spanCh := make(chan *tracev1.ResourceSpans, 10)
	metricsCh := make(chan *metricsv1.ResourceMetrics, 10)
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}

		var traces collecttracev1.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &traces); err != nil {
			http.Error(w, "Failed to parse traces", http.StatusBadRequest)
			return
		}

		for _, resourceSpans := range traces.ResourceSpans {
			timeout := time.After(otlpTimeout)
			select {
			case spanCh <- resourceSpans:
			case <-timeout:
				// Avoid blocking if the channel is full. Likely indicates a test issue or spans not being read like
				// the ones emitted during test shutdown. Otherwise, testerver shutdown blocks the test indefinitely.
				fmt.Println("Warning: Dropping spans due to timeout")
			}
		}

		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}

		var metrics collectmetricsv1.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &metrics); err != nil {
			http.Error(w, "Failed to parse metrics", http.StatusBadRequest)
			return
		}

		for _, resourceMetrics := range metrics.ResourceMetrics {
			timeout := time.After(otlpTimeout)
			select {
			case metricsCh <- resourceMetrics:
			case <-timeout:
				// Avoid blocking if the channel is full. Likely indicates a test issue or metrics not being read like
				// the ones emitted during test shutdown. Otherwise, testerver shutdown blocks the test indefinitely.
				fmt.Println("Warning: Dropping metrics due to timeout")
			}
		}

		w.WriteHeader(http.StatusOK)
	})

	s := httptest.NewServer(mux)
	env := []string{
		fmt.Sprintf("OTEL_EXPORTER_OTLP_ENDPOINT=%s", s.URL),
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_SERVICE_NAME=ai-gateway-extproc",
		"OTEL_BSP_SCHEDULE_DELAY=100",
		"OTEL_METRIC_EXPORT_INTERVAL=100",
		// Use delta temporality to prevent metric accumulation across subtests.
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta",
	}
	return &OTLPCollector{s, env, spanCh, metricsCh}
}

type OTLPCollector struct {
	s         *httptest.Server
	env       []string
	spanCh    chan *tracev1.ResourceSpans
	metricsCh chan *metricsv1.ResourceMetrics
}

// Env returns the environment variables needed to configure the OTLP collector.
func (o *OTLPCollector) Env() []string {
	return o.env
}

// SetEnv calls setenv for each environment variable in Env.
func (o *OTLPCollector) SetEnv(setenv func(key string, value string)) {
	for _, env := range o.Env() {
		kv := strings.SplitN(env, "=", 2)
		if len(kv) == 2 {
			setenv(kv[0], kv[1])
		}
	}
}

// TakeSpan returns a single span or nil if none were recorded.
func (o *OTLPCollector) TakeSpan() *tracev1.Span {
	select {
	case resourceSpans := <-o.spanCh:
		if len(resourceSpans.ScopeSpans) == 0 || len(resourceSpans.ScopeSpans[0].Spans) == 0 {
			return nil
		}
		return resourceSpans.ScopeSpans[0].Spans[0]
	case <-time.After(otlpTimeout):
		return nil
	}
}

// DrainMetrics returns metrics or nil if none were recorded.
func (o *OTLPCollector) DrainMetrics() *metricsv1.ResourceMetrics {
	select {
	case resourceMetrics := <-o.metricsCh:
		return resourceMetrics
	case <-time.After(otlpTimeout):
		return nil
	}
}

// TakeMetrics collects metrics until the expected count is reached or a timeout occurs.
func (o *OTLPCollector) TakeMetrics(expectedCount int) []*metricsv1.ResourceMetrics {
	var metrics []*metricsv1.ResourceMetrics
	deadline := time.After(otlpTimeout)

	// Helper to count total metrics across all ResourceMetrics.
	countMetrics := func() int {
		total := 0
		for _, rm := range metrics {
			for _, sm := range rm.ScopeMetrics {
				total += len(sm.Metrics)
			}
		}
		return total
	}

	for {
		select {
		case resourceMetrics := <-o.metricsCh:
			metrics = append(metrics, resourceMetrics)
			if countMetrics() >= expectedCount {
				// Drain any additional metrics that arrive immediately after.
				time.Sleep(50 * time.Millisecond)
			drainLoop:
				for {
					select {
					case rm := <-o.metricsCh:
						metrics = append(metrics, rm)
					default:
						break drainLoop
					}
				}
				return metrics
			}
		case <-deadline:
			return metrics
		}
	}
}

// Close shuts down the collector and cleans up resources.
func (o *OTLPCollector) Close() {
	o.s.Close()
}
