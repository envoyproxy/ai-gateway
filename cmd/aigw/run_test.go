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
	"strings"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestRun(t *testing.T) {
	ollamaModel := getOllamaChatModel(t)
	if ollamaModel == "" || !checkIfOllamaReady(t, ollamaModel) {
		t.Skipf("Ollama not ready or model %q missing. Run 'ollama pull %s' if needed.", ollamaModel, ollamaModel)
	}

	ports := internaltesting.RequireRandomPorts(t, 1)
	// TODO: parameterize the main listen port 1975
	adminPort := ports[0]

	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "unused")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		opts := runOpts{extProcLauncher: mainlib.Main}
		require.NoError(t, run(ctx, cmdRun{Debug: true, AdminPort: adminPort}, opts, os.Stdout, os.Stderr))
		close(done)
	}()
	defer func() { cancel(); <-done }()

	t.Run("chat completion", func(t *testing.T) {
		client := openai.NewClient(option.WithBaseURL("http://localhost:1975/v1/"))
		require.Eventually(t, func() bool {
			chatCompletion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("Say this is a test"),
				},
				Model: ollamaModel,
			})
			if err != nil {
				return false
			}
			for _, choice := range chatCompletion.Choices {
				if choice.Message.Content != "" {
					return true
				}
			}
			return false
		}, 30*time.Second, 2*time.Second)
	})

	t.Run("access metrics", func(t *testing.T) {
		require.Eventually(t, func() bool {
			req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", adminPort), nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}, 2*time.Minute, time.Second)
	})
}

func TestRunExtprocStartFailure(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "unused")

	ctx := t.Context()
	errChan := make(chan error)
	mockErr := errors.New("mock extproc error")
	go func() {
		errChan <- run(ctx, cmdRun{Debug: true}, runOpts{
			extProcLauncher: func(context.Context, []string, io.Writer) error { return mockErr },
		}, os.Stdout, os.Stderr)
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
		stderrLogger:             slog.New(slog.NewTextHandler(os.Stderr, nil)),
		envoyGatewayResourcesOut: &bytes.Buffer{},
		tmpdir:                   t.TempDir(),
		adminPort:                adminPort,
		extProcLauncher:          mainlib.Main,
		// UNIX doesn't like a long UDS path, so we use a short one.
		// https://unix.stackexchange.com/questions/367008/why-is-socket-path-length-limited-to-a-hundred-chars
		udsPath: filepath.Join("/tmp", "run.sock"),
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
		stderrLogger:             slog.New(slog.DiscardHandler),
		envoyGatewayResourcesOut: &bytes.Buffer{},
		tmpdir:                   t.TempDir(),
		adminPort:                adminPort,
		udsPath:                  filepath.Join("/tmp", "run-test.sock"),
	}

	_, _, _, _, err := runCtx.writeEnvoyResourcesAndRunExtProc(t.Context(), gatewayNoListenersConfig)
	require.EqualError(t, err, "gateway aigw-run has no listeners configured")
}

func Test_mustStartExtProc(t *testing.T) {
	mockErr := errors.New("mock extproc error")
	runCtx := &runCmdContext{
		tmpdir:          t.TempDir(),
		adminPort:       1064,
		extProcLauncher: func(context.Context, []string, io.Writer) error { return mockErr },
		stderrLogger:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	done := runCtx.mustStartExtProc(t.Context(), filterapi.MustLoadDefaultConfig())
	require.ErrorIs(t, <-done, mockErr)
}

func Test_mustStartExtProc_withHeaderAttributes(t *testing.T) {
	t.Setenv("OTEL_AIGW_METRICS_REQUEST_HEADER_ATTRIBUTES", "x-team-id:team.id,x-user-id:user.id")
	t.Setenv("OTEL_AIGW_SPAN_REQUEST_HEADER_ATTRIBUTES", "x-session-id:session.id,x-user-id:user.id")

	var capturedArgs []string
	runCtx := &runCmdContext{
		tmpdir:    t.TempDir(),
		adminPort: 1064,
		extProcLauncher: func(_ context.Context, args []string, _ io.Writer) error {
			capturedArgs = args
			return errors.New("mock error") // Return error to stop execution
		},
		stderrLogger: slog.New(slog.DiscardHandler),
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

func TestTryFindEnvoyAdminAddress(t *testing.T) {
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
		name    string
		gw      *gwapiv1.Gateway
		proxies []*egv1a1.EnvoyProxy
		want    string
	}{
		{
			name: "gateway with no envoy proxy",
			gw:   &gwapiv1.Gateway{},
			want: "",
		},
		{
			name:    "gateway with non matching envoy proxy",
			gw:      gwWithProxy("non-matching-proxy"),
			proxies: []*egv1a1.EnvoyProxy{proxyWithAdminAddr("proxy", "localhost", 8080)},
			want:    "",
		},
		{
			name: "gateway with custom proxy no bootstrap",
			gw:   gwWithProxy("proxy"),
			proxies: []*egv1a1.EnvoyProxy{
				{ObjectMeta: metav1.ObjectMeta{Name: "proxy"}},
			},
			want: "",
		},
		{
			name: "gateway with custom bootstrap",
			gw:   gwWithProxy("proxy"),
			proxies: []*egv1a1.EnvoyProxy{
				proxyWithAdminAddr("no-match", "localhost", 8081),
				proxyWithAdminAddr("proxy", "127.0.0.1", 9901),
			},
			want: "127.0.0.1:9901",
		},
	}

	runCtx := &runCmdContext{
		tmpdir:       t.TempDir(),
		stderrLogger: slog.New(slog.DiscardHandler),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := runCtx.tryFindEnvoyAdminAddress(tt.gw, tt.proxies)
			require.Equal(t, tt.want, addr)
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
		tmpdir:       t.TempDir(),
		stderrLogger: slog.New(slog.DiscardHandler),
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
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount < successAt {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
	}))
	t.Cleanup(s.Close)
	u, err := url.Parse(s.URL)
	require.NoError(t, err)

	l := slog.New(slog.DiscardHandler)

	t.Run("ready", func(t *testing.T) {
		t.Cleanup(func() { callCount = 0 })
		pollEnvoyAdminReady(t.Context(), l, u.Host, 50*time.Millisecond)
		require.Equal(t, successAt, callCount)
	})

	t.Run("abort on context done", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		t.Cleanup(cancel)
		t.Cleanup(func() { callCount = 0 })
		pollEnvoyAdminReady(ctx, l, u.Host, 50*time.Millisecond)
		require.Less(t, callCount, successAt)
	})
}

// getOllamaChatModel reads CHAT_MODEL from .env.ollama relative to the source directory.
// Returns empty string if not found or file missing.
func getOllamaChatModel(t *testing.T) string {
	t.Helper()
	envs := readFileFromProjectRoot(t, ".env.ollama")
	for _, line := range strings.Split(envs, "\n") {
		if strings.HasPrefix(line, "CHAT_MODEL=") {
			return strings.TrimPrefix(line, "CHAT_MODEL=")
		}
	}
	return ""
}

func readFileFromProjectRoot(t *testing.T, file string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", file))
	require.NoError(t, err)
	return string(b)
}

// checkIfOllamaReady verifies if Ollama server is ready and the model is available.
func checkIfOllamaReady(t *testing.T, modelName string) bool {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://localhost:11434/api/tags", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return strings.Contains(string(body), modelName)
}
