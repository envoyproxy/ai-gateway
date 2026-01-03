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
	"github.com/envoyproxy/ai-gateway/tests/internal/testopeninference"
)

func TestOtelAzureOpenAIChatCompletions_span(t *testing.T) {
	env := setupOtelTestEnvironment(t)
	listenerPort := env.listenerPort

	expected, err := testopeninference.GetSpan(t.Context(), io.Discard, testopenai.CassetteAzureChatBasic)
	require.NoError(t, err)

	// Use standard OpenAI path but set X-Cassette-Name header to azure-chat-basic
	req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), testopenai.CassetteChatBasic)
	require.NoError(t, err)
	req.Header.Set(testopenai.CassetteNameHeader, testopenai.CassetteAzureChatBasic.String())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode, "Response body: %s", string(body))

	span := env.collector.TakeSpan()
	testopeninference.RequireSpanEqual(t, expected, span)

	_ = env.collector.DrainMetrics()
}
