// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

// TestExtProcCustomMetrics verifies that the custom metrics example properly
// provides TTFT (Time To First Token) and ITL (Inter-Token Latency) values
// as dynamic metadata that Envoy can use in access logs.
//
// Note: `x.NewCustomChatCompletionMetric` is only implemented for streaming requests.
func TestExtProcCustomMetrics(t *testing.T) {
	// Use the custom extproc binary which demonstrates custom metrics.
	env := startTestEnvironment(t, extprocCustomBin, extprocCustomConfig, nil, envoyCustomConfig)

	listenerPort := env.EnvoyListenerPort()

	// Use CassetteChatStreaming which has "stream": true in the request JSON.
	// This is required for c.stream to be true and TTFT/ITL metrics to be added.
	req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d/v1", listenerPort), testopenai.CassetteChatStreaming)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Always consume the body to ensure the request completes before assertions.
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Unexpected status code: %d; body: %s", resp.StatusCode, body)
	}

	// Dynamic TTFT and ITL from custom metrics, plus the test_cost from extproc.yaml.
	expectAccessLog := `TTFT=1234 ITL=5678 TEST_COST=19 ALL={"backend_name":"openai","test_cost":19,"token_latency_itl":5678,"token_latency_ttft":1234}`
	require.Eventually(t, func() bool {
		accessLog := strings.TrimSpace(env.EnvoyStdout())
		return accessLog == expectAccessLog
	}, time.Second*3, time.Millisecond*20, "access log was never written")
}
