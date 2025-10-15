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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
	"github.com/envoyproxy/ai-gateway/tests/internal/testopeninference"
)

func TestOtelOpenAICompletions_span(t *testing.T) {
	env := setupOtelTestEnvironment(t)

	listenerPort := env.EnvoyListenerPort()

	was5xx := false
	for _, cassette := range testopenai.CompletionCassettes() {
		if strings.HasPrefix(cassette.String(), "azure-") {
			continue // Skip Azure as they are tested separately.
		}

		if was5xx {
			return // rather than also failing subsequent tests, which confuses root cause.
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

func TestOtelOpenAICompletions_span_modelNameOverride(t *testing.T) {
	env := setupOtelTestEnvironment(t)
	listenerPort := env.EnvoyListenerPort()

	req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), testopenai.CassetteCompletionBasic)
	require.NoError(t, err)
	// Set the x-test-backend which envoy.yaml routes to the openai-completions-override
	// backend in extproc.yaml. This backend overrides the model to babbage-002.
	req.Header.Set("x-test-backend", "openai-completions-override")
	originalModel := "gpt-3.5-turbo-instruct"
	replaceRequestModel(t, req, originalModel)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode, "Response body: %s", string(body))

	span := env.collector.TakeSpan()
	require.NotNil(t, span)
	requestModel := getInvocationModel(span.Attributes, "llm.invocation_parameters")
	// TODO: Until trace attribute recording is moved to the upstream filter,
	// llm.invocation_parameters is the original model, not the override.
	require.Equal(t, originalModel, requestModel)
}

// TestOtelOpenAICompletions_propagation tests that the LLM span continues
// the trace in headers.
func TestOtelOpenAICompletions_propagation(t *testing.T) {
	env := setupOtelTestEnvironment(t)
	listenerPort := env.EnvoyListenerPort()

	req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), testopenai.CassetteCompletionBasic)
	require.NoError(t, err)
	traceID := "12345678901234567890123456789012"
	req.Header.Add("traceparent", "00-"+traceID+"-1234567890123456-01")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode, "Response body: %s", string(body))

	span := env.collector.TakeSpan()
	require.NotNil(t, span)
	actualTraceID := hex.EncodeToString(span.TraceId)
	require.Equal(t, traceID, actualTraceID)
}
