// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internaltesting

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// BuildAigwOnDemand builds the aigw binary unless AIGW_BIN is set.
// If AIGW_BIN environment variable is set, it will use that path instead.
func BuildAigwOnDemand() (string, error) {
	return BuildGoBinaryOnDemand("AIGW_BIN", "aigw", "./cmd/aigw")
}

// StartAIGWCLI starts the aigw CLI as a subprocess with the given config file.
func StartAIGWCLI(t *testing.T, aigwBin string, arg ...string) {
	t.Logf("Starting aigw with args: %v", arg)
	cmd := exec.CommandContext(t.Context(), aigwBin, arg...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		err := cmd.Process.Signal(os.Interrupt)
		require.NoError(t, err, "Failed to send interrupt to aigw process")
		_, err = cmd.Process.Wait()
		require.NoError(t, err, "Failed to wait for aigw process to exit")
	})

	t.Logf("aigw process started with PID %d", cmd.Process.Pid)

	// Wait for health check.
	t.Log("Waiting for aigw to start (Envoy admin endpoint)...")
	require.Eventually(t, func() bool {
		reqCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://localhost:9901/ready", nil)
		if err != nil {
			t.Logf("Health check request failed: %v", err)
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("Health check connection failed: %v", err)
			return false
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Logf("Failed to read health check response: %v", err)
			return false
		}

		bodyStr := strings.TrimSpace(string(body))
		t.Logf("Health check: status=%d, body='%s'", resp.StatusCode, bodyStr)
		return resp.StatusCode == http.StatusOK && strings.ToLower(bodyStr) == "live"
	}, 180*time.Second, 2*time.Second)

	// Wait for MCP endpoint.
	t.Log("Waiting for MCP endpoint to be available...")
	require.Eventually(t, func() bool {
		reqCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://localhost:1975/mcp", nil)
		if err != nil {
			t.Logf("MCP endpoint request failed: %v", err)
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("MCP endpoint connection failed: %v", err)
			return false
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		t.Logf("MCP endpoint: status=%d", resp.StatusCode)
		return resp.StatusCode < 500
	}, 120*time.Second, 2*time.Second)

	t.Log("aigw CLI is ready with MCP endpoint")
}
