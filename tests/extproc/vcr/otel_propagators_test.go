// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

// TestOtelPropagators tests that the OTEL_PROPAGATORS environment variable properly controls propagation.
// See: https://opentelemetry.io/docs/languages/sdk-configuration/general/#otel_propagators
func TestOtelPropagators(t *testing.T) {
	t.Skip("otel not implemented yet")

	// Just test 2 propagators to prove the SDK is working.
	tests := []struct {
		propagator string
		headers    map[string]string
		checkTrace func(t *testing.T, actualTraceID string)
	}{
		{
			propagator: "b3",
			headers: map[string]string{
				"b3": "1234567890abcdef1234567890abcdef-1234567890abcdef-1",
			},
			checkTrace: func(t *testing.T, actualTraceID string) {
				require.Equal(t, "1234567890abcdef1234567890abcdef", actualTraceID, "B3 trace ID should be preserved")
			},
		},
		{
			propagator: "tracecontext",
			headers: map[string]string{
				"traceparent": "00-12345678901234567890123456789012-1234567890123456-01",
			},
			checkTrace: func(t *testing.T, actualTraceID string) {
				require.Equal(t, "12345678901234567890123456789012", actualTraceID, "W3C trace ID should be preserved")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.propagator, func(t *testing.T) {
			env := setupOtelTestEnvironment(t, fmt.Sprintf("OTEL_PROPAGATORS=%s", tt.propagator))
			listenerPort := env.EnvoyListenerPort()

			req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d/v1", listenerPort), testopenai.CassetteChatBasic)
			require.NoError(t, err)

			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			client := &http.Client{}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			require.Equal(t, http.StatusOK, resp.StatusCode)

			select {
			case resourceSpans := <-env.spanCh:
				require.NotEmpty(t, resourceSpans.ScopeSpans)
				span := resourceSpans.ScopeSpans[0].Spans[0]
				actualTraceID := hex.EncodeToString(span.TraceId)
				tt.checkTrace(t, actualTraceID)
			case <-time.After(otlpTimeout):
				t.Fatal("Timeout waiting for spans")
			}
		})
	}
}
