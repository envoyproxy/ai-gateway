// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"
	"io"
	"os"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// NewMetricsFromEnv configures an OpenTelemetry MeterProvider based on environment variables,
// always incorporating the provided Prometheus reader. It optionally includes additional exporters
// (e.g., console or OTLP) if enabled via environment variables. The function returns a metric.Meter
// for instrumentation and a shutdown function to gracefully close the provider.
//
// The stdout parameter directs output for the console exporter (use os.Stdout in production).
// Environment variables checked directly include:
//   - OTEL_SDK_DISABLED: If "true", disables OTEL exporters.
//   - OTEL_METRICS_EXPORTER: Supported values are "none", "console", "prometheus", "otlp".
//   - OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: Enables OTLP if set.
//
// Prometheus is always enabled via the provided promReader; other exporters are added conditionally.
func NewMetricsFromEnv(ctx context.Context, stdout io.Writer, promReader sdkmetric.Reader) (metric.Meter, func(context.Context) error, error) {
	// Initialize options for the MeterProvider, starting with the required Prometheus reader.
	var options []sdkmetric.Option
	options = append(options, sdkmetric.WithReader(promReader))

	// Add OTEL exporters only if the SDK is not disabled.
	if os.Getenv("OTEL_SDK_DISABLED") != "true" {
		exporter := os.Getenv("OTEL_METRICS_EXPORTER")
		hasOTLPEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
			os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != ""

		// Proceed if exporter is "console" or if OTLP is implied (not "none" or "prometheus" with endpoint set).
		if exporter == "console" || (exporter != "none" && exporter != "prometheus" && hasOTLPEndpoint) {
			// Configure resource with default attributes, fallback service name, and environment overrides.
			defaultRes := resource.Default()
			envRes, err := resource.New(ctx,
				resource.WithFromEnv(),
				resource.WithTelemetrySDK(),
			)
			if err != nil {
				return nil, nil, err
			}
			// Ensure a service name is set if not provided via environment.
			fallbackRes := resource.NewSchemaless(
				semconv.ServiceName("ai-gateway"),
			)
			res, err := resource.Merge(defaultRes, fallbackRes)
			if err != nil {
				return nil, nil, err
			}
			res, err = resource.Merge(res, envRes)
			if err != nil {
				return nil, nil, err
			}
			options = append(options, sdkmetric.WithResource(res))

			if exporter == "console" {
				// Configure console exporter with a PeriodicReader for aggregated metric export.
				exp, err := stdoutmetric.New(
					stdoutmetric.WithWriter(stdout),
				)
				if err != nil {
					return nil, nil, err
				}
				reader := sdkmetric.NewPeriodicReader(exp)
				options = append(options, sdkmetric.WithReader(reader))
			} else {
				// Use autoexport for OTLP, which internally handles PeriodicReader for aggregation.
				otelReader, err := autoexport.NewMetricReader(ctx)
				if err != nil {
					return nil, nil, err
				}
				options = append(options, sdkmetric.WithReader(otelReader))
			}
		}
	}

	// Create and return the MeterProvider with all configured options.
	mp := sdkmetric.NewMeterProvider(options...)
	return mp.Meter("envoyproxy/ai-gateway"), mp.Shutdown, nil
}
