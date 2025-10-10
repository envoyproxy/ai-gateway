// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"
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

	tracer := newMCPTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator())

	reqID, _ := jsonrpc.MakeID("id")
	r := &jsonrpc.Request{ID: reqID, Method: "initialize"}
	p := &mcp.InitializeParams{}
	span := tracer.StartSpanAndInjectMeta(t.Context(), r, p)

	require.NotNil(t, span)
	meta := p.GetMeta()
	require.NotNil(t, meta)
	require.NotNil(t, meta["traceparent"])
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

			tracer := newMCPTracer(tp.Tracer("test"), autoprop.NewTextMapPropagator())

			reqID, _ := jsonrpc.MakeID("test-id")
			req := &jsonrpc.Request{ID: reqID, Method: tt.method}

			span := tracer.StartSpanAndInjectMeta(context.Background(), req, tt.params)
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
