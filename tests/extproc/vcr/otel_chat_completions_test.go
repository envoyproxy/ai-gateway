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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
	"github.com/envoyproxy/ai-gateway/tests/internal/testopeninference"
)

func TestOtelOpenAIChatCompletions(t *testing.T) {
	t.Skip("otel not implemented yet")
	env := setupOtelTestEnvironment(t)

	listenerPort := env.EnvoyListenerPort()
	spanCh := env.spanCh

	wasBadGateway := false
	for _, cassette := range testopenai.ChatCassettes() {
		if wasBadGateway {
			return // rather than also failing subsequent tests, which confuses root cause.
		}

		expected, err := testopeninference.GetChatSpan(t.Context(), io.Discard, cassette)
		require.NoError(t, err)

		t.Run(cassette.String(), func(t *testing.T) {
			// Clear span channel.
			select {
			case <-spanCh:
			default:
			}

			// Send request.
			req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d/v1", listenerPort), cassette)
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

			// Wait for span with timeout.
			select {
			case resourceSpans := <-spanCh:
				require.NotEmpty(t, resourceSpans.ScopeSpans, "expected at least one span")
				actualSpan := resourceSpans.ScopeSpans[0].Spans[0]

				testopeninference.RequireSpanEqual(t, expected, actualSpan)
			case <-time.After(otlpTimeout):
				t.Fatal("timeout waiting for span")
			}
		})
	}
}
