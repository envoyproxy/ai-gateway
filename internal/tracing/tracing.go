// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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
func NewTracingFromEnv(ctx context.Context) (tracing.Tracing, error) {
	// Check if tracing is explicitly disabled via environment variable.
	if os.Getenv("OTEL_SDK_DISABLED") == "true" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return tracing.NoopTracing{}, nil
	}

	// Create OTLP trace exporter using environment variables.
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// The SDK automatically configures the batch processor from environment
	// variables like OTEL_BSP_SCHEDULE_DELAY, OTEL_BSP_MAX_QUEUE_SIZE, etc.
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))

	// Use autoprop to honor the OTEL_PROPAGATORS environment variable.
	// Defaults to "tracecontext,baggage" and can include b3, b3multi, etc.
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
