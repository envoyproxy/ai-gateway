// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

// TestPrometheusMetrics verifies that metrics are properly exported via Prometheus
// when processing a chat completion request. This test uses the default configuration
// which exposes metrics on the /metrics endpoint.
func TestPrometheusMetrics(t *testing.T) {
	// Start test environment with default configuration.
	env := startTestEnvironment(t, extprocBin, extprocConfig, nil, envoyConfig)

	listenerPort := env.EnvoyListenerPort()
	metricsPort := env.ExtProcMetricsPort()

	// Use the basic chat cassette for this test.
	cassette := testopenai.CassetteChatBasic

	// Send the chat request.
	req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d/v1", listenerPort), cassette)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Always consume the body to ensure the request completes.
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Wait for metrics to be available with exponential backoff.
	var metricsBody []byte
	require.Eventually(t, func() bool {
		metricsReq, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", metricsPort), nil)
		if reqErr != nil {
			return false
		}

		metricsResp, respErr := http.DefaultClient.Do(metricsReq)
		if respErr != nil {
			return false
		}
		defer metricsResp.Body.Close()

		metricsBody, respErr = io.ReadAll(metricsResp.Body)
		if respErr != nil {
			return false
		}

		// Check if we have the expected metrics.
		bodyStr := string(metricsBody)
		return strings.Contains(bodyStr, "gen_ai_server_request_duration_seconds") &&
			strings.Contains(bodyStr, "gen_ai_client_token_usage_token")
	}, 3*time.Second, 100*time.Millisecond)

	// Parse the Prometheus metrics.
	parser := expfmt.TextParser{}
	metricFamilies, err := parser.TextToMetricFamilies(strings.NewReader(string(metricsBody)))
	require.NoError(t, err)

	// Verify expected GenAI metrics are present.
	require.Contains(t, metricFamilies, "gen_ai_server_request_duration_seconds")
	require.Contains(t, metricFamilies, "gen_ai_client_token_usage_token")

	// Verify specific labels for request duration metric.
	requestDuration := metricFamilies["gen_ai_server_request_duration_seconds"]
	require.NotEmpty(t, requestDuration.Metric)

	// Extract labels from the first metric (assuming consistent labels across all).
	labels := make(map[string]string)
	for _, label := range requestDuration.Metric[0].Label {
		labels[*label.Name] = *label.Value
	}
	require.Equal(t, "chat", labels["gen_ai_operation_name"])
	require.Equal(t, "openai", labels["gen_ai_provider_name"])
	require.Equal(t, "gpt-5-nano", labels["gen_ai_request_model"])
	require.Equal(t, "envoyproxy/ai-gateway", labels["otel_scope_name"])

	// Verify token usage metrics for both input and output tokens.
	tokenUsage := metricFamilies["gen_ai_client_token_usage_token"]
	require.NotEmpty(t, tokenUsage.Metric)

	// Check for input and output token metrics.
	var hasInput, hasOutput bool
	for _, metric := range tokenUsage.Metric {
		for _, label := range metric.Label {
			if *label.Name == "gen_ai_token_type" {
				switch *label.Value {
				case "input":
					hasInput = true
				case "output":
					hasOutput = true
				}
			}
		}
	}
	require.True(t, hasInput)
	require.True(t, hasOutput)
}
