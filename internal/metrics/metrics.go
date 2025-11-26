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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// NewMeterFromEnv configures an OpenTelemetry MeterProvider based on environment variables,
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
func NewMeterFromEnv(ctx context.Context, stdout io.Writer, promReader sdkmetric.Reader) (metric.Meter, func(context.Context) error, error) {
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
			// We hardcode "service.name" to avoid pinning semconv version.
			fallbackRes := resource.NewSchemaless(
				attribute.String("service.name", "ai-gateway"),
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
				exp, err := newNonEmptyConsoleExporter(stdout)
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

// OptUint32 is an optional uint32 type represented as an uint64 to allow for a None value.
type OptUint32 uint64

// OptUint32None represents the None value for OptUint32.
const OptUint32None = OptUint32(0xffffffff_00000000)

// Metrics is the interface for the base AI Gateway metrics.
type Metrics interface {
	// StartRequest initializes timing for a new request.
	StartRequest(headers map[string]string)
	// SetOriginalModel sets the original model from the incoming request body before any virtualization applies.
	// This is usually called after parsing the request body. Example: gpt-5
	SetOriginalModel(originalModel internalapi.OriginalModel)
	// SetRequestModel sets the model from the request. This is usually called after parsing the request body.
	// Example: gpt-5-nano
	SetRequestModel(requestModel internalapi.RequestModel)
	// SetResponseModel sets the model that ultimately generated the response.
	// Example: gpt-5-nano-2025-08-07
	SetResponseModel(responseModel internalapi.ResponseModel)
	// SetBackend sets the selected backend when the routing decision has been made. This is usually called
	// after parsing the request body to determine the model and invoke the routing logic.
	SetBackend(backend *filterapi.Backend)
	// RecordRequestCompletion records the completion of the request, including success status.
	RecordRequestCompletion(ctx context.Context, success bool, requestHeaders map[string]string)
	// RecordTokenUsage records token usage metrics.
	//
	// Depending on the endpoint, some token types are not available and should be passed as OptUint32None.
	RecordTokenUsage(ctx context.Context, inputTokens, cachedInputTokens, outputTokens OptUint32, requestHeaders map[string]string)

	// Streaming-specific metrics methods, not used by all implementations.

	// GetTimeToFirstTokenMs returns the time to first token in stream mode in milliseconds.
	GetTimeToFirstTokenMs() float64
	// GetInterTokenLatencyMs returns the inter token latency in stream mode in milliseconds.
	GetInterTokenLatencyMs() float64
	// RecordTokenLatency records latency metrics for token generation.
	RecordTokenLatency(ctx context.Context, tokens uint32, endOfStream bool, requestHeaders map[string]string)
}

// Factory is a closure that creates a new Metrics instance for a given operation.
type Factory interface {
	// NewMetrics creates a new Metrics instance for the specified operation name.
	NewMetrics() Metrics
}

// NewMetricsFactory returns a Factory to create a new Metrics instance.
func NewMetricsFactory(meter metric.Meter, requestHeaderLabelMapping map[string]string, operation GenAIOperation) Factory {
	return &metricsImplFactory{metrics: newGenAI(meter), requestHeaderAttributeMapping: requestHeaderLabelMapping, operation: string(operation)}
}
