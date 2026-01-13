// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"k8s.io/utils/ptr"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// clearEnv clears any OTEL configuration that could exist in the environment.
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_SERVICE_NAME", "")
}

// TestNewTracingFromEnv_DefaultServiceName tests that the service name.
// defaults to "ai-gateway" when OTEL_SERVICE_NAME is not set.
func TestNewTracingFromEnv_DefaultServiceName(t *testing.T) {
	tests := []struct {
		name              string
		env               map[string]string
		expectServiceName string
	}{
		{
			name: "default service name when OTEL_SERVICE_NAME not set",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER": "console",
			},
			expectServiceName: "ai-gateway",
		},
		{
			name: "OTEL_SERVICE_NAME overrides default",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER": "console",
				"OTEL_SERVICE_NAME":    "custom-service",
			},
			expectServiceName: "custom-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			var stdout bytes.Buffer
			result, err := NewTracingFromEnv(t.Context(), &stdout, nil)
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = result.Shutdown(t.Context())
			})

			// Start a span to trigger output.
			span := startCompletionsSpan(t, result, nil)
			require.NotNil(t, span)
			span.EndSpan()

			// Check that the service name appears in the console output.
			output := stdout.String()
			require.Contains(t, output, `"service.name"`)
			require.Contains(t, output, tt.expectServiceName)
		})
	}
}

func TestNewTracingFromEnv_DisabledByEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "OTEL_SDK_DISABLED true",
			env: map[string]string{
				"OTEL_SDK_DISABLED":           "true",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318", // Should be ignored.
			},
		},
		{
			name: "OTEL_TRACES_EXPORTER none",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER":        "none",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318", // Should be ignored.
			},
		},
		{
			name: "no endpoints or exporters configured",
			env:  map[string]string{},
		},
		{
			name: "no traces endpoint when only metrics endpoint is configured",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://localhost:4318",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			result, err := NewTracingFromEnv(t.Context(), io.Discard, nil)
			require.NoError(t, err)
			require.IsType(t, tracingapi.NoopTracing{}, result)
		})
	}
}

// TestNewTracingFromEnv_EndpointHierarchy tests the OTEL endpoint hierarchy.
// according to the OTEL spec where signal-specific endpoints override generic ones.
func TestNewTracingFromEnv_EndpointHierarchy(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		expectActive bool
	}{
		{
			name: "uses generic OTLP endpoint when configured",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4317",
			},
			expectActive: true,
		},
		{
			name: "uses traces-specific endpoint when configured",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://localhost:4318",
			},
			expectActive: true,
		},
		{
			name: "traces-specific endpoint overrides generic",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT":        "http://localhost:4317",
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://localhost:4318",
			},
			expectActive: true,
		},
		{
			name: "explicit exporter overrides endpoint detection",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4317",
				"OTEL_TRACES_EXPORTER":        "console",
			},
			expectActive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			result, err := NewTracingFromEnv(t.Context(), io.Discard, nil)
			require.NoError(t, err)

			if tt.expectActive {
				_, isNoop := result.(tracingapi.NoopTracing)
				require.False(t, isNoop, "expected active tracing")
			} else {
				require.IsType(t, tracingapi.NoopTracing{}, result)
			}

			_ = result.Shutdown(context.Background())
		})
	}
}

// TestNewTracingFromEnv_ConsoleExporter tests that console exporter works.
// without requiring OTLP endpoints and doesn't make network calls.
func TestNewTracingFromEnv_ConsoleExporter(t *testing.T) {
	tests := []struct {
		name                string
		env                 map[string]string
		expectNoop          bool
		expectConsoleOutput bool
	}{
		{
			name: "console exporter without any endpoints",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER": "console",
			},
			expectConsoleOutput: true,
		},
		{
			name: "console exporter ignores OTLP endpoints",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER":               "console",
				"OTEL_EXPORTER_OTLP_ENDPOINT":        "http://should-be-ignored:4317",
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://should-be-ignored:4318",
			},
			expectConsoleOutput: true,
		},
		{
			name: "console exporter with custom service name",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER": "console",
				"OTEL_SERVICE_NAME":    "test-console-service",
			},
			expectConsoleOutput: true,
		},
		{
			name: "console exporter with sampling",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER": "console",
				"OTEL_TRACES_SAMPLER":  "always_on",
			},
			expectConsoleOutput: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			var stdout bytes.Buffer
			result, err := NewTracingFromEnv(t.Context(), &stdout, nil)
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = result.Shutdown(context.Background())
			})

			if tt.expectNoop {
				_, ok := result.(tracingapi.NoopTracing)
				require.True(t, ok, "expected NoopTracing")
				return
			}

			// Verify it's not noop.
			_, ok := result.(tracingapi.NoopTracing)
			require.False(t, ok, "expected non-noop tracing")

			// For console exporter, create a span and verify output.
			if tt.expectConsoleOutput {
				span := startCompletionsSpan(t, result, nil)
				require.NotNil(t, span, "expected span to be created")
				span.EndSpan()

				// Console exporter writes synchronously, so output should be immediate.
				output := stdout.String()
				require.Contains(t, output, "TraceID", "console output should contain TraceID")
				require.Contains(t, output, "SpanID", "console output should contain SpanID")

				// Verify service name if set.
				if serviceName := tt.env["OTEL_SERVICE_NAME"]; serviceName != "" {
					require.Contains(t, output, serviceName, "console output should contain custom service name")
				}
			}
		})
	}
}

// TestNewTracingFromEnv_Exporter tests that the OTEL_TRACES_EXPORTER env
// variable works.
// See: https://opentelemetry.io/docs/languages/sdk-configuration/general/#otel_traces_exporter
func TestNewTracingFromEnv_Exporter(t *testing.T) {
	// Just test 2 exporters to prove the SDK is wired up correctly.
	for _, exporter := range []string{"console", "otlp"} {
		t.Run(exporter, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OTEL_TRACES_EXPORTER", exporter)

			var stdout bytes.Buffer
			collector, tracing := newTracingFromEnvForTest(t, &stdout)

			// Create a test request to start a span.
			span := startCompletionsSpan(t, tracing, nil)
			require.NotNil(t, span, "expected span to be sampled")
			span.EndSpan()

			// Now, verify the actual ENV were honored.
			v1Span := collector.TakeSpan()
			switch exporter {
			case "otlp":
				require.NotNil(t, v1Span)
				require.Empty(t, stdout)
			case "console":
				require.Nil(t, v1Span)
				require.Contains(t, stdout.String(), "TraceID")
			}
		})
	}
}

// TestNewTracingFromEnv_TracesSampler tests that the OTEL_TRACES_SAMPLER env
// variable works.
// See: https://opentelemetry.io/docs/languages/sdk-configuration/general/#otel_traces_sampler
func TestNewTracingFromEnv_TracesSampler(t *testing.T) {
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
			clearEnv(t)
			t.Setenv("OTEL_TRACES_SAMPLER", tt.sampler)
			collector, tracing := newTracingFromEnvForTest(t, io.Discard)

			span := startCompletionsSpan(t, tracing, nil)
			if tt.expectSampled {
				require.NotNil(t, span, "expected span to be sampled")
				span.EndSpan()
			} else {
				require.Nil(t, span, "expected span to not be sampled")
			}

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

// TestNewTracingFromEnv_OtelPropagators tests that the OTEL_PROPAGATORS env
// variable works.
// See: https://opentelemetry.io/docs/languages/sdk-configuration/general/#otel_propagators
func TestNewTracingFromEnv_OtelPropagators(t *testing.T) {
	// Just test 2 propagators to prove the SDK is wired up correctly.
	tests := []struct {
		propagator         string
		expectHeaderKey    string
		expectHeaderFormat func(string, string) string
	}{
		{
			propagator:         "b3",
			expectHeaderKey:    "b3",
			expectHeaderFormat: func(traceID, spanID string) string { return traceID + "-" + spanID + "-1" },
		},
		{
			propagator:         "tracecontext",
			expectHeaderKey:    "traceparent",
			expectHeaderFormat: func(traceID, spanID string) string { return "00-" + traceID + "-" + spanID + "-01" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.propagator, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("OTEL_PROPAGATORS", tt.propagator)
			collector, tracing := newTracingFromEnvForTest(t, io.Discard)

			// Create headers for injection.
			carrier := propagation.MapCarrier{}

			// Start span and inject headers.
			span := startCompletionsSpan(t, tracing, carrier)
			require.NotNil(t, span)
			span.EndSpan()

			// Check that the expected header was injected.
			require.Len(t, carrier, 1, "expected exactly one header to be set")
			require.Contains(t, carrier, tt.expectHeaderKey)

			// Get the span to check trace/span IDs.
			v1Span := collector.TakeSpan()
			require.NotNil(t, v1Span)

			// Convert IDs to hex strings.
			traceIDStr := fmt.Sprintf("%032x", v1Span.TraceId)
			spanIDStr := fmt.Sprintf("%016x", v1Span.SpanId)

			// Verify the header value format.
			expectedValue := tt.expectHeaderFormat(traceIDStr, spanIDStr)
			require.Equal(t, expectedValue, carrier[tt.expectHeaderKey])
		})
	}
}

// TestNewTracingFromEnv_ChatCompletion_Redaction tests that the OpenInference.
// environment variables (OPENINFERENCE_HIDE_INPUTS and OPENINFERENCE_HIDE_OUTPUTS)
// work correctly to redact sensitive data from spans, following the OpenInference.
// configuration specification.
func TestNewTracingFromEnv_ChatCompletion_Redaction(t *testing.T) {
	tests := []struct {
		name        string
		hideInputs  bool
		hideOutputs bool
	}{
		{
			name:        "no redaction",
			hideInputs:  false,
			hideOutputs: false,
		},
		{
			name:        "hide inputs only",
			hideInputs:  true,
			hideOutputs: false,
		},
		{
			name:        "hide outputs only",
			hideInputs:  false,
			hideOutputs: true,
		},
		{
			name:        "hide inputs and outputs",
			hideInputs:  true,
			hideOutputs: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(openinference.EnvHideInputs, strconv.FormatBool(tt.hideInputs))
			t.Setenv(openinference.EnvHideOutputs, strconv.FormatBool(tt.hideOutputs))

			collector, tr := newTracingFromEnvForTest(t, io.Discard)
			tracer := tr.ChatCompletionTracer()

			// Create a test request with sensitive data.
			req := &openai.ChatCompletionRequest{
				Model: openai.ModelGPT5Nano,
				Messages: []openai.ChatCompletionMessageParamUnion{{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.StringOrUserRoleContentUnion{
							Value: "Hello, sensitive data!",
						},
						Role: openai.ChatMessageRoleUser,
					},
				}},
			}
			reqBody := []byte(`{"model":"gpt-4.1-nano","messages":[{"role":"user","content":"Hello, sensitive data!"}]}`)
			respBody := &openai.ChatCompletionResponse{
				ID:     "chatcmpl-abc123",
				Object: "chat.completion",
				Choices: []openai.ChatCompletionResponseChoice{{
					Message: openai.ChatCompletionResponseChoiceMessage{
						Role:    "assistant",
						Content: ptr.To("Response with sensitive data"),
					},
				}},
			}

			// Start a span and record request/response.
			span := tracer.StartSpanAndInjectHeaders(
				t.Context(),
				map[string]string{},
				propagation.MapCarrier{},
				req,
				reqBody,
			)
			require.NotNil(t, span)
			span.RecordResponse(respBody)
			span.EndSpan()

			// Check the recorded span.
			v1Span := collector.TakeSpan()
			require.NotNil(t, v1Span)

			// Check if inputs/outputs are redacted as expected.
			attrs := make(map[string]string)
			for _, kv := range v1Span.Attributes {
				attrs[kv.Key] = kv.Value.GetStringValue()
			}

			// Check input redaction.
			if tt.hideInputs {
				require.Equal(t, openinference.RedactedValue, attrs[openinference.InputValue])
				_, hasInputMessage := attrs[openinference.InputMessageAttribute(0, openinference.MessageContent)]
				require.False(t, hasInputMessage)
			} else {
				require.Contains(t, attrs[openinference.InputValue], "Hello, sensitive data!")
				require.Contains(t, attrs[openinference.InputValue], attrs[openinference.InputMessageAttribute(0, openinference.MessageContent)])
			}

			// Check output redaction.
			if tt.hideOutputs {
				require.Equal(t, openinference.RedactedValue, attrs[openinference.OutputValue])
				_, hasOutputMessage := attrs[openinference.OutputMessageAttribute(0, openinference.MessageContent)]
				require.False(t, hasOutputMessage)
			} else {
				require.Contains(t, attrs[openinference.OutputValue], "Response with sensitive data")
				require.Contains(t, attrs[openinference.OutputValue], attrs[openinference.OutputMessageAttribute(0, openinference.MessageContent)])
			}
		})
	}
}

func newTracingFromEnvForTest(t *testing.T, stdout io.Writer) (*testotel.OTLPCollector, tracingapi.Tracing) {
	collector := testotel.StartOTLPCollector()
	t.Cleanup(collector.Close)
	collector.SetEnv(t.Setenv)

	result, err := NewTracingFromEnv(t.Context(), stdout, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = result.Shutdown(context.Background())
	})

	return collector, result
}

// TestNewTracingFromEnv_OTLPHeaders tests that OTEL_EXPORTER_OTLP_HEADERS
// is properly handled by the autoexport package.
func TestNewTracingFromEnv_OTLPHeaders(t *testing.T) {
	expectedAuthorization := "ApiKey test-key-123"
	actualAuthorization := make(chan string, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualAuthorization <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	clearEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization="+expectedAuthorization)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ts.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

	result, err := NewTracingFromEnv(t.Context(), io.Discard, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = result.Shutdown(context.Background())
	})

	// Create span to trigger export
	span := startCompletionsSpan(t, result, nil)
	require.NotNil(t, span)
	span.EndSpan()

	// Force flush
	if impl, ok := result.(*tracingImpl); ok {
		_ = impl.shutdown(t.Context())
	}

	require.Equal(t, expectedAuthorization, <-actualAuthorization)
}

// TestNewTracingFromEnv_HeaderAttributeMapping verifies that headerAttributeMapping
// passed to NewTracingFromEnv is applied by tracers to set span attributes.
func TestNewTracingFromEnv_HeaderAttributeMapping(t *testing.T) {
	collector := testotel.StartOTLPCollector()
	t.Cleanup(collector.Close)
	clearEnv(t)
	collector.SetEnv(t.Setenv)

	mapping := map[string]string{
		"x-session-id": "session.id",
		"x-user-id":    "user.id",
	}

	result, err := NewTracingFromEnv(t.Context(), io.Discard, mapping)
	require.NoError(t, err)
	t.Cleanup(func() { _ = result.Shutdown(context.Background()) })

	headers := map[string]string{
		"x-session-id": "abc123",
		"x-user-id":    "user456",
	}
	carrier := propagation.MapCarrier{}

	tr := result.ChatCompletionTracer()
	req := &openai.ChatCompletionRequest{Model: openai.ModelGPT5Nano}
	span := tr.StartSpanAndInjectHeaders(t.Context(), headers, carrier, req, []byte("{}"))
	require.NotNil(t, span)
	span.EndSpan()

	v1Span := collector.TakeSpan()
	require.NotNil(t, v1Span)

	attrs := make(map[string]string)
	for _, kv := range v1Span.Attributes {
		attrs[kv.Key] = kv.Value.GetStringValue()
	}
	require.Equal(t, "abc123", attrs["session.id"])
	require.Equal(t, "user456", attrs["user.id"])
}

// TestNewTracingFromEnv_Embeddings_Redaction tests that the OpenInference
// environment variables (OPENINFERENCE_HIDE_EMBEDDINGS_TEXT and OPENINFERENCE_HIDE_EMBEDDINGS_VECTORS)
// work correctly to redact sensitive data from embeddings spans, following the OpenInference
// configuration specification.
func TestNewTracingFromEnv_Embeddings_Redaction(t *testing.T) {
	tests := []struct {
		name                  string
		hideEmbeddingsText    bool
		hideEmbeddingsVectors bool
	}{
		{
			name:                  "no redaction",
			hideEmbeddingsText:    false,
			hideEmbeddingsVectors: false,
		},
		{
			name:                  "hide embeddings text only",
			hideEmbeddingsText:    true,
			hideEmbeddingsVectors: false,
		},
		{
			name:                  "hide embeddings vectors only",
			hideEmbeddingsText:    false,
			hideEmbeddingsVectors: true,
		},
		{
			name:                  "hide embeddings text and vectors",
			hideEmbeddingsText:    true,
			hideEmbeddingsVectors: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(openinference.EnvHideEmbeddingsText, strconv.FormatBool(tt.hideEmbeddingsText))
			t.Setenv(openinference.EnvHideEmbeddingsVectors, strconv.FormatBool(tt.hideEmbeddingsVectors))

			collector, tr := newTracingFromEnvForTest(t, io.Discard)
			tracer := tr.EmbeddingsTracer()

			// Create a test request with sensitive data.
			req := &openai.EmbeddingRequest{
				Model: "text-embedding-3-small",
				Input: openai.EmbeddingRequestInput{
					Value: "Sensitive embedding text",
				},
			}
			reqBody := []byte(`{"input":"Sensitive embedding text","model":"text-embedding-3-small"}`)
			respBody := &openai.EmbeddingResponse{
				Object: "list",
				Model:  "text-embedding-3-small",
				Data: []openai.Embedding{{
					Object: "embedding",
					Index:  0,
					Embedding: openai.EmbeddingUnion{
						Value: []float64{0.1, 0.2, 0.3, 0.4, 0.5},
					},
				}},
				Usage: openai.EmbeddingUsage{
					PromptTokens: 5,
					TotalTokens:  5,
				},
			}

			// Start a span and record request/response.
			span := tracer.StartSpanAndInjectHeaders(
				t.Context(),
				map[string]string{},
				propagation.MapCarrier{},
				req,
				reqBody,
			)
			require.NotNil(t, span)
			span.RecordResponse(respBody)
			span.EndSpan()

			// Check the recorded span.
			v1Span := collector.TakeSpan()
			require.NotNil(t, v1Span)

			// Check if embeddings text/vectors are redacted as expected.
			attrs := make(map[string]string)
			hasVector := false
			for _, kv := range v1Span.Attributes {
				attrs[kv.Key] = kv.Value.GetStringValue()
				if kv.Key == openinference.EmbeddingVectorAttribute(0) {
					hasVector = true
				}
			}

			// Check embeddings text redaction.
			textAttrKey := openinference.EmbeddingTextAttribute(0)
			if tt.hideEmbeddingsText {
				_, hasText := attrs[textAttrKey]
				require.False(t, hasText, "embedding text should be hidden")
			} else {
				require.Equal(t, "Sensitive embedding text", attrs[textAttrKey], "embedding text should be present")
			}

			// Check embeddings vector redaction.
			if tt.hideEmbeddingsVectors {
				require.False(t, hasVector, "embedding vectors should be hidden")
			} else {
				require.True(t, hasVector, "embedding vectors should be present")
			}
		})
	}
}

// startCompletionsSpan is a test helper that creates a span with a basic request.
func startCompletionsSpan(t *testing.T, tracing tracingapi.Tracing, carrier propagation.MapCarrier) tracingapi.ChatCompletionSpan {
	if carrier == nil {
		carrier = propagation.MapCarrier{}
	}
	tracer := tracing.ChatCompletionTracer()
	req := &openai.ChatCompletionRequest{Model: openai.ModelGPT5Nano}
	return tracer.StartSpanAndInjectHeaders(t.Context(), nil, carrier, req, nil)
}

func TestTracingImpl_Getters_ImageGenerationAndRerank(t *testing.T) {
	ig := tracingapi.NoopTracer[openai.ImageGenerationRequest, openai.ImageGenerationResponse, struct{}]{}
	rr := tracingapi.NoopTracer[cohereschema.RerankV2Request, cohereschema.RerankV2Response, struct{}]{}

	ti := &tracingImpl{
		imageGenerationTracer: ig,
		rerankTracer:          rr,
	}

	require.Equal(t, ig, ti.ImageGenerationTracer())
	require.Equal(t, rr, ti.RerankTracer())
}
