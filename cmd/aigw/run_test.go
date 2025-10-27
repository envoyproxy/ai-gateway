// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/func-e/api"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

// TestRun verifies that the main run function starts up correctly without making any actual requests.
//
// The real e2e tests are in tests/e2e-aigw.
func TestRun(t *testing.T) {
	ports := internaltesting.RequireRandomPorts(t, 1)
	// TODO: parameterize the main listen port 1975
	adminPort := ports[0]

	// Note: we do not make any real requests here!
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	t.Setenv("OPENAI_API_KEY", "unused")

	buffers := internaltesting.DumpLogsOnFail(t, "aigw Stdout", "aigw Stderr")
	stdout, stderr := buffers[0], buffers[1]
	ctx, cancel := context.WithCancel(t.Context())
	defer cleanupRun(t, cancel)

	opts := testRunOpts(t, func(context.Context, []string, io.Writer) error { return nil })
	require.NoError(t, run(ctx, cmdRun{Debug: true, AdminPort: adminPort}, opts, stdout, stderr))
}

func cleanupRun(t testing.TB, cancel context.CancelFunc) {
	cancel()
	if err := internaltesting.AwaitPortClosed(1975, 10*time.Second); err != nil {
		t.Logf("Failed to close port 1975: %v", err)
	}
	// Delete the hard-coded path to certs defined in Envoy Gateway
	// TODO: Remove once EG supports configurable cert directory
	// https://github.com/envoyproxy/gateway/pull/7225
	if err := os.RemoveAll("/tmp/envoy-gateway/certs"); err != nil {
		t.Logf("Failed to delete envoy gateway certs: %v", err)
	}
}

func TestRunExtprocStartFailure(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "unused")

	ctx := t.Context()
	errChan := make(chan error)
	mockErr := errors.New("mock extproc error")
	go func() {
		opts := testRunOpts(t, func(context.Context, []string, io.Writer) error { return mockErr })
		errChan <- run(ctx, cmdRun{}, opts, os.Stdout, io.Discard)
	}()

	select {
	case <-time.After(10 * time.Second):
		t.Fatal("expected extproc start to fail promptly")
	case err := <-errChan:
		require.ErrorIs(t, err, errExtProcRun)
		require.ErrorIs(t, err, mockErr)
	}
}

func TestRunCmdContext_writeEnvoyResourcesAndRunExtProc(t *testing.T) {
	ports := internaltesting.RequireRandomPorts(t, 1)
	// TODO: parameterize the main listen port 1975
	adminPort := ports[0]

	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "unused")

	runCtx := &runCmdContext{
		envoyGatewayResourcesOut: &bytes.Buffer{},
		stderrLogger:             slog.New(slog.DiscardHandler),
		stderr:                   io.Discard,
		tmpdir:                   t.TempDir(),
		// UNIX doesn't like a long UDS path, so we use a short one.
		// https://unix.stackexchange.com/questions/367008/why-is-socket-path-length-limited-to-a-hundred-chars
		udsPath:         filepath.Join("/tmp", "run.sock"),
		adminPort:       adminPort,
		extProcLauncher: mainlib.Main,
	}
	config := readFileFromProjectRoot(t, "examples/aigw/ollama.yaml")
	ctx, cancel := context.WithCancel(t.Context())
	_, done, _, err := runCtx.writeEnvoyResourcesAndRunExtProc(ctx, config)
	require.NoError(t, err)
	time.Sleep(time.Second)
	cancel()
	// Wait for the external processor to stop.
	require.NoError(t, <-done)
}

//go:embed testdata/gateway_no_listeners.yaml
var gatewayNoListenersConfig string

func TestRunCmdContext_writeEnvoyResourcesAndRunExtProc_noListeners(t *testing.T) {
	ports := internaltesting.RequireRandomPorts(t, 1)
	// TODO: parameterize the main listen port 1975
	adminPort := ports[0]

	runCtx := &runCmdContext{
		envoyGatewayResourcesOut: &bytes.Buffer{},
		stderrLogger:             slog.New(slog.DiscardHandler),
		stderr:                   io.Discard,
		tmpdir:                   t.TempDir(),
		udsPath:                  filepath.Join("/tmp", "run-test.sock"),
		adminPort:                adminPort,
	}

	_, _, _, err := runCtx.writeEnvoyResourcesAndRunExtProc(t.Context(), gatewayNoListenersConfig)
	require.EqualError(t, err, "gateway aigw-run has no listeners configured")
}

func Test_mustStartExtProc(t *testing.T) {
	mockErr := errors.New("mock extproc error")
	runCtx := &runCmdContext{
		stderrLogger:    slog.New(slog.DiscardHandler),
		stderr:          io.Discard,
		tmpdir:          t.TempDir(),
		adminPort:       1064,
		extProcLauncher: func(context.Context, []string, io.Writer) error { return mockErr },
	}
	done := runCtx.mustStartExtProc(t.Context(), filterapi.MustLoadDefaultConfig())
	require.ErrorIs(t, <-done, mockErr)
}

func Test_mustStartExtProc_withHeaderAttributes(t *testing.T) {
	t.Setenv("OTEL_AIGW_METRICS_REQUEST_HEADER_ATTRIBUTES", "x-team-id:team.id,x-user-id:user.id")
	t.Setenv("OTEL_AIGW_SPAN_REQUEST_HEADER_ATTRIBUTES", "x-session-id:session.id,x-user-id:user.id")

	var capturedArgs []string
	runCtx := &runCmdContext{
		stderrLogger: slog.New(slog.DiscardHandler),
		stderr:       io.Discard,
		tmpdir:       t.TempDir(),
		adminPort:    1064,
		extProcLauncher: func(_ context.Context, args []string, _ io.Writer) error {
			capturedArgs = args
			return errors.New("mock error") // Return error to stop execution
		},
	}

	done := runCtx.mustStartExtProc(t.Context(), filterapi.MustLoadDefaultConfig())
	<-done // Wait for completion

	// Verify both metrics and tracing flags are set
	require.Contains(t, capturedArgs, "-metricsRequestHeaderAttributes")
	require.Contains(t, capturedArgs, "-spanRequestHeaderAttributes")

	// Find the index and verify the values
	for i, arg := range capturedArgs {
		if arg == "-metricsRequestHeaderAttributes" {
			require.Less(t, i+1, len(capturedArgs), "metricsRequestHeaderAttributes should have a value")
			require.Equal(t, "x-team-id:team.id,x-user-id:user.id", capturedArgs[i+1])
		}
		if arg == "-spanRequestHeaderAttributes" {
			require.Less(t, i+1, len(capturedArgs), "spanRequestHeaderAttributes should have a value")
			require.Equal(t, "x-session-id:session.id,x-user-id:user.id", capturedArgs[i+1])
		}
	}
}

func TestTryFindEnvoyListenerPort(t *testing.T) {
	gwWithListener := func(port gwapiv1.PortNumber) *gwapiv1.Gateway {
		return &gwapiv1.Gateway{
			Spec: gwapiv1.GatewaySpec{
				Listeners: []gwapiv1.Listener{
					{Port: port},
				},
			},
		}
	}

	tests := []struct {
		name string
		gw   *gwapiv1.Gateway
		want int
	}{
		{
			name: "gateway with no listeners",
			gw:   &gwapiv1.Gateway{},
			want: 0,
		},
		{
			name: "gateway with listener on port 1975",
			gw:   gwWithListener(1975),
			want: 1975,
		},
	}

	runCtx := &runCmdContext{
		stderrLogger: slog.New(slog.DiscardHandler),
		tmpdir:       t.TempDir(),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := runCtx.tryFindEnvoyListenerPort(tt.gw)
			require.Equal(t, tt.want, port)
		})
	}
}

func Test_newEnvoyMiddleware(t *testing.T) {
	tests := []struct {
		name         string
		inputOptions []api.RunOption
	}{
		{
			name: "no input options",
		},
		{
			name:         "options appended",
			inputOptions: []api.RunOption{api.EnvoyVersion("1.2.3")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			start := time.Now()
			listenerPort := 1975

			dirs := newTempDirectories(t)
			middleware := newEnvoyRunMiddleware(dirs, "test-run", start, listenerPort, &stdout, &stderr)
			require.NotNil(t, middleware)

			err := middleware(func(ctx context.Context, args []string, options ...api.RunOption) error {
				require.Equal(t, t.Context(), ctx)
				require.Equal(t, []string{"test"}, args)

				// 8 = EnvoyOut, EnvoyErr, ConfigHome, DataHome, StateHome, RuntimeDir, RunID, StartupHook
				require.Len(t, options, 8+len(tt.inputOptions))
				return nil
			})(t.Context(), []string{"test"}, tt.inputOptions...)
			require.NoError(t, err)
		})
	}
}

func readFileFromProjectRoot(t *testing.T, file string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", file))
	require.NoError(t, err)
	return string(b)
}

// testRunOpts creates runOpts for testing.
// This ensures test isolation by using t.TempDir() for all XDG directories.
func testRunOpts(t *testing.T, extProcLauncher func(context.Context, []string, io.Writer) error) *runOpts {
	t.Helper()
	dirs := newTempDirectories(t)
	opts, err := newRunOpts(dirs, "test-run", "", extProcLauncher)
	require.NoError(t, err)
	return opts
}
