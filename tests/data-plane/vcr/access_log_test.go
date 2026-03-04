// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	"bufio"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/tests/internal/dataplaneenv"
	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

type accessLogLine struct {
	SessionID string `json:"session.id"`
}

func TestAccessLogSessionID(t *testing.T) {
	env := dataplaneenv.StartTestEnvironment(t,
		requireUpstream, map[string]int{"upstream": 11434},
		extprocConfig, nil, envoyConfig, true, false, 120*time.Second, "-logRequestHeaderAttributes", "x-session-id:session.id",
	)
	listenerPort := env.EnvoyListenerPort()

	req, err := testopenai.NewRequest(t.Context(), fmt.Sprintf("http://localhost:%d", listenerPort), testopenai.CassetteChatBasic)
	require.NoError(t, err)
	req.Header.Set("X-Session-Id", "session-123")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Eventually(t, func() bool {
		accessLog := env.EnvoyStdout()
		scanner := bufio.NewScanner(strings.NewReader(accessLog))
		for scanner.Scan() {
			var line accessLogLine
			if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
				continue
			}
			if line.SessionID == "session-123" {
				return true
			}
		}
		return false
	}, 15*time.Second, 500*time.Millisecond)
}
