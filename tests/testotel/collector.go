// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package testotel provides test utilities for OpenTelemetry tracing tests.
// This is not internal for use in cmd/extproc/mainlib/main_test.go.
package testotel

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// otlpTimeout is the timeout for spans to read back.
const otlpTimeout = 1 * time.Second // OTEL_BSP_SCHEDULE_DELAY + overhead..

// StartOTLPCollector starts a test OTLP collector server that receives trace data.
func StartOTLPCollector() *OTLPCollector {
	spanCh := make(chan *tracev1.ResourceSpans, 10)
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
			spanCh <- resourceSpans
		}

		w.WriteHeader(http.StatusOK)
	})
	s := httptest.NewServer(mux)
	env := []string{
		fmt.Sprintf("OTEL_EXPORTER_OTLP_ENDPOINT=%s", s.URL),
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_SERVICE_NAME=ai-gateway-extproc",
		"OTEL_BSP_SCHEDULE_DELAY=100",
	}
	return &OTLPCollector{s, env, spanCh}
}

type OTLPCollector struct {
	s      *httptest.Server
	env    []string
	spanCh chan *tracev1.ResourceSpans
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

// Close shuts down the collector and cleans up resources.
func (o *OTLPCollector) Close() {
	o.s.Close()
}
