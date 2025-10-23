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

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

// otelImageGenerationMetricsTestCase defines the expected behavior for each cassette.
type otelImageGenerationMetricsTestCase struct {
	cassette testopenai.Cassette
	isError  bool // whether this is an error response.
}

// buildOtelImageGenerationMetricsTestCases returns all test cases with their expected behaviors.
func buildOtelImageGenerationMetricsTestCases() []otelImageGenerationMetricsTestCase {
	var cases []otelImageGenerationMetricsTestCase
	for _, cassette := range testopenai.ImageCassettes() {
		tc := otelImageGenerationMetricsTestCase{cassette: cassette}
		// Currently we only have happy-path cassettes for image generation
		cases = append(cases, tc)
	}
	return cases
}

// TestOtelOpenAIImageGeneration_metrics tests that metrics are properly exported via OTLP for image generation requests.
func TestOtelOpenAIImageGeneration_metrics(t *testing.T) {
	env := setupOtelTestEnvironment(t)
	listenerPort := env.EnvoyListenerPort()
	was5xx := false

	for _, tc := range buildOtelImageGenerationMetricsTestCases() {
		if was5xx {
			return // rather than also failing subsequent tests, which confuses root cause.
		}

		t.Run(tc.cassette.String(), func(t *testing.T) {
			// Send request.
			req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), tc.cassette)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			failIf5xx(t, resp, &was5xx)

			// Always read the content.
			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)

			// Get the span to extract duration and model attributes.
			span := env.collector.TakeSpan()
			require.NotNil(t, span)

			// Collect all metrics within the timeout period.
			// Image generation should have 2 metrics: token usage + request duration
			allMetrics := env.collector.TakeMetrics(2)
			metrics := requireScopeMetrics(t, allMetrics)

			// For image generation spans, we record llm.model_name on request; no response model attribute exists.
			// In non-override cases, original = request = response.
			requestModel := getSpanAttributeString(span.Attributes, "llm.model_name")
			originalModel := requestModel
			responseModel := requestModel

			// Verify metrics.
			verifyTokenUsageMetricsWithOriginal(t, "image_generation", metrics, span, originalModel, requestModel, responseModel, tc.isError)
			verifyRequestDurationMetricsWithOriginal(t, "image_generation", metrics, span, originalModel, requestModel, responseModel, tc.isError)
		})
	}
}
