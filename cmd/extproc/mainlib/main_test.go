// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mainlib

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_parseAndValidateFlags(t *testing.T) {
	t.Run("ok extProcFlags", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			args       []string
			configPath string
			addr       string
			rootPrefix string
			logLevel   slog.Level
		}{
			{
				name:       "minimal extProcFlags",
				args:       []string{"-configPath", "/path/to/config.yaml"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				rootPrefix: "/",
				logLevel:   slog.LevelInfo,
			},
			{
				name:       "custom addr",
				args:       []string{"-configPath", "/path/to/config.yaml", "-extProcAddr", "unix:///tmp/ext_proc.sock"},
				configPath: "/path/to/config.yaml",
				addr:       "unix:///tmp/ext_proc.sock",
				rootPrefix: "/",
				logLevel:   slog.LevelInfo,
			},
			{
				name:       "log level debug",
				args:       []string{"-configPath", "/path/to/config.yaml", "-logLevel", "debug"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				rootPrefix: "/",
				logLevel:   slog.LevelDebug,
			},
			{
				name:       "log level warn",
				args:       []string{"-configPath", "/path/to/config.yaml", "-logLevel", "warn"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				rootPrefix: "/",
				logLevel:   slog.LevelWarn,
			},
			{
				name:       "log level error",
				args:       []string{"-configPath", "/path/to/config.yaml", "-logLevel", "error"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				rootPrefix: "/",
				logLevel:   slog.LevelError,
			},
			{
				name: "all extProcFlags",
				args: []string{
					"-configPath", "/path/to/config.yaml",
					"-extProcAddr", "unix:///tmp/ext_proc.sock",
					"-logLevel", "debug",
					"-rootPrefix", "/foo/bar/",
				},
				configPath: "/path/to/config.yaml",
				addr:       "unix:///tmp/ext_proc.sock",
				rootPrefix: "/foo/bar/",
				logLevel:   slog.LevelDebug,
			},
			{
				name: "with header mapping",
				args: []string{
					"-configPath", "/path/to/config.yaml",
					"-metricsRequestHeaderLabels", "x-team-id:team_id,x-user-id:user_id",
				},
				configPath: "/path/to/config.yaml",
				rootPrefix: "/",
				addr:       ":1063",
				logLevel:   slog.LevelInfo,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				flags, err := parseAndValidateFlags(tc.args)
				require.NoError(t, err)
				require.Equal(t, tc.configPath, flags.configPath)
				require.Equal(t, tc.addr, flags.extProcAddr)
				require.Equal(t, tc.logLevel, flags.logLevel)
				require.Equal(t, tc.rootPrefix, flags.rootPrefix)
			})
		}
	})

	t.Run("invalid extProcFlags", func(t *testing.T) {
		_, err := parseAndValidateFlags([]string{"-logLevel", "invalid"})
		require.EqualError(t, err, `configPath must be provided
failed to unmarshal log level: slog: level string "invalid": unknown name`)
	})
}

func TestListenAddress(t *testing.T) {
	unixPath := t.TempDir() + "/extproc.sock"
	// Create a stale file to ensure that removing the file works correctly.
	require.NoError(t, os.WriteFile(unixPath, []byte("stale socket"), 0o600))

	lis, err := listen(t.Context(), t.Name(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	tests := []struct {
		addr        string
		wantNetwork string
		wantAddress string
	}{
		{lis.Addr().String(), "tcp", lis.Addr().String()},
		{"unix://" + unixPath, "unix", unixPath},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			network, address := listenAddress(tt.addr)
			require.Equal(t, tt.wantNetwork, network)
			require.Equal(t, tt.wantAddress, address)
		})
	}
	_, err = os.Stat(unixPath)
	require.ErrorIs(t, err, os.ErrNotExist, "expected the stale socket file to be removed")
}

// TestExtProcStartupMessage ensures other programs can rely on the startup message to STDERR.
func TestExtProcStartupMessage(t *testing.T) {
	// Create a temporary config file.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`metadataNamespace: test_ns
modelNameHeaderKey: x-model-name
backends:
- name: openai
  schema:
    name: OpenAI
    version: v1
`), 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Create a pipe for stderr.
	stderrR, stderrW := io.Pipe()

	// Start a goroutine to scan stderr until it reaches "AI Gateway External Processor is ready" written by envoy.
	go func() {
		scanner := bufio.NewScanner(stderrR)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "AI Gateway External Processor is ready") {
				cancel() // interrupts extproc.
				return
			}
		}
	}()

	// Run ExtProc in a goroutine on ephemeral ports.
	errCh := make(chan error, 1)
	go func() {
		args := []string{
			"-configPath", configPath,
			"-extProcAddr", ":0",
			"-adminPort", "0",
		}
		errCh <- Main(ctx, args, stderrW)
	}()

	// block until the context is canceled or an error occurs.
	err := <-errCh
	require.NoError(t, err)
}
