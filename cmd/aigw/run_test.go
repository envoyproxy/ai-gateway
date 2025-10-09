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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/internal/aigw"
	"github.com/envoyproxy/ai-gateway/internal/autoconfig"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

// startupRegexp ensures the status message is written to stderr as we know we are healthy!
var startupRegexp = `Envoy AI Gateway listening on http://localhost:1975 \(admin http://localhost:\d+\) after .* duration\n`

func TestRun(t *testing.T) {
	ollamaModel, err := internaltesting.GetOllamaModel(internaltesting.ChatModel)
	if err == nil {
		err = internaltesting.CheckIfOllamaReady(ollamaModel)
	}
	if err != nil {
		t.Skip(err)
	}

	ports := internaltesting.RequireRandomPorts(t, 1)
	// TODO: parameterize the main listen port 1975
	adminPort := ports[0]

	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "unused")

	buffers := internaltesting.DumpLogsOnFail(t, "aigw Stdout", "aigw Stderr")
	stdout, stderr := buffers[0], buffers[1]
	ctx, cancel := context.WithCancel(t.Context())
	defer cleanupRun(t, cancel)

	go func() {
		opts := runOpts{extProcLauncher: mainlib.Main}
		require.NoError(t, run(ctx, cmdRun{Debug: true, AdminPort: adminPort}, opts, stdout, stderr))
	}()

	client := openai.NewClient(option.WithBaseURL("http://localhost:1975/v1/"))
	chatReq := openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Say this is a test"),
		},
		Model: ollamaModel,
	}

	t.Run("chat completion", func(t *testing.T) {
		internaltesting.RequireEventuallyNoError(t, func() error {
			chatCompletion, err := client.Chat.Completions.New(ctx, chatReq)
			if err != nil {
				return fmt.Errorf("chat completion failed: %w", err)
			}
			for _, choice := range chatCompletion.Choices {
				if choice.Message.Content != "" {
					return nil
				}
			}
			return fmt.Errorf("no content in response")
		}, 1*time.Minute, 2*time.Second,
			"chat completion never succeeded")
	})

	// By now, we're listening
	require.Regexp(t, startupRegexp, stderr.String())

	t.Run("access metrics", func(t *testing.T) {
		internaltesting.RequireEventuallyNoError(t, func() error {
			req, err := http.NewRequest(http.MethodGet,
				fmt.Sprintf("http://localhost:%d/metrics", adminPort), nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("status %d", resp.StatusCode)
			}
			return nil
		}, 1*time.Minute, time.Second,
			"metrics endpoint never became available")
	})
}

func cleanupRun(t testing.TB, cancel context.CancelFunc) {
	cancel()
	if err := internaltesting.AwaitPortClosed(1975, 10*time.Second); err != nil {
		t.Logf("Failed to close port 1975: %v", err)
	}
	// Delete the hard-coded path to certs defined in Envoy AI Gateway
	if err := os.RemoveAll("/tmp/envoy-gateway/certs"); err != nil {
		t.Logf("Failed to delete envoy gateway certs: %v", err)
	}
}

// TestRunMCP runs the AIGW with MCP configured and verifies calling a tool.
// It uses the same MCP config as docker-compose.yaml to ensure consistency.
func TestRunMCP(t *testing.T) {
	ports := internaltesting.RequireRandomPorts(t, 1)
	// TODO: parameterize the main listen port 1975
	adminPort := ports[0]

	mcpServers := &autoconfig.MCPServers{
		McpServers: map[string]autoconfig.MCPServer{
			"kiwi": {
				Type: "http",
				URL:  "https://mcp.kiwi.com",
			},
		},
	}

	buffers := internaltesting.DumpLogsOnFail(t, "aigw Stdout", "aigw Stderr")
	stdout, stderr := buffers[0], buffers[1]
	ctx, cancel := context.WithCancel(t.Context())
	defer cleanupRun(t, cancel)

	go func() {
		opts := runOpts{extProcLauncher: mainlib.Main}
		require.NoError(t, run(ctx, cmdRun{Debug: true, AdminPort: adminPort, mcpConfig: mcpServers}, opts, stdout, stderr))
	}()

	url := fmt.Sprintf("http://localhost:%d/mcp", 1975)
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"},
		&mcp.ClientOptions{})
	callTool := &mcp.CallToolParams{
		Name: "kiwi__search-flight",
		Arguments: map[string]any{
			"flyFrom":       "NYC",
			"flyTo":         "LAX",
			"departureDate": "15/12/2025",
		},
	}

	t.Run("call tool", func(t *testing.T) {
		internaltesting.RequireEventuallyNoError(t, func() error {
			session, err := mcpClient.Connect(t.Context(),
				&mcp.StreamableClientTransport{Endpoint: url}, nil)
			if err != nil {
				return fmt.Errorf("connect failed: %w", err)
			}
			defer session.Close()

			resp, err := session.CallTool(t.Context(), callTool)
			if err != nil {
				return fmt.Errorf("call tool failed: %w", err)
			}
			if resp.IsError {
				return fmt.Errorf("tool returned error response")
			}
			return nil
		}, 2*time.Minute, 2*time.Second,
			"MCP tool call never succeeded")
	})

	// By now, we're listening
	require.Regexp(t, startupRegexp, stderr.String())
}

func TestRunExtprocStartFailure(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "unused")

	ctx := t.Context()
	errChan := make(chan error)
	mockErr := errors.New("mock extproc error")
	go func() {
		errChan <- run(ctx, cmdRun{}, runOpts{
			extProcLauncher: func(context.Context, []string, io.Writer) error { return mockErr },
		}, os.Stdout, io.Discard)
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
	_, done, _, _, err := runCtx.writeEnvoyResourcesAndRunExtProc(ctx, config)
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

	_, _, _, _, err := runCtx.writeEnvoyResourcesAndRunExtProc(t.Context(), gatewayNoListenersConfig)
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

func TestTryFindEnvoyAdminPort(t *testing.T) {
	gwWithProxy := func(name string) *gwapiv1.Gateway {
		return &gwapiv1.Gateway{
			Spec: gwapiv1.GatewaySpec{
				Infrastructure: &gwapiv1.GatewayInfrastructure{
					ParametersRef: &gwapiv1.LocalParametersReference{
						Kind: "EnvoyProxy",
						Name: name,
					},
				},
			},
		}
	}

	proxyWithAdminAddr := func(name, host string, port int) *egv1a1.EnvoyProxy {
		return &egv1a1.EnvoyProxy{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: egv1a1.EnvoyProxySpec{
				Bootstrap: &egv1a1.ProxyBootstrap{
					Value: ptr.To(fmt.Sprintf(`
admin:
  address:
    socket_address:
      address: %s
      port_value: %d`, host, port)),
				},
			},
		}
	}

	tests := []struct {
		name     string
		gw       *gwapiv1.Gateway
		proxies  []*egv1a1.EnvoyProxy
		expected int
	}{
		{
			name:     "gateway with no envoy proxy",
			gw:       &gwapiv1.Gateway{},
			expected: 0,
		},
		{
			name:     "gateway with non matching envoy proxy",
			gw:       gwWithProxy("non-matching-proxy"),
			proxies:  []*egv1a1.EnvoyProxy{proxyWithAdminAddr("proxy", "localhost", 8080)},
			expected: 0,
		},
		{
			name: "gateway with custom proxy no bootstrap",
			gw:   gwWithProxy("proxy"),
			proxies: []*egv1a1.EnvoyProxy{
				{ObjectMeta: metav1.ObjectMeta{Name: "proxy"}},
			},
			expected: 0,
		},
		{
			name: "gateway with custom bootstrap",
			gw:   gwWithProxy("proxy"),
			proxies: []*egv1a1.EnvoyProxy{
				proxyWithAdminAddr("no-match", "localhost", 8081),
				proxyWithAdminAddr("proxy", "127.0.0.1", 9901),
			},
			expected: 9901,
		},
	}

	runCtx := &runCmdContext{
		stderrLogger: slog.New(slog.DiscardHandler),
		tmpdir:       t.TempDir(),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := runCtx.tryFindEnvoyAdminPort(tt.gw, tt.proxies)
			require.Equal(t, tt.expected, port)
		})
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

func TestPollEnvoyReady(t *testing.T) {
	successAt := 5
	var callCount int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < successAt {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("live"))
		}
	}))
	t.Cleanup(s.Close)
	u, err := url.Parse(s.URL)
	require.NoError(t, err)

	adminPort, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	l := slog.New(slog.DiscardHandler)

	t.Run("ready", func(t *testing.T) {
		t.Cleanup(func() { callCount = 0 })
		envoyAdmin, err := aigw.NewEnvoyAdminClient(t.Context(), os.Getpid(), adminPort)
		require.NoError(t, err)
		pollEnvoyReady(t.Context(), l, envoyAdmin, 50*time.Millisecond)
		require.Equal(t, successAt, callCount)
	})

	t.Run("abort on context done", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		t.Cleanup(cancel)
		t.Cleanup(func() { callCount = 0 })
		envoyAdmin, err := aigw.NewEnvoyAdminClient(ctx, os.Getpid(), adminPort)
		require.NoError(t, err)
		pollEnvoyReady(ctx, l, envoyAdmin, 50*time.Millisecond)
		require.Less(t, callCount, successAt)
	})
}

func readFileFromProjectRoot(t *testing.T, file string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", file))
	require.NoError(t, err)
	return string(b)
}
