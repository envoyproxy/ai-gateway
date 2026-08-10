// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
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

func TestTracer_StartSpanAndInjectMeta_ParentTraceContext(t *testing.T) {
	const (
		headerTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
		headerSpanID  = "00f067aa0ba902b7"
		metaTraceID   = "0af7651916cd43dd8448eb211c80319c"
		metaSpanID    = "b7ad6b7169203331"
	)
	traceparent := func(traceID, spanID string) string {
		return "00-" + traceID + "-" + spanID + "-01"
	}
	meta := func(traceID, spanID string) map[string]any {
		return map[string]any{"traceparent": traceparent(traceID, spanID)}
	}

	cases := []struct {
		name            string
		headers         http.Header
		meta            map[string]any
		expectedTraceID string
		expectedSpanID  string
	}{
		{
			name:            "header only becomes the parent",
			headers:         http.Header{"Traceparent": []string{traceparent(headerTraceID, headerSpanID)}},
			expectedTraceID: headerTraceID,
			expectedSpanID:  headerSpanID,
		},
		{
			name:            "meta only becomes the parent",
			meta:            meta(metaTraceID, metaSpanID),
			expectedTraceID: metaTraceID,
			expectedSpanID:  metaSpanID,
		},
		{
			name:            "meta wins when both are present",
			headers:         http.Header{"Traceparent": []string{traceparent(headerTraceID, headerSpanID)}},
			meta:            meta(metaTraceID, metaSpanID),
			expectedTraceID: metaTraceID,
			expectedSpanID:  metaSpanID,
		},
		{
			name:    "invalid header traceparent is ignored",
			headers: http.Header{"Traceparent": []string{"not-a-traceparent"}},
		},
		{
			name: "no trace context anywhere starts a root span",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
			tracer := newMCPTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator(), nil)

			reqID, _ := jsonrpc.MakeID("id")
			span := tracer.StartSpanAndInjectMeta(t.Context(),
				&jsonrpc.Request{ID: reqID, Method: "initialize"},
				&mcp.InitializeParams{Meta: tc.meta}, tc.headers)
			require.NotNil(t, span)
			span.EndSpan()

			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			if tc.expectedTraceID == "" {
				require.False(t, spans[0].Parent.IsValid(), "span must be a trace root")
				return
			}
			require.Equal(t, tc.expectedTraceID, spans[0].SpanContext.TraceID().String())
			require.Equal(t, tc.expectedSpanID, spans[0].Parent.SpanID().String())
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
				attribute.String("mcp.tool.name", "fake-tool"),
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
				attribute.String("mcp.prompt.name", "fake-prompt"),
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

func Test_getSpanName(t *testing.T) {
	tests := []struct {
		method   string
		expected string
	}{
		{method: "initialize", expected: "Initialize"},
		{method: "tools/list", expected: "ListTools"},
		{method: "tools/call", expected: "CallTool"},
		{method: "prompts/list", expected: "ListPrompts"},
		{method: "prompts/get", expected: "GetPrompt"},
		{method: "resources/list", expected: "ListResources"},
		{method: "resources/read", expected: "ReadResource"},
		{method: "resources/subscribe", expected: "Subscribe"},
		{method: "resources/unsubscribe", expected: "Unsubscribe"},
		{method: "resources/templates/list", expected: "ListResourceTemplates"},
		{method: "logging/setLevel", expected: "SetLoggingLevel"},
		{method: "completion/complete", expected: "Complete"},
		{method: "ping", expected: "Ping"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			actual := getSpanName(tt.method)
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
			expectedSpanName: "ListTools",
		},
		{
			name:             "tools/call",
			method:           "tools/call",
			params:           &mcp.CallToolParams{Name: "test-tool"},
			expectedSpanName: "CallTool",
		},
		{
			name:             "prompts/list",
			method:           "prompts/list",
			params:           &mcp.ListPromptsParams{},
			expectedSpanName: "ListPrompts",
		},
		{
			name:             "prompts/get",
			method:           "prompts/get",
			params:           &mcp.GetPromptParams{Name: "test-prompt"},
			expectedSpanName: "GetPrompt",
		},
		{
			name:             "resources/list",
			method:           "resources/list",
			params:           &mcp.ListResourcesParams{},
			expectedSpanName: "ListResources",
		},
		{
			name:             "resources/read",
			method:           "resources/read",
			params:           &mcp.ReadResourceParams{URI: "test://uri"},
			expectedSpanName: "ReadResource",
		},
		{
			name:             "initialize",
			method:           "initialize",
			params:           &mcp.InitializeParams{},
			expectedSpanName: "Initialize",
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
