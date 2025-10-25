// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestNewNonEmptyConsoleExporter(t *testing.T) {
	tests := []struct {
		name         string
		envValue     string
		expectedTemp metricdata.Temporality
		expectError  bool
	}{
		{
			name:         "defaults to cumulative",
			envValue:     "",
			expectedTemp: metricdata.CumulativeTemporality,
			expectError:  false,
		},
		{
			name:         "explicit cumulative",
			envValue:     "cumulative",
			expectedTemp: metricdata.CumulativeTemporality,
			expectError:  false,
		},
		{
			name:         "explicit delta",
			envValue:     "delta",
			expectedTemp: metricdata.DeltaTemporality,
			expectError:  false,
		},
		{
			name:         "unsupported value",
			envValue:     "unsupported",
			expectedTemp: metricdata.CumulativeTemporality,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", tt.envValue)

			var buf bytes.Buffer
			exp, err := newNonEmptyConsoleExporter(&buf)
			if tt.expectError {
				require.ErrorContains(t, err, "unsupported")
				return
			}
			require.NoError(t, err)

			require.Equal(t, tt.expectedTemp, exp.Temporality(sdkmetric.InstrumentKindCounter))
			require.NoError(t, exp.Shutdown(t.Context()))
		})
	}
}

func TestParseTemporalityPreference(t *testing.T) {
	tests := []struct {
		name        string
		envValue    string
		expected    metricdata.Temporality
		expectError bool
	}{
		{
			name:        "empty defaults to cumulative",
			envValue:    "",
			expected:    metricdata.CumulativeTemporality,
			expectError: false,
		},
		{
			name:        "explicit cumulative",
			envValue:    "cumulative",
			expected:    metricdata.CumulativeTemporality,
			expectError: false,
		},
		{
			name:        "explicit delta",
			envValue:    "delta",
			expected:    metricdata.DeltaTemporality,
			expectError: false,
		},
		{
			name:        "unsupported value",
			envValue:    "unsupported",
			expected:    metricdata.CumulativeTemporality,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", tt.envValue)

			temporality, err := parseTemporalityPreference()
			if tt.expectError {
				require.ErrorContains(t, err, "unsupported")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expected, temporality)
		})
	}
}

func TestNonEmptyExporter_Export(t *testing.T) {
	tests := []struct {
		name            string
		resourceMetrics *metricdata.ResourceMetrics
		expectOutput    bool
	}{
		{
			name:            "nil resource metrics",
			resourceMetrics: nil,
			expectOutput:    false,
		},
		{
			name: "empty scope metrics",
			resourceMetrics: &metricdata.ResourceMetrics{
				Resource:     resource.Default(),
				ScopeMetrics: []metricdata.ScopeMetrics{},
			},
			expectOutput: false,
		},
		{
			name: "scope with no metrics",
			resourceMetrics: &metricdata.ResourceMetrics{
				Resource: resource.Default(),
				ScopeMetrics: []metricdata.ScopeMetrics{
					{
						Scope:   instrumentation.Scope{Name: "test-scope"},
						Metrics: []metricdata.Metrics{},
					},
				},
			},
			expectOutput: false,
		},
		{
			name: "scope with metrics",
			resourceMetrics: &metricdata.ResourceMetrics{
				Resource: resource.Default(),
				ScopeMetrics: []metricdata.ScopeMetrics{
					{
						Scope: instrumentation.Scope{Name: "test-scope"},
						Metrics: []metricdata.Metrics{
							{
								Name: "test.counter",
								Data: metricdata.Sum[int64]{
									DataPoints: []metricdata.DataPoint[int64]{
										{
											Attributes: attribute.NewSet(),
											Value:      42,
										},
									},
									Temporality: metricdata.CumulativeTemporality,
									IsMonotonic: true,
								},
							},
						},
					},
				},
			},
			expectOutput: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			exp, err := newNonEmptyExporter(&buf)
			require.NoError(t, err)

			err = exp.Export(t.Context(), tt.resourceMetrics)
			require.NoError(t, err)

			output := strings.TrimSpace(buf.String())
			if tt.expectOutput {
				require.NotEmpty(t, output)
			} else {
				require.Empty(t, output)
			}
		})
	}
}

func TestNonEmptyExporter_TemporalityAndAggregation(t *testing.T) {
	var buf bytes.Buffer
	exp, err := newNonEmptyExporter(&buf)
	require.NoError(t, err)

	// Check one representative kind
	kind := sdkmetric.InstrumentKindCounter
	require.Equal(t, metricdata.CumulativeTemporality, exp.Temporality(kind))
	require.NotNil(t, exp.Aggregation(kind))
}

func TestNonEmptyExporter_ShutdownAndForceFlush(t *testing.T) {
	var buf bytes.Buffer
	exp, err := newNonEmptyExporter(&buf)
	require.NoError(t, err)

	require.NoError(t, exp.ForceFlush(t.Context()))
	require.NoError(t, exp.Shutdown(t.Context()))
}

func TestNonEmptyExporter_Integration(t *testing.T) {
	var buf bytes.Buffer
	exp, err := newNonEmptyExporter(&buf)
	require.NoError(t, err)

	reader := sdkmetric.NewPeriodicReader(exp)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(resource.NewSchemaless(
			attribute.String("service.name", "test-service"),
		)),
	)
	defer func() {
		_ = mp.Shutdown(context.Background())
	}()

	meter := mp.Meter("test-meter")

	// No metrics recorded
	buf.Reset()
	require.NoError(t, mp.ForceFlush(t.Context()))
	require.Empty(t, strings.TrimSpace(buf.String()))

	// Metrics recorded
	counter, err := meter.Int64Counter("test.counter")
	require.NoError(t, err)
	counter.Add(t.Context(), 1)

	buf.Reset()
	require.NoError(t, mp.ForceFlush(t.Context()))
	require.NotEmpty(t, strings.TrimSpace(buf.String()))
}
