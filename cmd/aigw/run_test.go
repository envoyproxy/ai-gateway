// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
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
)

// getOllamaChatModel reads CHAT_MODEL from .env.ollama relative to the source directory.
// Returns empty string if not found or file missing.
func getOllamaChatModel(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	envPath := filepath.Join(filepath.Dir(filename), "..", "..", ".env.ollama")
	content, err := os.ReadFile(envPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "CHAT_MODEL=") {
			return strings.TrimPrefix(line, "CHAT_MODEL=")
		}
	}
	return ""
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

func TestRun(t *testing.T) {
	ollamaModel := getOllamaChatModel(t)
	if ollamaModel == "" || !checkIfOllamaReady(t, ollamaModel) {
		t.Skipf("Ollama not ready or model %q missing. Run 'ollama pull %s' if needed.", ollamaModel, ollamaModel)
	}

	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "unused")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		opts := runOpts{extProcLauncher: mainlib.Main}
		require.NoError(t, run(ctx, cmdRun{Debug: true}, opts, os.Stdout, os.Stderr))
		close(done)
	}()
	defer func() { cancel(); <-done }()

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

	require.Eventually(t, func() bool {
		req, err := http.NewRequest(http.MethodGet, "http://localhost:1064/metrics", nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Minute, time.Second)
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
	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("OPENAI_API_KEY", "unused")

	runCtx := &runCmdContext{
		stderrLogger:             slog.New(slog.NewTextHandler(os.Stderr, nil)),
		envoyGatewayResourcesOut: &bytes.Buffer{},
		tmpdir:                   t.TempDir(),
		extProcLauncher:          mainlib.Main,
		udsPath:                  filepath.Join("/tmp", "run.sock"), // Short UDS path for UNIX compatibility.
	}
	content, err := readConfig("")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	_, done, _, err := runCtx.writeEnvoyResourcesAndRunExtProc(ctx, content)
	require.NoError(t, err)
	time.Sleep(time.Second)
	cancel()
	require.NoError(t, <-done)
}

func Test_mustStartExtProc(t *testing.T) {
	mockErr := errors.New("mock extproc error")
	runCtx := &runCmdContext{
		tmpdir:          t.TempDir(),
		extProcLauncher: func(context.Context, []string, io.Writer) error { return mockErr },
		stderrLogger:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	done := runCtx.mustStartExtProc(t.Context(), filterapi.MustLoadDefaultConfig())
	require.ErrorIs(t, <-done, mockErr)
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
	defer s.Close()
	u, err := url.Parse(s.URL)
	require.NoError(t, err)

	l := slog.New(slog.DiscardHandler)

	t.Run("empty address", func(t *testing.T) {
		callCount = 0
		pollEnvoyReadiness(t.Context(), l, "", 50*time.Millisecond)
		require.Zero(t, callCount)
	})

	t.Run("ready", func(t *testing.T) {
		callCount = 0
		pollEnvoyReadiness(t.Context(), l, u.Host, 50*time.Millisecond)
		require.Equal(t, successAt, callCount)
	})

	t.Run("abort on context done", func(t *testing.T) {
		callCount = 0
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		pollEnvoyReadiness(ctx, l, u.Host, 50*time.Millisecond)
		require.Less(t, callCount, successAt)
	})
}
