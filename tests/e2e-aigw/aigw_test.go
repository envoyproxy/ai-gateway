// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2emcp

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/func-e/experimental/admin"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

var (
	aigwBin     string
	ollamaModel string
)

func TestMain(m *testing.M) {
	var err error
	if aigwBin, err = buildAigwOnDemand(); err == nil {
		if ollamaModel, err = internaltesting.GetOllamaModel(internaltesting.ThinkingModel); err == nil {
			err = internaltesting.CheckIfOllamaReady(ollamaModel)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start tests: %v\n", err)
		os.Exit(1)
	}

	// Check if Goose CLI is available.
	cmd := exec.Command("goose", "--version")
	if err := cmd.Run(); err != nil {
		log.Printf("Goose CLI is not available: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Goose CLI is available\n")

	os.Exit(m.Run())
}

// buildAigwOnDemand builds the aigw binary unless AIGW_BIN is set.
// If AIGW_BIN environment variable is set, it will use that path instead.
func buildAigwOnDemand() (string, error) {
	return internaltesting.BuildGoBinaryOnDemand("AIGW_BIN", "aigw", "./cmd/aigw")
}

// startAIGWCLI starts the aigw CLI as a subprocess with the given config file.
func startAIGWCLI(t *testing.T, aigwBin string, env []string, arg ...string) (adminPort int) {
	// aigw has many fixed ports: some are in the envoy subprocess
	gatewayPort := 1975

	// Wait up to 10 seconds for both ports to be free.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for isPortInUse(ctx, gatewayPort) {
		select {
		case <-ctx.Done():
			require.FailNow(t, "Ports still in use after timeout",
				"Port %d is still in use", gatewayPort)
		case <-time.After(500 * time.Millisecond):
			// Retry after a short delay.
		}
	}

	// Capture logs, only dump on failure.
	buffers := internaltesting.DumpLogsOnFail(t, "aigw Stdout", "aigw Stderr")

	t.Logf("Starting aigw with args: %v", arg)
	// Note: do not pass t.Context() to CommandContext, as it's canceled
	// *before* t.Cleanup functions are called.
	//
	// > Context returns a context that is canceled just before
	// > Cleanup-registered functions are called.
	//
	// That means the subprocess gets killed before we can send it an interrupt
	// signal for graceful shutdown, which results in orphaned subprocesses.
	cmdCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(cmdCtx, aigwBin, arg...)
	cmd.Stdout = buffers[0]
	cmd.Stderr = buffers[1]
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 3 * time.Second // auto-kill after 3 seconds.

	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		defer cancel()
		// Don't use require.XXX inside cleanup functions as they call
		// runtime.Goexit preventing further cleanup functions from running.

		// Graceful shutdown, should kill the Envoy subprocess, too.
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Logf("Failed to send interrupt to aigw process: %v", err)
		}
		// Wait for the process to exit gracefully, in worst case this is
		// killed in 3 seconds by WaitDelay above. In that case, you may
		// have a zombie Envoy process left behind!
		if _, err := cmd.Process.Wait(); err != nil {
			t.Logf("Failed to wait for aigw process to exit: %v", err)
		}

		// Delete the hard-coded path to certs defined in Envoy AI Gateway
		if err := os.RemoveAll("/tmp/envoy-gateway/certs"); err != nil {
			t.Logf("Failed to delete envoy gateway certs: %v", err)
		}
	})

	t.Logf("aigw process started with PID %d", cmd.Process.Pid)

	t.Log("Waiting for aigw to start (Envoy admin endpoint)...")

	adminClient, err := admin.NewAdminClient(t.Context(), cmd.Process.Pid)
	require.NoError(t, err)

	err = adminClient.AwaitReady(t.Context(), time.Second)
	require.NoError(t, err)

	// Wait for MCP endpoint using RequireEventuallyNoError.
	t.Log("Waiting for MCP endpoint to be available...")
	internaltesting.RequireEventuallyNoError(t, func() error {
		reqCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
			"http://localhost:1975/mcp", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			return fmt.Errorf("MCP endpoint returned status %d", resp.StatusCode)
		}
		return nil
	}, 120*time.Second, 2*time.Second,
		"MCP endpoint never became available")

	t.Log("aigw CLI is ready with MCP endpoint")
	return adminClient.Port()
}

// Function to check if a port is in use (returns true if listening).
func isPortInUse(ctx context.Context, port int) bool {
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	if err == nil {
		_ = conn.Close()
		return true
	}
	return false
}
