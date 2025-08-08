// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mainlib

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/tests/testotel"
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
			{
				name: "with header mapping",
				args: []string{
					"-configPath", "/path/to/config.yaml",
					"-metricsRequestHeaderLabels", "x-team-id:team_id,x-user-id:user_id",
				},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelInfo,
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
			assert.Equal(t, tt.wantNetwork, network)
			assert.Equal(t, tt.wantAddress, address)
		})
	}
	_, err = os.Stat(unixPath)
	require.ErrorIs(t, err, os.ErrNotExist, "expected the stale socket file to be removed")
}

func TestStartMetricsServer(t *testing.T) {
	lis, err := listen(t.Context(), t.Name(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	s, m := startMetricsServer(lis, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})))
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })

	require.NotNil(t, s)
	require.NotNil(t, m)
	ccm := metrics.NewChatCompletion(m, nil)
	ccm.StartRequest(nil)
	ccm.SetModel("test-model")
	ccm.SetBackend(&filterapi.Backend{Name: "test-backend"})
	ccm.RecordTokenUsage(t.Context(), 10, 5, 15, nil)
	ccm.RecordRequestCompletion(t.Context(), true, nil)
	ccm.RecordTokenLatency(t.Context(), 10, nil)

	require.HTTPStatusCode(t, s.Handler.ServeHTTP, http.MethodGet, "/", nil, http.StatusNotFound)

	require.HTTPSuccess(t, s.Handler.ServeHTTP, http.MethodGet, "/health", nil)
	require.HTTPBodyContains(t, s.Handler.ServeHTTP, http.MethodGet, "/health", nil, "OK")

	require.HTTPSuccess(t, s.Handler.ServeHTTP, http.MethodGet, "/metrics", nil)
	// Ensure that the metrics endpoint returns the expected metrics.
	for _, metric := range []string{
		"gen_ai_client_token_usage_token_bucket",
		"gen_ai_server_request_duration_seconds_bucket",
		"gen_ai_server_request_duration_seconds_count",
		"gen_ai_server_request_duration_seconds_sum",
		"gen_ai_client_token_usage_token_bucket",
		"gen_ai_client_token_usage_token_count",
		"gen_ai_client_token_usage_token_sum",
	} {
		require.HTTPBodyContains(t, s.Handler.ServeHTTP, http.MethodGet, "/metrics", nil, metric)
	}
}

func TestStartHealthCheckServer(t *testing.T) {
	lis, err := listen(t.Context(), t.Name(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

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
				lis,
				slog.Default(),
				grpcLis,
			)

			req := httptest.NewRequest("GET", "/", nil)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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

func TestStartHealthCheckServer_ErrorCases(t *testing.T) {
	// Test health check RPC error.
	t.Run("health check RPC error", func(t *testing.T) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer lis.Close()

		grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer grpcLis.Close()

		// Start a gRPC server that returns error.
		grpcServer := grpc.NewServer()
		healthServer := &mockHealthServerError{}
		grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
		go func() {
			_ = grpcServer.Serve(grpcLis)
		}()
		defer grpcServer.Stop()

		httpSrv := startHealthCheckServer(lis, slog.Default(), grpcLis)
		defer httpSrv.Close()

		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		httpSrv.Handler.ServeHTTP(rr, req)
		res := rr.Result()
		defer res.Body.Close()

		require.Equal(t, http.StatusInternalServerError, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		require.Contains(t, string(body), "health check RPC failed")
	})

	// Test unhealthy status.
	t.Run("unhealthy status", func(t *testing.T) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer lis.Close()

		grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer grpcLis.Close()

		// Start a gRPC server that returns unhealthy status.
		grpcServer := grpc.NewServer()
		healthServer := &mockHealthServer{status: grpc_health_v1.HealthCheckResponse_NOT_SERVING}
		grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
		go func() {
			_ = grpcServer.Serve(grpcLis)
		}()
		defer grpcServer.Stop()

		httpSrv := startHealthCheckServer(lis, slog.Default(), grpcLis)
		defer httpSrv.Close()

		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		httpSrv.Handler.ServeHTTP(rr, req)
		res := rr.Result()
		defer res.Body.Close()

		require.Equal(t, http.StatusInternalServerError, res.StatusCode)
		body, _ := io.ReadAll(res.Body)
		require.Contains(t, string(body), "unhealthy status")
	})
}

type mockHealthServer struct {
	grpc_health_v1.UnimplementedHealthServer
	status grpc_health_v1.HealthCheckResponse_ServingStatus
}

func (m *mockHealthServer) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: m.status}, nil
}

type mockHealthServerError struct {
	grpc_health_v1.UnimplementedHealthServer
}

func (m *mockHealthServerError) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return nil, fmt.Errorf("health check failed")
}

// TestExtProcStartupMessage ensures other programs can rely on the startup message to STDERR.
func TestExtProcStartupMessage(t *testing.T) {
	// Create a temporary config file.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`metadataNamespace: test_ns
schema:
  name: OpenAI
  version: v1
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
			"-metricsPort", "0",
			"-healthPort", "0",
		}
		errCh <- Main(ctx, args, stderrW)
	}()

	// block until the context is canceled or an error occurs.
	err := <-errCh
	require.NoError(t, err)
}

func TestConfigureTracing_DisabledByEnv(t *testing.T) {
	tests := []struct {
		name         string
		sdkDisabled  string
		otlpEndpoint string
	}{
		{
			name:         "OTEL_SDK_DISABLED true",
			sdkDisabled:  "true",
			otlpEndpoint: "http://localhost:4318", // Should be ignored.
		},
		{
			name:         "OTEL_EXPORTER_OTLP_ENDPOINT unset",
			sdkDisabled:  "",
			otlpEndpoint: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_SDK_DISABLED", tt.sdkDisabled)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tt.otlpEndpoint)

			provider, propagator, err := configureTracing(t.Context())
			require.NoError(t, err)
			require.IsType(t, noop.TracerProvider{}, provider)
			require.IsType(t, noopTextMapPropagator{}, propagator)
		})
	}
}

// TestConfigureTracing_TracesSampler tests that the OTEL_TRACES_SAMPLER env
// variable works.
// See: https://opentelemetry.io/docs/languages/sdk-configuration/general/#otel_traces_sampler
func TestConfigureTracing_TracesSampler(t *testing.T) {
	// Just test 2 samplers to prove the SDK is wired up correctly.
	tests := []struct {
		sampler       string
		expectSampled bool
	}{
		{"always_on", true},
		{"always_off", false},
	}

	for _, tt := range tests {
		t.Run(tt.sampler, func(t *testing.T) {
			t.Setenv("OTEL_TRACES_SAMPLER", tt.sampler)
			collector, tracer, _ := configureTracingForTest(t)

			// Create a span and check if it's sampled.
			_, span := tracer.Start(t.Context(), "test-span")
			if tt.expectSampled {
				require.True(t, span.IsRecording())
			} else {
				require.False(t, span.IsRecording())
			}
			span.End()

			// Now, verify the actual ENV were honored.
			v1Span := collector.TakeSpan()
			if tt.expectSampled {
				require.NotNil(t, v1Span)
			} else {
				require.Nil(t, v1Span)
			}
		})
	}
}

// TestConfigureTracing_OtelPropagators tests that the OTEL_PROPAGATORS env
// variable works.
// See: https://opentelemetry.io/docs/languages/sdk-configuration/general/#otel_propagators
func TestConfigureTracing_OtelPropagators(t *testing.T) {
	// Just test 2 propagators to prove the SDK is wired up correctly.
	tests := []struct {
		propagator    string
		expectHeaders func(string, string) map[string]string
	}{
		{
			propagator: "b3",
			expectHeaders: func(traceID, spanID string) map[string]string {
				return map[string]string{"b3": traceID + "-" + spanID + "-1"}
			},
		},
		{
			propagator: "tracecontext",
			expectHeaders: func(traceID, spanID string) map[string]string {
				return map[string]string{"traceparent": "00-" + traceID + "-" + spanID + "-01"}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.propagator, func(t *testing.T) {
			t.Setenv("OTEL_PROPAGATORS", tt.propagator)
			_, tracer, propagator := configureTracingForTest(t)

			// Create and inject the span.
			ctx, span := tracer.Start(t.Context(), "test-span")
			headers := make(map[string]string)
			propagator.Inject(ctx, propagation.MapCarrier(headers))
			span.End()

			// Ensure the propagation format is honored.
			traceID := span.SpanContext().TraceID().String()
			spanID := span.SpanContext().SpanID().String()
			require.Equal(t, tt.expectHeaders(traceID, spanID), headers)
		})
	}
}

func configureTracingForTest(t *testing.T) (*testotel.OTLPCollector, trace.Tracer, propagation.TextMapPropagator) {
	collector := testotel.StartOTLPCollector()
	t.Cleanup(collector.Close)
	collector.SetEnv(t.Setenv)

	provider, propagator, err := configureTracing(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = maybeTracerProviderShutdown(context.Background(), provider)
	})

	return collector, provider.Tracer("test-tracer"), propagator
}
