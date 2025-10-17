// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
	"github.com/envoyproxy/ai-gateway/tests/internal/testopeninference"
)

// TestOtelOpenAIImageGeneration_tracing validates that image generation spans
// emitted by the gateway match the OpenInference reference spans for the same cassette.
func TestOtelOpenAIImageGeneration_tracing(t *testing.T) {
	env := setupOtelTestEnvironment(t)
	listenerPort := env.EnvoyListenerPort()

	was5xx := false
	for _, cassette := range testopenai.ImageCassettes() {
		if was5xx {
			return // avoid cascading failures obscuring the first root cause
		}

		expected, err := testopeninference.GetSpan(t.Context(), io.Discard, cassette)
		require.NoError(t, err)

		t.Run(cassette.String(), func(t *testing.T) {
			// Send request.
			req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), cassette)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			failIf5xx(t, resp, &was5xx)

			// Always read the content.
			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)

			span := env.collector.TakeSpan()
			testopeninference.RequireSpanEqual(t, expected, span)

			// Also drain any metrics that might have been sent.
			_ = env.collector.DrainMetrics()
		})
	}
}

// TestOtelOpenAIImageGeneration_propagation verifies that the image generation LLM span
// participates in the incoming trace when W3C trace context is provided.
func TestOtelOpenAIImageGeneration_propagation(t *testing.T) {
	env := setupOtelTestEnvironment(t)
	listenerPort := env.EnvoyListenerPort()

	req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), testopenai.CassetteImageGenerationBasic)
	require.NoError(t, err)

	traceID := "12345678901234567890123456789012"
	req.Header.Add("traceparent", "00-"+traceID+"-1234567890123456-01")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	span := env.collector.TakeSpan()
	require.NotNil(t, span)
	actualTraceID := hex.EncodeToString(span.TraceId)
	require.Equal(t, traceID, actualTraceID)
}
