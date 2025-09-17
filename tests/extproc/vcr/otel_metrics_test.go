// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

// metricsTestCase defines the expected behavior for each cassette.
type metricsTestCase struct {
	cassette    testopenai.Cassette
	isStreaming bool // whether this is a streaming response.
	isError     bool // whether this is an error response.
}

// buildMetricsTestCases returns all test cases with their expected behaviors.
func buildMetricsTestCases() []metricsTestCase {
	var cases []metricsTestCase

	// Iterate through ALL chat cassettes.
	for _, cassette := range testopenai.ChatCassettes() {
		tc := metricsTestCase{
			cassette: cassette,
		}

		// Set special case flags.
		switch cassette {
		case testopenai.CassetteChatBadRequest,
			testopenai.CassetteChatUnknownModel,
			testopenai.CassetteChatNoMessages:
			tc.isError = true

		case testopenai.CassetteChatStreaming,
			testopenai.CassetteChatStreamingWebSearch,
			testopenai.CassetteChatStreamingDetailedUsage:
			tc.isStreaming = true
		}

		cases = append(cases, tc)
	}

	return cases
}

// TestOtelMetrics_ChatCompletions tests that metrics are properly exported via OTLP.
// when processing chat completion requests.
func TestOtelMetrics_ChatCompletions(t *testing.T) {
	env := setupOtelTestEnvironment(t)

	listenerPort := env.EnvoyListenerPort()

	wasBadGateway := false
	for _, tc := range buildMetricsTestCases() {
		if wasBadGateway {
			return // rather than also failing subsequent tests, which confuses root cause.
		}

		t.Run(tc.cassette.String(), func(t *testing.T) {
			// Send request.
			req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d/v1", listenerPort), tc.cassette)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			if failIfBadGateway(t, resp) {
				wasBadGateway = true
				return // stop further tests if we got a bad gateway.
			}

			// Always read the content.
			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)

			// Get the span to extract actual token counts and duration.
			span := env.collector.TakeSpan()

			// Collect all metrics within the timeout period.
			allMetrics := env.collector.TakeAllMetrics()

			// Filter out empty metric resources (OTEL sends periodic empty batches).
			var metrics []*metricsv1.ResourceMetrics
			for _, rm := range allMetrics {
				if len(rm.ScopeMetrics) > 0 {
					metrics = append(metrics, rm)
				}
			}
			require.NotEmpty(t, metrics)

			verifyMetrics(t, metrics, span, tc.isStreaming, tc.isError)
		})
	}
}

func verifyMetrics(t *testing.T, metrics []*metricsv1.ResourceMetrics, span *tracev1.Span, isStreaming bool, isError bool) {
	require.NotNil(t, span)

	require.Equal(t,
		getSpanAttributeInt(span.Attributes, "llm.token_count.prompt"),
		getMetricValueByAttribute(metrics, "gen_ai.client.token.usage", "gen_ai.token.type", "input"))

	require.Equal(t,
		getSpanAttributeInt(span.Attributes, "llm.token_count.completion"),
		getMetricValueByAttribute(metrics, "gen_ai.client.token.usage", "gen_ai.token.type", "output"))

	spanDurationSec := float64(span.EndTimeUnixNano-span.StartTimeUnixNano) / 1e9
	metricDurationSec := getMetricHistogramSum(metrics, "gen_ai.server.request.duration")
	require.Greater(t, metricDurationSec, 0.0)
	require.InDelta(t, spanDurationSec, metricDurationSec, 0.3)

	if isStreaming && !isError {
		ttft := getMetricHistogramSum(metrics, "gen_ai.server.time_to_first_token")
		require.Greater(t, ttft, 0.0)
		require.Less(t, ttft, metricDurationSec)

		outputTokens := getSpanAttributeInt(span.Attributes, "llm.token_count.completion")
		if outputTokens > 0 {
			tpot := getMetricHistogramSum(metrics, "gen_ai.server.time_per_output_token")
			require.Greater(t, tpot, 0.0)
			require.Less(t, tpot, metricDurationSec)
		}
	}
}

func getSpanAttributeInt(attrs []*commonv1.KeyValue, key string) int64 {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value.GetIntValue()
		}
	}
	return 0
}

func getMetricValueByAttribute(metrics []*metricsv1.ResourceMetrics, metricName string, attrKey string, attrValue string) int64 {
	for _, resourceMetrics := range metrics {
		for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
			for _, metric := range scopeMetrics.Metrics {
				if metric.Name == metricName {
					histogram := metric.GetHistogram()
					if histogram != nil {
						for _, dp := range histogram.DataPoints {
							for _, attr := range dp.Attributes {
								if attr.Key == attrKey && attr.Value.GetStringValue() == attrValue {
									return int64(dp.GetSum())
								}
							}
						}
					}
				}
			}
		}
	}
	return 0
}

func getMetricHistogramSum(metrics []*metricsv1.ResourceMetrics, metricName string) float64 {
	for _, resourceMetrics := range metrics {
		for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
			for _, metric := range scopeMetrics.Metrics {
				if metric.Name == metricName {
					histogram := metric.GetHistogram()
					if histogram != nil && len(histogram.DataPoints) > 0 {
						return histogram.DataPoints[0].GetSum()
					}
				}
			}
		}
	}
	return 0
}
