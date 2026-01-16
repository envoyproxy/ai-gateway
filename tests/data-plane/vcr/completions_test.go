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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testopenai"
)

// completionsTestCase defines the expected behavior for each cassette.
type completionsTestCase struct {
	cassette           testopenai.Cassette
	expectStatusCode   int
	expectResponseBody string // only set this when not the same as what's proxied.
}

// buildCompletionsTestCases returns all test cases with their expected behaviors.
func buildCompletionsTestCases() []completionsTestCase {
	var cases []completionsTestCase
	for _, cassette := range testopenai.CompletionCassettes() {
		tc := completionsTestCase{
			cassette:         cassette,
			expectStatusCode: http.StatusOK, // default to OK
		}

		// Set specific expectations for error cases
		switch cassette {
		case testopenai.CassetteCompletionBadRequest:
			tc.expectStatusCode = http.StatusBadRequest
		case testopenai.CassetteCompletionUnknownModel:
			tc.expectStatusCode = http.StatusNotFound
			// For completions, errors are passed through directly without wrapping
			tc.expectResponseBody = ""
		}

		cases = append(cases, tc)
	}
	return cases
}

func TestOpenAICompletions(t *testing.T) {
	listenerPort := startTestEnvironment(t, nil).EnvoyListenerPort()

	was5xx := false

	for _, tc := range buildCompletionsTestCases() {
		if was5xx {
			return // rather than also failing subsequent tests, which confuses root cause.
		}
		t.Run(tc.cassette.String(), func(t *testing.T) {
			req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), tc.cassette)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			if resp.StatusCode == http.StatusBadGateway {
				was5xx = true // assertions will fail later and log the body.
			}

			// Check status code matches expectation
			assert.Equal(t, tc.expectStatusCode, resp.StatusCode)

			// Safe to use assert as no nil risk and response body explains status.
			expectedBody := tc.expectResponseBody
			if expectedBody == "" {
				expectedBody = testopenai.ResponseBody(tc.cassette)
			}
			assert.Equal(t, expectedBody, string(body))
		})
	}
}
