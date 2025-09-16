// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"
	"fmt"
	"os"

	promregistry "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// Metrics is the interface for OpenTelemetry metrics configuration.
type Metrics interface {
	// Meter returns the meter for creating metrics.
	Meter() metric.Meter
	// Registry returns the Prometheus registry if metrics are exported to Prometheus, nil otherwise.
	Registry() *promregistry.Registry
	// Shutdown shuts down the metrics provider.
	Shutdown(context.Context) error
}

var _ Metrics = (*metricsImpl)(nil)

type metricsImpl struct {
	meter    metric.Meter
	registry *promregistry.Registry
	// shutdown is nil when we didn't create mp.
	shutdown func(context.Context) error
}

// Meter implements the same method as documented on Metrics.
func (m *metricsImpl) Meter() metric.Meter {
	return m.meter
}

// Registry implements the same method as documented on Metrics.
func (m *metricsImpl) Registry() *promregistry.Registry {
	return m.registry
}

// Shutdown implements the same method as documented on Metrics.
func (m *metricsImpl) Shutdown(ctx context.Context) error {
	if m.shutdown != nil {
		return m.shutdown(ctx)
	}
	return nil
}

// NoopMetrics returns a no-op metrics implementation.
type NoopMetrics struct{}

// Meter returns a no-op meter.
func (NoopMetrics) Meter() metric.Meter { return noop.NewMeterProvider().Meter("noop") }

// Registry returns nil for no-op metrics.
func (NoopMetrics) Registry() *promregistry.Registry { return nil }

// Shutdown is a no-op.
func (NoopMetrics) Shutdown(context.Context) error { return nil }

// NewMetricsFromEnv configures OpenTelemetry metrics based on environment
// variables. Returns a metrics graph that is noop when disabled.
func NewMetricsFromEnv(ctx context.Context) (Metrics, error) {
	// Return no-op metrics if disabled.
	if os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return NoopMetrics{}, nil
	}

	// Check for metrics-specific exporter first, then fall back to generic.
	exporter := os.Getenv("OTEL_METRICS_EXPORTER")
	if exporter == "none" {
		return NoopMetrics{}, nil
	}

	// If no metrics-specific exporter is set, check if OTLP endpoints are configured.
	// According to OTEL spec, we should use OTLP if any endpoint is configured.
	// The autoexport library will handle the endpoint precedence correctly:
	// 1. OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (metrics-specific)
	// 2. OTEL_EXPORTER_OTLP_ENDPOINT (generic base endpoint).
	if exporter == "" {
		// Check if any OTLP endpoint is configured for metrics.
		hasOTLPEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
			os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != ""

		if !hasOTLPEndpoint {
			// Default to prometheus for backward compatibility when no OTLP is configured.
			exporter = "prometheus"
		}
		// Fall through to handle configuration.
	}

	// Create resource with service name, defaulting to "ai-gateway" if not set.
	// First create default resource, then one from env, then our fallback.
	// The merge order ensures env vars override our default.
	defaultRes := resource.Default()
	envRes, err := resource.New(ctx,
		resource.WithFromEnv(),      // Read OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES.
		resource.WithTelemetrySDK(), // Add telemetry SDK info.
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource from env: %w", err)
	}

	// Only set our default if service.name wasn't set via env
	fallbackRes := resource.NewSchemaless(
		semconv.ServiceName("ai-gateway"),
	)

	// Merge in order: default -> fallback -> env (env takes precedence).
	res, err := resource.Merge(defaultRes, fallbackRes)
	if err != nil {
		return nil, fmt.Errorf("failed to merge default resources: %w", err)
	}
	res, err = resource.Merge(res, envRes)
	if err != nil {
		return nil, fmt.Errorf("failed to merge env resource: %w", err)
	}

	// Create the meter provider.
	var mp *sdkmetric.MeterProvider
	var registry *promregistry.Registry

	// Special case prometheus to serve on our own HTTP server instead of autoexport's.
	if exporter == "prometheus" {
		registry = promregistry.NewRegistry()
		promExporter, err := prometheus.New(prometheus.WithRegisterer(registry))
		if err != nil {
			return nil, fmt.Errorf("failed to create prometheus exporter: %w", err)
		}
		// We don't add sdkmetric.WithResource(res) to ensure we remain compat with aigw 0.3.x
		mp = sdkmetric.NewMeterProvider(sdkmetric.WithReader(promExporter))
	} else {
		// Use autoexport for everything else (console, otlp, etc.).
		// This handles OTEL_METRICS_EXPORTER values like "console", "otlp", "none".
		autoExporter, err := autoexport.NewMetricReader(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create exporter: %w", err)
		}
		mp = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(autoExporter),
			sdkmetric.WithResource(res),
		)
	}

	return &metricsImpl{
		meter:    mp.Meter("envoyproxy/ai-gateway"),
		registry: registry,
		shutdown: mp.Shutdown, // we have to shut down what we create.
	}, nil
}

// NewMetrics configures OpenTelemetry metrics based on the configuration.
// Returns a metrics graph that is noop when the meter is no-op.
func NewMetrics(meter metric.Meter, registry *promregistry.Registry) Metrics {
	if _, ok := meter.(noop.Meter); ok {
		return NoopMetrics{}
	}
	return &metricsImpl{
		meter:    meter,
		registry: registry,
		shutdown: nil, // shutdown is nil when we didn't create mp.
	}
}
