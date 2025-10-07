// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package aigw

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExtractAdminAddressPath(t *testing.T) {
	tmpDir := t.TempDir()
	validFile := filepath.Join(tmpDir, "admin-address.txt")
	require.NoError(t, os.WriteFile(validFile, []byte("127.0.0.1:9901"), 0o600))

	tests := []struct {
		name          string
		cmdline       []string
		expected      string
		expectedError string
	}{
		{
			name:     "valid flag with file",
			cmdline:  []string{"envoy", "--admin-address-path", validFile},
			expected: validFile,
		},
		{
			name:     "flag at end with file",
			cmdline:  []string{"--config", "/etc/envoy.yaml", "--admin-address-path", validFile},
			expected: validFile,
		},
		{
			name:          "flag not present",
			cmdline:       []string{"envoy", "--config", "/etc/envoy.yaml"},
			expectedError: "--admin-address-path not found in command line",
		},
		{
			name:          "flag present but no value",
			cmdline:       []string{"envoy", "--admin-address-path"},
			expectedError: "--admin-address-path not found in command line",
		},
		{
			name:          "empty cmdline",
			cmdline:       []string{},
			expectedError: "--admin-address-path not found in command line",
		},
		{
			name:          "path is a directory not a file",
			cmdline:       []string{"envoy", "--admin-address-path", tmpDir},
			expectedError: fmt.Sprintf("envoy admin address path %q is not a file", tmpDir),
		},
		{
			name:          "path does not exist",
			cmdline:       []string{"envoy", "--admin-address-path", "/nonexistent/path"},
			expectedError: "envoy admin address path \"/nonexistent/path\" is not a file",
		},
		{
			name:     "sh -c wrapped command",
			cmdline:  []string{"sh", "-c", "sleep 30 && echo -- --admin-address-path " + validFile},
			expected: validFile,
		},
		{
			name:     "sh -c with multiple spaces",
			cmdline:  []string{"sh", "-c", "envoy  --admin-address-path  " + validFile + "  --other-flag"},
			expected: validFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := extractAdminAddressPath(tt.cmdline)

			if tt.expectedError != "" {
				require.EqualError(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, actual)
			}
		})
	}
}

func TestPollEnvoyAdminAddressPathFromArgs(t *testing.T) {
	t.Run("success - finds admin address path from child process", func(t *testing.T) {
		adminFile := filepath.Join(t.TempDir(), "admin-address.txt")
		require.NoError(t, os.WriteFile(adminFile, []byte("127.0.0.1:9901"), 0o600))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		cmdStr := fmt.Sprintf("sleep 30 && echo -- --admin-address-path %s", adminFile)
		cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
		require.NoError(t, cmd.Start())
		defer func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}()

		time.Sleep(100 * time.Millisecond)

		pid := os.Getpid()
		actual, err := pollEnvoyAdminAddressPathFromArgs(t.Context(), int32(pid)) // #nosec G115 -- PID fits in int32
		require.NoError(t, err)
		require.Equal(t, adminFile, actual)
	})

	t.Run("failure - no child processes", func(t *testing.T) {
		_, err := pollEnvoyAdminAddressPathFromArgs(t.Context(), 1)
		require.Error(t, err)
	})
}

func TestPollPortFromEnvoyAddressPath(t *testing.T) {
	t.Run("file appears after delay", func(t *testing.T) {
		adminAddrFile := filepath.Join(t.TempDir(), "admin-address.txt")

		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = os.WriteFile(adminAddrFile, []byte("127.0.0.1:9901\n"), 0o600)
		}()

		port, err := pollPortFromEnvoyAddressPath(t.Context(), adminAddrFile)
		require.NoError(t, err)
		require.Equal(t, 9901, port)
	})

	t.Run("timeout when file never appears", func(t *testing.T) {
		adminAddrFile := filepath.Join(t.TempDir(), "admin-address.txt")

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		_, err := pollPortFromEnvoyAddressPath(ctx, adminAddrFile)
		require.Error(t, err)
	})

	t.Run("extracts port from address with any hostname", func(t *testing.T) {
		adminAddrFile := filepath.Join(t.TempDir(), "admin-address.txt")

		require.NoError(t, os.WriteFile(adminAddrFile, []byte("localhost:9901"), 0o600))

		port, err := pollPortFromEnvoyAddressPath(t.Context(), adminAddrFile)
		require.NoError(t, err)
		require.Equal(t, 9901, port)
	})

	t.Run("invalid address format", func(t *testing.T) {
		adminAddrFile := filepath.Join(t.TempDir(), "admin-address.txt")

		require.NoError(t, os.WriteFile(adminAddrFile, []byte("invalid-address"), 0o600))

		_, err := pollPortFromEnvoyAddressPath(t.Context(), adminAddrFile)
		require.Error(t, err)
	})
}

func TestEnvoyAdminAPIClient_IsReady(t *testing.T) {
	t.Run("returns nil when ready", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/ready", r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("live"))
		}))
		defer server.Close()

		u, err := url.Parse(server.URL)
		require.NoError(t, err)
		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)

		client := &envoyAdminAPIClient{adminPort: port}
		err = client.IsReady(t.Context())
		require.NoError(t, err)
	})

	t.Run("returns error when not ready", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
		}))
		defer server.Close()

		u, err := url.Parse(server.URL)
		require.NoError(t, err)
		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)

		client := &envoyAdminAPIClient{adminPort: port}
		err = client.IsReady(t.Context())
		require.Error(t, err)
	})

	t.Run("returns error when body is not live", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("something else"))
		}))
		defer server.Close()

		u, err := url.Parse(server.URL)
		require.NoError(t, err)
		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)

		client := &envoyAdminAPIClient{adminPort: port}
		err = client.IsReady(t.Context())
		require.Error(t, err)
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("live"))
		}))
		defer server.Close()

		u, err := url.Parse(server.URL)
		require.NoError(t, err)
		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)

		client := &envoyAdminAPIClient{adminPort: port}

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()

		err = client.IsReady(ctx)
		require.Error(t, err)
	})
}

func TestEnvoyListenerPortClient_IsReady(t *testing.T) {
	t.Run("returns nil when port is open", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer listener.Close()

		port := listener.Addr().(*net.TCPAddr).Port
		client := &envoyListenerPortClient{port: port}

		err = client.IsReady(t.Context())
		require.NoError(t, err)
	})

	t.Run("returns error when port is closed", func(t *testing.T) {
		client := &envoyListenerPortClient{port: 54321}

		err := client.IsReady(t.Context())
		require.Error(t, err)
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		client := &envoyListenerPortClient{port: 54321}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := client.IsReady(ctx)
		require.Error(t, err)
	})
}

func TestNewEnvoyAdminClient(t *testing.T) {
	t.Run("returns envoyAdminAPIClient when envoyAdminPort > 0", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		client := NewEnvoyAdminClient(t.Context(), logger, os.Getpid(), 9901, 1975)

		adminClient, ok := client.(*envoyAdminAPIClient)
		require.True(t, ok)
		require.Equal(t, 9901, adminClient.adminPort)

		logOutput := buf.String()
		require.Contains(t, logOutput, "Using configured Envoy admin")
		require.Contains(t, logOutput, "9901")
	})

	t.Run("returns envoyListenerPortClient when discovery fails", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		client := NewEnvoyAdminClient(t.Context(), logger, 1, 0, 1975)

		listenerClient, ok := client.(*envoyListenerPortClient)
		require.True(t, ok)
		require.Equal(t, 1975, listenerClient.port)

		logOutput := buf.String()
		require.Contains(t, logOutput, "Falling back to Envoy listener")
	})
}
