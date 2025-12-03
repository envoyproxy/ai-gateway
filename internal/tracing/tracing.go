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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference/cohere"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference/openai"
)

var _ tracing.Tracing = (*tracingImpl)(nil)

type tracingImpl struct {
	chatCompletionTracer  tracing.ChatCompletionTracer
	completionTracer      tracing.CompletionTracer
	imageGenerationTracer tracing.ImageGenerationTracer
	embeddingsTracer      tracing.EmbeddingsTracer
	rerankTracer          tracing.RerankTracer
	messageTracer         tracing.MessageTracer
	mcpTracer             tracing.MCPTracer
	// shutdown is nil when we didn't create tp.
	shutdown func(context.Context) error
}

// ChatCompletionTracer implements the same method as documented on api.Tracing.
func (t *tracingImpl) ChatCompletionTracer() tracing.ChatCompletionTracer {
	return t.chatCompletionTracer
}

// CompletionTracer implements the same method as documented on api.Tracing.
func (t *tracingImpl) CompletionTracer() tracing.CompletionTracer {
	return t.completionTracer
}

// EmbeddingsTracer implements the same method as documented on api.Tracing.
func (t *tracingImpl) EmbeddingsTracer() tracing.EmbeddingsTracer {
	return t.embeddingsTracer
}

// ImageGenerationTracer implements the same method as documented on api.Tracing.
func (t *tracingImpl) ImageGenerationTracer() tracing.ImageGenerationTracer {
	return t.imageGenerationTracer
}

// RerankTracer implements the same method as documented on api.Tracing.
func (t *tracingImpl) RerankTracer() tracing.RerankTracer {
	return t.rerankTracer
}

// MCPTracer implements the same method as documented on api.Tracing.
func (t *tracingImpl) MCPTracer() tracing.MCPTracer {
	return t.mcpTracer
}

// MessageTracer implements the same method as documented on api.Tracing.
func (t *tracingImpl) MessageTracer() tracing.MessageTracer {
	return t.messageTracer
}

// Shutdown implements the same method as documented on api.Tracing.
func (t *tracingImpl) Shutdown(ctx context.Context) error {
	if t.shutdown != nil {
		return t.shutdown(ctx)
	}
	return nil
}

// NewTracingFromEnv configures OpenTelemetry tracing based on environment
// variables and optional header attribute mapping.
//
// Parameters:
//   - headerAttributeMapping: maps HTTP headers to otel span attributes (e.g. map["x-session-id"]="session.id").
//     If nil, no header mapping is applied.
//
// Returns a tracing graph that is noop when disabled.
func NewTracingFromEnv(ctx context.Context, stdout io.Writer, headerAttributeMapping map[string]string) (tracing.Tracing, error) {
	// Return no-op tracing if disabled.
	if os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return tracing.NoopTracing{}, nil
	}

	// Check for traces-specific exporter first.
	exporter := os.Getenv("OTEL_TRACES_EXPORTER")
	if exporter == "none" {
		return tracing.NoopTracing{}, nil
	}

	// If no traces-specific exporter is set, check if OTLP endpoints are configured.
	// According to OTEL spec, we should use OTLP if any endpoint is configured.
	// The autoexport library will handle the endpoint precedence correctly:
	// 1. OTEL_EXPORTER_OTLP_TRACES_ENDPOINT (traces-specific)
	// 2. OTEL_EXPORTER_OTLP_ENDPOINT (generic base endpoint).
	if exporter == "" {
		hasOTLPEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
			os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""

		if !hasOTLPEndpoint {
			// No tracing configured.
			return tracing.NoopTracing{}, nil
		}
		// Fall through to use autoexport which will handle OTLP configuration.
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
	// We hardcode "service.name" to avoid pinning semconv version.
	fallbackRes := resource.NewSchemaless(
		attribute.String("service.name", "ai-gateway"),
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

	// Use provided header attribute mapping.
	headerAttrs := headerAttributeMapping

	// Default to OpenInference trace span semantic conventions.
	chatRecorder := openai.NewChatCompletionRecorderFromEnv()
	imageRecorder := openai.NewImageGenerationRecorderFromEnv()
	completionRecorder := openai.NewCompletionRecorderFromEnv()
	embeddingsRecorder := openai.NewEmbeddingsRecorderFromEnv()
	rerankRecorder := cohere.NewRerankRecorderFromEnv()

	tracer := tp.Tracer("envoyproxy/ai-gateway")
	return &tracingImpl{
		chatCompletionTracer: newChatCompletionTracer(
			tracer,
			propagator,
			chatRecorder,
			headerAttrs,
		),
		imageGenerationTracer: newImageGenerationTracer(
			tracer,
			propagator,
			imageRecorder,
		),
		completionTracer: newCompletionTracer(
			tracer,
			propagator,
			completionRecorder,
			headerAttrs,
		),
		embeddingsTracer: newEmbeddingsTracer(
			tracer,
			propagator,
			embeddingsRecorder,
			headerAttrs,
		),
		rerankTracer: newRerankTracer(
			tracer,
			propagator,
			rerankRecorder,
			headerAttrs,
		),
		// TODO: implement /message tracer: https://github.com/envoyproxy/ai-gateway/issues/1389
		messageTracer: tracing.NoopTracer[anthropicschema.MessagesRequest, anthropicschema.MessagesResponse, anthropicschema.MessagesStreamEventMessageDelta]{},
		mcpTracer:     newMCPTracer(tracer, propagator, headerAttrs),
		shutdown:      tp.Shutdown, // we have to shut down what we create.
	}, nil
}
