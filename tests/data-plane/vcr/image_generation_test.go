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

func TestOpenAIImageGeneration(t *testing.T) {
	env := startTestEnvironment(t, nil)

	listenerPort := env.EnvoyListenerPort()

	cassettes := testopenai.ImageCassettes()

	was5xx := false
	for _, cassette := range cassettes {
		if was5xx {
			return // stop early on infrastructure failures to avoid cascading errors
		}
		t.Run(cassette.String(), func(t *testing.T) {
			req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), cassette)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			if resp.StatusCode == http.StatusBadGateway {
				was5xx = true
			}

			expectedBody := testopenai.ResponseBody(cassette)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, expectedBody, string(body))
		})
	}
}
