// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mainlib

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func Test_parseAndValidateFlags(t *testing.T) {
	t.Run("ok extProcFlags", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			args       []string
			configPath string
			addr       string
			logLevel   slog.Level
		}{
			{
				name:       "minimal extProcFlags",
				args:       []string{"-configPath", "/path/to/config.yaml"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelInfo,
			},
			{
				name:       "custom addr",
				args:       []string{"-configPath", "/path/to/config.yaml", "-extProcAddr", "unix:///tmp/ext_proc.sock"},
				configPath: "/path/to/config.yaml",
				addr:       "unix:///tmp/ext_proc.sock",
				logLevel:   slog.LevelInfo,
			},
			{
				name:       "log level debug",
				args:       []string{"-configPath", "/path/to/config.yaml", "-logLevel", "debug"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelDebug,
			},
			{
				name:       "log level warn",
				args:       []string{"-configPath", "/path/to/config.yaml", "-logLevel", "warn"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelWarn,
			},
			{
				name:       "log level error",
				args:       []string{"-configPath", "/path/to/config.yaml", "-logLevel", "error"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelError,
			},
			{
				name: "all extProcFlags",
				args: []string{
					"-configPath", "/path/to/config.yaml",
					"-extProcAddr", "unix:///tmp/ext_proc.sock",
					"-logLevel", "debug",
				},
				configPath: "/path/to/config.yaml",
				addr:       "unix:///tmp/ext_proc.sock",
				logLevel:   slog.LevelDebug,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				flags, err := parseAndValidateFlags(tc.args)
				require.NoError(t, err)
				assert.Equal(t, tc.configPath, flags.configPath)
				assert.Equal(t, tc.addr, flags.extProcAddr)
				assert.Equal(t, tc.logLevel, flags.logLevel)
			})
		}
	})

	t.Run("invalid extProcFlags", func(t *testing.T) {
		_, err := parseAndValidateFlags([]string{"-logLevel", "invalid"})
		assert.EqualError(t, err, `configPath must be provided
failed to unmarshal log level: slog: level string "invalid": unknown name`)
	})
}

func TestListenAddress(t *testing.T) {
	unixPath := t.TempDir() + "/extproc.sock"
	// Create a stale file to ensure that removing the file works correctly.
	require.NoError(t, os.WriteFile(unixPath, []byte("stale socket"), 0o600))

	tests := []struct {
		addr        string
		wantNetwork string
		wantAddress string
	}{
		{":8080", "tcp", ":8080"},
		{"unix://" + unixPath, "unix", unixPath},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			network, address := listenAddress(tt.addr)
			assert.Equal(t, tt.wantNetwork, network)
			assert.Equal(t, tt.wantAddress, address)
		})
	}
	_, err := os.Stat(unixPath)
	require.ErrorIs(t, err, os.ErrNotExist, "expected the stale socket file to be removed")
}

func TestStartMetricsServer(t *testing.T) {
	s, m := startMetricsServer("127.0.0.1:", slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})))
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })

	require.NotNil(t, s)
	require.NotNil(t, m)

	require.HTTPStatusCode(t, s.Handler.ServeHTTP, http.MethodGet, "/", nil, http.StatusNotFound)

	require.HTTPSuccess(t, s.Handler.ServeHTTP, http.MethodGet, "/health", nil)
	require.HTTPBodyContains(t, s.Handler.ServeHTTP, http.MethodGet, "/health", nil, "OK")

	require.HTTPSuccess(t, s.Handler.ServeHTTP, http.MethodGet, "/metrics", nil)
	require.HTTPBodyContains(t, s.Handler.ServeHTTP, http.MethodGet, "/metrics", nil, "target_info{")
}

func TestStartHealthCheckServer(t *testing.T) {
	for _, tc := range []string{"unix", "tcp"} {
		t.Run(tc, func(t *testing.T) {
			var grpcLis net.Listener
			var err error
			if tc == "unix" {
				_ = os.Remove("/tmp/ext_proc.sock")
				grpcLis, err = net.Listen("unix", "/tmp/ext_proc.sock")
			} else {
				grpcLis, err = net.Listen("tcp", "localhost:1063")
			}
			require.NoError(t, err)

			hs := health.NewServer()
			hs.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
			grpcSrv := grpc.NewServer()
			grpc_health_v1.RegisterHealthServer(grpcSrv, hs)
			go func() {
				_ = grpcSrv.Serve(grpcLis)
			}()
			defer grpcSrv.Stop()
			time.Sleep(time.Millisecond * 100)

			httpSrv := startHealthCheckServer(
				"", // addr unused when invoking Handler directly.
				slog.Default(),
				grpcLis,
			)

			req := httptest.NewRequest("GET", "/", nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			httpSrv.Handler.ServeHTTP(rr, req)
			res := rr.Result()
			defer res.Body.Close()

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			fmt.Println(string(body))
			require.Equal(t, http.StatusOK, res.StatusCode)
		})
	}
}
