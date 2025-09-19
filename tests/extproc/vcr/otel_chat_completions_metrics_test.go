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
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

// otelChatMetricsTestCase defines the expected behavior for each cassette.
type otelChatMetricsTestCase struct {
	cassette    testopenai.Cassette
	isStreaming bool // whether this is a streaming response.
	isError     bool // whether this is an error response.
}

// buildOtelChatMetricsTestCases returns all test cases with their expected behaviors.
func buildOtelChatMetricsTestCases() []otelChatMetricsTestCase {
	var cases []otelChatMetricsTestCase
	for _, cassette := range testopenai.ChatCassettes() {
		tc := otelChatMetricsTestCase{cassette: cassette}
		switch cassette {
		case testopenai.CassetteChatBadRequest, testopenai.CassetteChatUnknownModel, testopenai.CassetteChatNoMessages:
			tc.isError = true
		case testopenai.CassetteChatStreaming, testopenai.CassetteChatStreamingWebSearch, testopenai.CassetteChatStreamingDetailedUsage:
			tc.isStreaming = true
		}
		cases = append(cases, tc)
	}
	return cases
}

// TestOtelOpenAIChatCompletions_metrics tests that metrics are properly exported via OTLP for chat completion requests.
func TestOtelOpenAIChatCompletions_metrics(t *testing.T) {
	env := setupOtelTestEnvironment(t)
	listenerPort := env.EnvoyListenerPort()
	wasBadGateway := false

	for _, tc := range buildOtelChatMetricsTestCases() {
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
			require.NotNil(t, span)

			// Collect all metrics within the timeout period.
			allMetrics := env.collector.TakeAllMetrics()
			metrics := requireScopeMetrics(t, allMetrics)

			// Get expected model names from span
			requestModel := getInvocationModel(span.Attributes, "llm.invocation_parameters")
			responseModel := getSpanAttributeString(span.Attributes, "llm.model_name")

			// Verify each metric in separate functions.
			verifyTokenUsageMetrics(t, "chat", metrics, span, requestModel, responseModel, tc.isError)
			verifyRequestDurationMetrics(t, "chat", metrics, span, requestModel, responseModel, tc.isError)
			if tc.isStreaming && !tc.isError {
				verifyTimeToFirstTokenMetrics(t, metrics, requestModel, responseModel)
				verifyTimePerOutputTokenMetrics(t, metrics, span, requestModel, responseModel)
			}
		})
	}
}

// verifyTimeToFirstTokenMetrics verifies the gen_ai.server.time_to_first_token metric including its values and attributes.
func verifyTimeToFirstTokenMetrics(t *testing.T, metrics *metricsv1.ScopeMetrics, requestModel, responseModel string) {
	t.Helper()

	ttft := getMetricHistogramSum(metrics, "gen_ai.server.time_to_first_token")
	metricDurationSec := getMetricHistogramSum(metrics, "gen_ai.server.request.duration")
	require.Greater(t, ttft, 0.0)
	require.Less(t, ttft, metricDurationSec)

	expectedAttrs := map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.provider.name":  "openai",
		"gen_ai.request.model":  requestModel,
		"gen_ai.response.model": responseModel,
	}
	verifyMetricAttributes(t, metrics, "gen_ai.server.time_to_first_token", expectedAttrs)
}

// verifyTimePerOutputTokenMetrics verifies the gen_ai.server.time_per_output_token metric including its values and attributes.
func verifyTimePerOutputTokenMetrics(t *testing.T, metrics *metricsv1.ScopeMetrics, span *tracev1.Span, requestModel, responseModel string) {
	t.Helper()

	outputTokens := getSpanAttributeInt(span.Attributes, "llm.token_count.completion")
	if outputTokens <= 0 {
		return // Skip if no output tokens.
	}

	tpot := getMetricHistogramSum(metrics, "gen_ai.server.time_per_output_token")
	metricDurationSec := getMetricHistogramSum(metrics, "gen_ai.server.request.duration")
	require.Greater(t, tpot, 0.0)
	require.Less(t, tpot, metricDurationSec)

	expectedAttrs := map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.provider.name":  "openai",
		"gen_ai.request.model":  requestModel,
		"gen_ai.response.model": responseModel,
	}
	verifyMetricAttributes(t, metrics, "gen_ai.server.time_per_output_token", expectedAttrs)
}
