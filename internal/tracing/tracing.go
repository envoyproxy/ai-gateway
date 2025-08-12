// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"
	"fmt"
	"io"
	"os"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace/noop"

	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference/openai"
)

var _ tracing.Tracing = (*tracingImpl)(nil)

type tracingImpl struct {
	chatCompletionTracer tracing.ChatCompletionTracer
	// shutdown is nil when we didn't create tp.
	shutdown func(context.Context) error
}

// ChatCompletionTracer implements the same method as documented on api.Tracing.
func (t *tracingImpl) ChatCompletionTracer() tracing.ChatCompletionTracer {
	return t.chatCompletionTracer
}

// Shutdown implements the same method as documented on api.Tracing.
func (t *tracingImpl) Shutdown(ctx context.Context) error {
	if t.shutdown != nil {
		return t.shutdown(ctx)
	}
	return nil
}

// NewTracingFromEnv configures OpenTelemetry tracing based on environment
// variables. Returns a tracing graph that is noop when disabled.
func NewTracingFromEnv(ctx context.Context, stdout io.Writer) (tracing.Tracing, error) {
	// Return no-op tracing if disabled or no exporter/endpoint is configured.
	exporter := os.Getenv("OTEL_TRACES_EXPORTER")
	if os.Getenv("OTEL_SDK_DISABLED") == "true" || exporter == "none" ||
		(exporter == "" && os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "") {
		return tracing.NoopTracing{}, nil
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

	// Only set our default if service.name wasn't set via env.
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

	// Create the tracer provider, special casing console for sync and tests.
	var tp *sdktrace.TracerProvider
	if exporter == "console" {
		stdoutExporter, err := stdouttrace.New(stdouttrace.WithWriter(stdout))
		if err != nil {
			return nil, fmt.Errorf("failed to create console exporter: %w", err)
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithSyncer(stdoutExporter),
			sdktrace.WithResource(res),
		)

	} else { // Configure exporter via ENV variables like OTEL_TRACES_EXPORTER.
		autoExporter, err := autoexport.NewSpanExporter(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create exporter: %w", err)
		}
		// Configure batcher via ENV variables like OTEL_BSP_SCHEDULE_DELAY.
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(autoExporter),
			sdktrace.WithResource(res),
		)
	}

	// Configure propagation via the OTEL_PROPAGATORS ENV variable.
	propagator := autoprop.NewTextMapPropagator()

	// Default to OpenInference trace span semantic conventions.
	recorder := openai.NewChatCompletionRecorderFromEnv()

	return &tracingImpl{
		chatCompletionTracer: newChatCompletionTracer(
			tp.Tracer("envoyproxy/ai-gateway"),
			propagator,
			recorder,
		),
		shutdown: tp.Shutdown, // we have to shut down what we create.
	}, nil
}

type Shutdown interface {
	Shutdown(context.Context) error
}
type noopShutdown struct{}

func (noopShutdown) Shutdown(context.Context) error { return nil }

// NewTracing configures OpenTelemetry tracing based on the configuration.
// Returns a tracing graph that is noop when the tracer provider is no-op.
func NewTracing(config *tracing.TracingConfig) tracing.Tracing {
	if _, ok := config.Tracer.(noop.Tracer); ok {
		return tracing.NoopTracing{}
	}
	return &tracingImpl{
		chatCompletionTracer: newChatCompletionTracer(
			config.Tracer,
			config.Propagator,
			config.ChatCompletionRecorder,
		),
		shutdown: nil, // shutdown is nil when we didn't create tp.
	}
}
