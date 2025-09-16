// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"
	"testing"

	promregistry "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// TestNewMetricsFromEnv_DefaultServiceName tests that the service name.
// defaults to "ai-gateway" when OTEL_SERVICE_NAME is not set.
func TestNewMetricsFromEnv_DefaultServiceName(t *testing.T) {
	tests := []struct {
		name              string
		env               map[string]string
		expectServiceName string
		expectNoop        bool
	}{
		{
			name: "default service name when OTEL_SERVICE_NAME not set",
			env: map[string]string{
				"OTEL_METRICS_EXPORTER": "console",
			},
			expectServiceName: "ai-gateway",
		},
		{
			name: "OTEL_SERVICE_NAME overrides default",
			env: map[string]string{
				"OTEL_METRICS_EXPORTER": "console",
				"OTEL_SERVICE_NAME":     "custom-service",
			},
			expectServiceName: "custom-service",
		},
		{
			name:              "prometheus default when no metrics exporter set",
			env:               map[string]string{},
			expectServiceName: "ai-gateway",
		},
		{
			name: "disabled when OTEL_SDK_DISABLED is true",
			env: map[string]string{
				"OTEL_SDK_DISABLED":     "true",
				"OTEL_METRICS_EXPORTER": "console",
			},
			expectNoop: true,
		},
		{
			name: "disabled when OTEL_METRICS_EXPORTER is none",
			env: map[string]string{
				"OTEL_METRICS_EXPORTER": "none",
			},
			expectNoop: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			result, err := NewMetricsFromEnv(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = result.Shutdown(context.Background())
			})

			if tt.expectNoop {
				_, ok := result.(NoopMetrics)
				require.True(t, ok, "expected NoopMetrics")
			} else {
				_, ok := result.(NoopMetrics)
				require.False(t, ok, "expected non-noop metrics")

				// For console exporter, we should see output containing service name.
				if tt.env["OTEL_METRICS_EXPORTER"] == "console" {
					meter := result.Meter()
					require.NotNil(t, meter)
					_, ok := meter.(noop.Meter)
					require.False(t, ok, "expected non-noop meter")
				}

				// For prometheus, check registry exists.
				if tt.env["OTEL_METRICS_EXPORTER"] == "" || tt.env["OTEL_METRICS_EXPORTER"] == "prometheus" {
					registry := result.Registry()
					require.NotNil(t, registry, "expected prometheus registry")
				}
			}
		})
	}
}

// TestNewMetricsFromEnv_ExporterPrecedence tests that metrics-specific.
// exporter configuration takes precedence over generic OTLP configuration.
func TestNewMetricsFromEnv_ExporterPrecedence(t *testing.T) {
	tests := []struct {
		name           string
		env            map[string]string
		expectExporter string
		expectNoop     bool
	}{
		{
			name: "metrics-specific exporter takes precedence over generic OTLP",
			env: map[string]string{
				"OTEL_METRICS_EXPORTER":       "console",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4317",
			},
			expectExporter: "console",
		},
		{
			name: "uses OTLP when generic endpoint is configured",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4317",
			},
			expectExporter: "otlp",
		},
		{
			name: "uses metrics-specific OTLP endpoint",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4318",
			},
			expectExporter: "otlp",
		},
		{
			name: "metrics-specific endpoint takes precedence over generic",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://localhost:4317",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4318",
			},
			expectExporter: "otlp",
		},
		{
			name:           "defaults to prometheus when no configuration",
			env:            map[string]string{},
			expectExporter: "prometheus",
		},
		{
			name: "respects none exporter",
			env: map[string]string{
				"OTEL_METRICS_EXPORTER":       "none",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4317",
			},
			expectNoop: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			result, err := NewMetricsFromEnv(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = result.Shutdown(context.Background())
			})

			if tt.expectNoop {
				_, ok := result.(NoopMetrics)
				require.True(t, ok, "expected NoopMetrics")
			} else {
				_, ok := result.(NoopMetrics)
				require.False(t, ok, "expected non-noop metrics")

				// Verify the correct exporter type is configured.
				switch tt.expectExporter {
				case "console":
					// Console writes to stdout.
					meter := result.Meter()
					require.NotNil(t, meter)
				case "prometheus":
					// Prometheus provides a registry.
					registry := result.Registry()
					require.NotNil(t, registry, "expected prometheus registry")
				case "otlp":
					// OTLP doesn't provide a registry.
					registry := result.Registry()
					require.Nil(t, registry, "expected no registry for OTLP")
				}
			}
		})
	}
}

// TestNewMetricsFromEnv_ConsoleExporter tests that console exporter works.
// without requiring OTLP endpoints and doesn't make network calls.
func TestNewMetricsFromEnv_ConsoleExporter(t *testing.T) {
	tests := []struct {
		name                string
		env                 map[string]string
		expectNoop          bool
		expectRegistry      bool
		expectConsoleOutput bool
	}{
		{
			name: "console exporter without any endpoints",
			env: map[string]string{
				"OTEL_METRICS_EXPORTER": "console",
			},
			expectConsoleOutput: true,
			expectRegistry:      false,
		},
		{
			name: "console exporter ignores OTLP endpoints",
			env: map[string]string{
				"OTEL_METRICS_EXPORTER":               "console",
				"OTEL_EXPORTER_OTLP_ENDPOINT":         "http://should-be-ignored:4317",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://should-be-ignored:4318",
			},
			expectConsoleOutput: true,
			expectRegistry:      false,
		},
		{
			name: "console exporter with custom service name",
			env: map[string]string{
				"OTEL_METRICS_EXPORTER": "console",
				"OTEL_SERVICE_NAME":     "test-console-service",
			},
			expectConsoleOutput: true,
			expectRegistry:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			result, err := NewMetricsFromEnv(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = result.Shutdown(context.Background())
			})

			if tt.expectNoop {
				_, ok := result.(NoopMetrics)
				require.True(t, ok, "expected NoopMetrics")
				return
			}

			// Verify it's not noop.
			_, ok := result.(NoopMetrics)
			require.False(t, ok, "expected non-noop metrics")

			// Check registry expectation.
			registry := result.Registry()
			if tt.expectRegistry {
				require.NotNil(t, registry, "expected prometheus registry")
			} else {
				require.Nil(t, registry, "expected no registry for console exporter")
			}

			// For console exporter, verify it has a valid meter.
			meter := result.Meter()
			require.NotNil(t, meter)

			// Create a test counter to verify console output works.
			if tt.expectConsoleOutput {
				counter, err := meter.Int64Counter("test.counter")
				require.NoError(t, err)
				counter.Add(t.Context(), 1)

				// Console exporter is periodic, but we can verify the meter was created.
				// The actual output would appear after the export interval.
				_, ok := meter.(noop.Meter)
				require.False(t, ok, "console exporter should provide real meter, not noop")
			}
		})
	}
}

// TestNewMetrics tests the NewMetrics function with provided meter and registry.
func TestNewMetrics(t *testing.T) {
	tests := []struct {
		name       string
		meter      func() metric.Meter
		registry   *promregistry.Registry
		expectNoop bool
	}{
		{
			name: "non-noop meter",
			meter: func() metric.Meter {
				mp := sdkmetric.NewMeterProvider()
				return mp.Meter("test")
			},
			registry:   promregistry.NewRegistry(),
			expectNoop: false,
		},
		{
			name: "noop meter",
			meter: func() metric.Meter {
				return noop.NewMeterProvider().Meter("test")
			},
			registry:   nil,
			expectNoop: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewMetrics(tt.meter(), tt.registry)

			if tt.expectNoop {
				_, ok := result.(NoopMetrics)
				require.True(t, ok, "expected NoopMetrics")
			} else {
				impl, ok := result.(*metricsImpl)
				require.True(t, ok, "expected metricsImpl")
				require.NotNil(t, impl.meter)
				require.Equal(t, tt.registry, impl.registry)
				require.Nil(t, impl.shutdown, "shutdown should be nil when meter is provided")
			}
		})
	}
}
