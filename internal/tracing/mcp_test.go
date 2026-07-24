// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

func TestTracer_StartSpanAndInjectMeta(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))

	tracer := newMCPTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(),
		map[string]string{
			"x-tracing-enrichment-user-region": "user.region",
			"agent-session-id":                 "session.id",
			"CustomAttr":                       "custom.attr",
		})

	headers := make(http.Header)
	headers.Add("X-Tracing-Enrichment-User-Region", "us-east-1")
	headers.Add("Agent-Session-Id", "123") // should be ignored as the value in the metadata takes precedence

	reqID, _ := jsonrpc.MakeID("id")
	r := &jsonrpc.Request{ID: reqID, Method: "initialize"}
	p := &mcp.InitializeParams{Meta: map[string]any{
		"Agent-Session-Id": "sess-4567", // alphabetical order wins when multiple values match case-insensitively
		"agent-session-id": "sess-1234",
		"customattr":       "custom-value1", // exact match should win over case-insensitive match
		"CustomAttr":       "custom-value2",
	}}
	span := tracer.StartSpanAndInjectMeta(t.Context(), r, p, headers)

	require.NotNil(t, span)
	meta := p.GetMeta()
	require.NotNil(t, meta)
	require.NotNil(t, meta["traceparent"])

	// End the span to export it
	span.EndSpan()
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	actualSpan := spans[0]
	require.Contains(t, actualSpan.Attributes, attribute.String("user.region", "us-east-1"))
	require.Contains(t, actualSpan.Attributes, attribute.String("session.id", "sess-4567"))
	require.Contains(t, actualSpan.Attributes, attribute.String("custom.attr", "custom-value2"))
	require.NotContains(t, actualSpan.Attributes, attribute.String("session.id", "123"))
	require.NotContains(t, actualSpan.Attributes, attribute.String("custom.attr", "custom-value1"))
}

func TestTracer_StartSpanAndInjectMeta_MetaAndHeaderFallback(t *testing.T) {
	cases := []struct {
		name     string
		meta     map[string]any
		headers  http.Header
		expected string
	}{
		{
			name:     "meta only",
			meta:     map[string]any{"agent-session-id": "meta-session"},
			expected: "meta-session",
		},
		{
			name:     "header fallback",
			headers:  http.Header{"Agent-Session-Id": []string{"header-session"}},
			expected: "header-session",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
			tracer := newMCPTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(),
				map[string]string{"agent-session-id": "session.id"})

			reqID, _ := jsonrpc.MakeID("id")
			r := &jsonrpc.Request{ID: reqID, Method: "initialize"}
			p := &mcp.InitializeParams{Meta: tc.meta}
			span := tracer.StartSpanAndInjectMeta(t.Context(), r, p, tc.headers)
			require.NotNil(t, span)
			span.EndSpan()

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			require.Contains(t, spans[0].Attributes, attribute.String("session.id", tc.expected))
		})
	}
}

func Test_getMCPAttributes(t *testing.T) {
	cases := []struct {
		p        mcp.Params
		expected []attribute.KeyValue
	}{
		{
			p: &mcp.InitializeParams{},
		},
		{
			p: &mcp.ListToolsParams{},
		},
		{
			p: &mcp.CallToolParams{
				Name: "fake-tool",
			},
			expected: []attribute.KeyValue{
				attribute.String("gen_ai.operation.name", "execute_tool"),
				attribute.String("gen_ai.tool.name", "fake-tool"),
			},
		},
		{
			p: &mcp.ListPromptsParams{},
		},
		{
			p: &mcp.GetPromptParams{
				Name: "fake-prompt",
			},
			expected: []attribute.KeyValue{
				attribute.String("gen_ai.prompt.name", "fake-prompt"),
			},
		},
		{
			p: &mcp.SetLoggingLevelParams{
				Level: "info",
			},
			expected: []attribute.KeyValue{
				attribute.String("mcp.logging.level", "info"),
			},
		},
		{
			p: &mcp.ListResourcesParams{},
		},
		{
			p: &mcp.ReadResourceParams{
				URI: "fake-uri",
			},
			expected: []attribute.KeyValue{
				attribute.String("mcp.resource.uri", "fake-uri"),
			},
		},
		{
			p: &mcp.ListResourceTemplatesParams{},
		},
		{
			p: &mcp.SubscribeParams{
				URI: "fake-uri",
			},
			expected: []attribute.KeyValue{
				attribute.String("mcp.resource.uri", "fake-uri"),
			},
		},
		{
			p: &mcp.UnsubscribeParams{
				URI: "fake-uri",
			},
			expected: []attribute.KeyValue{
				attribute.String("mcp.resource.uri", "fake-uri"),
			},
		},
		{
			p: &mcp.ProgressNotificationParams{
				Message:       "fake-message",
				Progress:      100,
				ProgressToken: "fake-token",
			},
			expected: []attribute.KeyValue{
				attribute.Float64("mcp.notifications.progress", 100),
				attribute.String("mcp.notifications.progress.token", "fake-token"),
				attribute.String("mcp.notifications.progress.message", "fake-message"),
			},
		},
		{
			p: &mcp.CompleteParams{
				Argument: mcp.CompleteParamsArgument{
					Name:  "fake-name",
					Value: "fake-value",
				},
			},
			expected: []attribute.KeyValue{
				attribute.String("mcp.complete.argument.name", "fake-name"),
				attribute.String("mcp.complete.argument.value", "fake-value"),
			},
		},
	}

	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			require.Equal(t, tc.expected, getMCPParamsAsAttributes(tc.p))
		})
	}
}

func Test_mcpSpanName(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		params   mcp.Params
		expected string
	}{
		{name: "initialize", method: "initialize", params: &mcp.InitializeParams{}, expected: "initialize"},
		{name: "tools/list", method: "tools/list", params: &mcp.ListToolsParams{}, expected: "tools/list"},
		{name: "tools/call with name", method: "tools/call", params: &mcp.CallToolParams{Name: "fake-tool"}, expected: "tools/call fake-tool"},
		{name: "tools/call without name", method: "tools/call", params: &mcp.CallToolParams{}, expected: "tools/call"},
		{name: "prompts/list", method: "prompts/list", params: &mcp.ListPromptsParams{}, expected: "prompts/list"},
		{name: "prompts/get with name", method: "prompts/get", params: &mcp.GetPromptParams{Name: "fake-prompt"}, expected: "prompts/get fake-prompt"},
		{name: "prompts/get without name", method: "prompts/get", params: &mcp.GetPromptParams{}, expected: "prompts/get"},
		{name: "resources/read omits uri", method: "resources/read", params: &mcp.ReadResourceParams{URI: "fake-uri"}, expected: "resources/read"},
		{name: "logging/setLevel", method: "logging/setLevel", params: &mcp.SetLoggingLevelParams{}, expected: "logging/setLevel"},
		{name: "completion/complete", method: "completion/complete", params: &mcp.CompleteParams{}, expected: "completion/complete"},
		{name: "ping nil params", method: "ping", params: nil, expected: "ping"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := mcpSpanName(tt.method, tt.params)
			require.Equal(t, tt.expected, actual)
		})
	}
}

func TestMCPTracer_SpanName(t *testing.T) {
	tests := []struct {
		name             string
		method           string
		params           mcp.Params
		expectedSpanName string
	}{
		{
			name:             "tools/list",
			method:           "tools/list",
			params:           &mcp.ListToolsParams{},
			expectedSpanName: "tools/list",
		},
		{
			name:             "tools/call",
			method:           "tools/call",
			params:           &mcp.CallToolParams{Name: "test-tool"},
			expectedSpanName: "tools/call test-tool",
		},
		{
			name:             "prompts/list",
			method:           "prompts/list",
			params:           &mcp.ListPromptsParams{},
			expectedSpanName: "prompts/list",
		},
		{
			name:             "prompts/get",
			method:           "prompts/get",
			params:           &mcp.GetPromptParams{Name: "test-prompt"},
			expectedSpanName: "prompts/get test-prompt",
		},
		{
			name:             "resources/list",
			method:           "resources/list",
			params:           &mcp.ListResourcesParams{},
			expectedSpanName: "resources/list",
		},
		{
			name:             "resources/read",
			method:           "resources/read",
			params:           &mcp.ReadResourceParams{URI: "test://uri"},
			expectedSpanName: "resources/read",
		},
		{
			name:             "initialize",
			method:           "initialize",
			params:           &mcp.InitializeParams{},
			expectedSpanName: "initialize",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			tp := trace.NewTracerProvider(trace.WithSyncer(exporter))

			tracer := newMCPTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(), nil)

			reqID, _ := jsonrpc.MakeID("test-id")
			req := &jsonrpc.Request{ID: reqID, Method: tt.method}

			span := tracer.StartSpanAndInjectMeta(context.Background(), req, tt.params, nil)
			require.NotNil(t, span)
			span.EndSpan()

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			actualSpan := spans[0]

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindClient, actualSpan.SpanKind)
		})
	}
}

func newTestMCPSpan(t *testing.T, method string, params mcp.Params) (tracingapi.MCPSpan, func() tracetest.SpanStub) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	tracer := newMCPTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(), nil)

	reqID, _ := jsonrpc.MakeID("test-id")
	req := &jsonrpc.Request{ID: reqID, Method: method}
	span := tracer.StartSpanAndInjectMeta(context.Background(), req, params, nil)
	require.NotNil(t, span)

	return span, func() tracetest.SpanStub {
		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		return spans[0]
	}
}

func TestMCPTracer_StaticAttributes(t *testing.T) {
	span, exported := newTestMCPSpan(t, "tools/list", &mcp.ListToolsParams{})
	span.EndSpan()

	attrs := exported().Attributes
	require.Contains(t, attrs, attribute.String("mcp.method.name", "tools/list"))
	require.Contains(t, attrs, attribute.String("mcp.protocol.version", "2025-06-18"))
	require.Contains(t, attrs, attribute.String("jsonrpc.request.id", "{test-id}"))
	require.Contains(t, attrs, attribute.String("network.transport", "tcp"))
	require.Contains(t, attrs, attribute.String("network.protocol.name", "http"))
	require.Contains(t, attrs, attribute.String("network.protocol.version", "1.1"))
	// The legacy custom keys must be gone.
	for _, a := range attrs {
		require.NotEqual(t, "mcp.transport", string(a.Key))
		require.NotEqual(t, "mcp.request.id", string(a.Key))
	}
}

func TestMCPSpan_EndSpanOnError(t *testing.T) {
	t.Run("jsonrpc error records status code", func(t *testing.T) {
		span, exported := newTestMCPSpan(t, "tools/call", &mcp.CallToolParams{Name: "fake-tool"})
		span.EndSpanOnError("invalid_param", &jsonrpc.Error{Code: -32602, Message: "invalid params"})

		stub := exported()
		require.Equal(t, codes.Error, stub.Status.Code)
		require.Contains(t, stub.Attributes, attribute.String("error.type", "invalid_param"))
		require.Contains(t, stub.Attributes, attribute.Int64("rpc.response.status_code", -32602))
		require.Contains(t, stub.Events[0].Attributes, attribute.String("exception.type", "invalid_param"))
	})

	t.Run("non-jsonrpc error omits status code", func(t *testing.T) {
		span, exported := newTestMCPSpan(t, "tools/call", &mcp.CallToolParams{Name: "fake-tool"})
		span.EndSpanOnError("internal_error", errors.New("boom"))

		stub := exported()
		require.Contains(t, stub.Attributes, attribute.String("error.type", "internal_error"))
		for _, a := range stub.Attributes {
			require.NotEqual(t, "rpc.response.status_code", string(a.Key))
		}
	})
}

func TestMCPSpan_RecordRouteToBackend(t *testing.T) {
	span, exported := newTestMCPSpan(t, "tools/call", &mcp.CallToolParams{Name: "fake-tool"})
	span.RecordRouteToBackend("backend-a", "sess-1234", true, "127.0.0.1", 9999)
	span.EndSpan()

	stub := exported()
	require.Contains(t, stub.Attributes, attribute.String("mcp.session.id", "sess-1234"))
	require.Contains(t, stub.Attributes, attribute.String("server.address", "127.0.0.1"))
	require.Contains(t, stub.Attributes, attribute.Int("server.port", 9999))

	// The gateway-specific event is still emitted.
	require.Len(t, stub.Events, 1)
	require.Equal(t, "route to backend", stub.Events[0].Name)
	require.Contains(t, stub.Events[0].Attributes, attribute.String("mcp.backend.name", "backend-a"))
	require.Contains(t, stub.Events[0].Attributes, attribute.Bool("mcp.session.new", true))
}

func TestMCPSpan_RecordRouteToBackend_UnknownPeer(t *testing.T) {
	span, exported := newTestMCPSpan(t, "tools/call", &mcp.CallToolParams{Name: "fake-tool"})
	span.RecordRouteToBackend("backend-a", "sess-1234", false, "", 0)
	span.EndSpan()

	// An unknown peer leaves server.address/server.port unrecorded.
	for _, a := range exported().Attributes {
		require.NotEqual(t, "server.address", string(a.Key))
		require.NotEqual(t, "server.port", string(a.Key))
	}
}
