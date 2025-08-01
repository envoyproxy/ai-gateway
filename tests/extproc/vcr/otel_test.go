// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/envoyproxy/ai-gateway/tests/internal/testenvironment"
)

// otlpTimeout is the timeout for spans to read back.
// TODO: figure out why this is so long and reduce it.
const otlpTimeout = 5 * time.Second

func startOTLPCollector() (*httptest.Server, chan *tracev1.ResourceSpans) {
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
	server := httptest.NewServer(mux)
	return server, spanCh
}

// otelTestEnvironment holds all the services needed for OTEL tests.
type otelTestEnvironment struct {
	*testenvironment.TestEnvironment
	spanCh    chan *tracev1.ResourceSpans
	collector *httptest.Server
}

// setupOtelTestEnvironment starts all required services and returns ports and a closer.
func setupOtelTestEnvironment(t *testing.T, extraExtProcEnv ...string) *otelTestEnvironment {
	// Start OTLP collector.
	collector, spanCh := startOTLPCollector()
	t.Cleanup(collector.Close)

	extprocEnv := append([]string{
		fmt.Sprintf("OTEL_EXPORTER_OTLP_ENDPOINT=%s", collector.URL),
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_SERVICE_NAME=ai-gateway-extproc",
	}, extraExtProcEnv...)

	testEnv := startTestEnvironment(t, extprocBin, extprocConfig, extprocEnv, envoyConfig)

	return &otelTestEnvironment{
		TestEnvironment: testEnv,
		spanCh:          spanCh,
		collector:       collector,
	}
}

// failIfBadGateway because is likely a sign of a broken ExtProc or Envoy.
func failIfBadGateway(t *testing.T, resp *http.Response) bool {
	if resp.StatusCode == http.StatusBadGateway {
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		t.Fatalf("received 502 response with body: %s", string(body))
		return true
	}
	return false
}
