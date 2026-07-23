// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/lang"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// Ensure mcpSpan implements [tracingapi.MCPSpan].
var _ tracingapi.MCPSpan = (*mcpSpan)(nil)

// Ensure mcpTracer implements [tracingapi.MCPTracer].
var _ tracingapi.MCPTracer = (*mcpTracer)(nil)

// mcpSpan is an implementation of [tracingapi.MCPSpan].
type mcpSpan struct {
	span trace.Span
}

// RecordRouteToBackend implements [tracingapi.MCPSpan.RecordRouteToBackend].
func (s mcpSpan) RecordRouteToBackend(backend string, sessionID string, isNew bool, serverAddr string, serverPort int) {
	// The resolved backend, session, and server peer are only known after
	// routing. Record them as span attributes for the OTel conventions
	// (mcp.session.id, server.address, server.port) in addition to the
	// gateway-specific "route to backend" event.
	attrs := []attribute.KeyValue{attribute.String("mcp.session.id", sessionID)}
	if serverAddr != "" {
		attrs = append(attrs, attribute.String("server.address", serverAddr))
	}
	if serverPort != 0 {
		attrs = append(attrs, attribute.Int("server.port", serverPort))
	}
	s.span.SetAttributes(attrs...)

	s.span.AddEvent("route to backend", trace.WithAttributes(
		attribute.String("mcp.backend.name", backend),
		attribute.String("mcp.session.id", sessionID),
		attribute.Bool("mcp.session.new", isNew),
	))
}

// EndSpanOnError implements [tracingapi.MCPSpan.EndSpanOnError].
func (s mcpSpan) EndSpanOnError(errType string, err error) {
	// error.type is the OTel span attribute for the failure class; the JSON-RPC
	// numeric code, when present, is recorded as rpc.response.status_code.
	s.span.SetAttributes(attribute.String("error.type", errType))
	var jsonrpcErr *jsonrpc.Error
	if errors.As(err, &jsonrpcErr) {
		s.span.SetAttributes(attribute.Int64("rpc.response.status_code", jsonrpcErr.Code))
	}
	s.span.AddEvent("exception", trace.WithAttributes(
		attribute.String("exception.type", errType),
		attribute.String("exception.message", err.Error()),
	))
	s.span.SetStatus(codes.Error, err.Error())
	s.span.End()
}

// EndSpan implements [tracingapi.MCPSpan.EndSpan].
func (s mcpSpan) EndSpan() {
	s.span.SetStatus(codes.Ok, "")
	s.span.End()
}

// mcpTracer is an implementation of [tracingapi.MCPTracer].
type mcpTracer struct {
	tracer            trace.Tracer
	propagator        propagation.TextMapPropagator
	attributeMappings map[string]string
}

func newMCPTracer(tracer trace.Tracer, propagator propagation.TextMapPropagator, attributeMappings map[string]string) tracingapi.MCPTracer {
	return mcpTracer{
		tracer:            tracer,
		propagator:        propagator,
		attributeMappings: attributeMappings,
	}
}

// StartSpanAndInjectMeta implements [tracingapi.MCPTracer.StartSpanAndInjectMeta].
func (m mcpTracer) StartSpanAndInjectMeta(ctx context.Context, req *jsonrpc.Request, param mcp.Params, headers http.Header) tracingapi.MCPSpan {
	attrs := []attribute.KeyValue{
		attribute.String("mcp.protocol.version", "2025-06-18"),
		// network.transport is the OSI transport ("tcp"); network.protocol.* the
		// application protocol. The gateway forwards to a local plain-HTTP/1.1
		// listener, so the version is fixed.
		attribute.String("network.transport", "tcp"),
		attribute.String("network.protocol.name", "http"),
		attribute.String("network.protocol.version", "1.1"),
		attribute.String("jsonrpc.request.id", fmt.Sprintf("%v", req.ID)),
		attribute.String("mcp.method.name", req.Method),
	}
	attrs = append(attrs, getMCPParamsAsAttributes(param)...)

	for srcName, targetName := range m.attributeMappings {
		// Check if the attribute is present in the metadata first, as this is the common place to add custom attributes
		// in MCP requests. Fall back to headers if not found in metadata.
		// If the attribute is not found there, check if there is any custom header to map.
		if metaValue := lang.CaseInsensitiveValue(param.GetMeta(), srcName); metaValue != "" {
			attrs = append(attrs, attribute.String(targetName, metaValue))
		} else if headerValue := headers.Get(srcName); headerValue != "" { // this is case-insensitive
			attrs = append(attrs, attribute.String(targetName, headerValue))
		}
	}

	// Extract trace context from incoming meta.
	mutableMeta := param.GetMeta()
	if mutableMeta == nil {
		mutableMeta = make(map[string]any)
	}
	mc := metaMapCarrier{
		m: mutableMeta,
	}
	parentCtx := m.propagator.Extract(ctx, mc)

	// Start the span with options appropriate for the semantic convention.
	// Span name follows the OTel MCP convention: the raw method name, with the
	// tool or prompt name appended for the high-value targeted operations.
	spanName := mcpSpanName(req.Method, param)
	newCtx, span := m.tracer.Start(parentCtx, spanName, trace.WithSpanKind(trace.SpanKindClient))

	// Always inject trace context into the header mutation if provided.
	// This ensures trace propagation works even for unsampled spans.
	m.propagator.Inject(newCtx, mc)
	param.SetMeta(mc.m)

	// Only record request attributes if span is recording (sampled).
	if span.IsRecording() {
		span.SetAttributes(attrs...)
		return &mcpSpan{span: span}
	}

	return nil
}

func getMCPParamsAsAttributes(p mcp.Params) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	switch params := p.(type) {
	case *mcp.InitializeParams:
		if params.ClientInfo != nil {
			attrs = append(attrs,
				attribute.String("mcp.client.name", params.ClientInfo.Name),
				attribute.String("mcp.client.title", params.ClientInfo.Title),
				attribute.String("mcp.client.version", params.ClientInfo.Version),
			)
		}
	case *mcp.CallToolParams:
		attrs = append(attrs,
			attribute.String("gen_ai.operation.name", "execute_tool"),
			attribute.String("gen_ai.tool.name", params.Name),
		)
	case *mcp.GetPromptParams:
		attrs = append(attrs, attribute.String("gen_ai.prompt.name", params.Name))
	case *mcp.SetLoggingLevelParams:
		attrs = append(attrs, attribute.String("mcp.logging.level", string(params.Level)))
	case *mcp.ListResourcesParams:
	case *mcp.ReadResourceParams:
		attrs = append(attrs, attribute.String("mcp.resource.uri", params.URI))
	case *mcp.SubscribeParams:
		attrs = append(attrs, attribute.String("mcp.resource.uri", params.URI))
	case *mcp.UnsubscribeParams:
		attrs = append(attrs, attribute.String("mcp.resource.uri", params.URI))
	case *mcp.ProgressNotificationParams:
		if params.Progress != 0 {
			attrs = append(attrs, attribute.Float64("mcp.notifications.progress", params.Progress))
		}
		if params.ProgressToken != nil {
			attrs = append(attrs, attribute.String("mcp.notifications.progress.token", fmt.Sprintf("%v", params.ProgressToken)))
		}
		if len(params.Message) > 0 {
			attrs = append(attrs, attribute.String("mcp.notifications.progress.message", params.Message))
		}
	case *mcp.CompleteParams:
		if len(params.Argument.Name) > 0 {
			attrs = append(attrs, attribute.String("mcp.complete.argument.name", params.Argument.Name))
		}
		if len(params.Argument.Value) > 0 {
			attrs = append(attrs, attribute.String("mcp.complete.argument.value", params.Argument.Value))
		}

	}

	return attrs
}

// Ensure metaMapCarrier implements the [propagation.TextMapCarrier] interface.
var _ propagation.TextMapCarrier = metaMapCarrier{}

// metaMapCarrier adapts a map[string]any to implement the TextMapCarrier interface.
type metaMapCarrier struct {
	m map[string]any
}

// Get implements [propagation.TextMapCarrier.Get].
func (c metaMapCarrier) Get(key string) string {
	return fmt.Sprintf("%v", c.m[key])
}

// Set implements [propagation.TextMapCarrier.Set].
func (c metaMapCarrier) Set(key string, value string) {
	c.m[key] = value
}

// Keys implements [propagation.TextMapCarrier.Keys].
func (c metaMapCarrier) Keys() []string {
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}

	return keys
}

// mcpSpanName derives the span name following the OTel MCP convention: the raw
// method name, with the target appended for tools/call and prompts/get. The
// resource URI is deliberately omitted from resources/* names to keep span name
// cardinality bounded.
func mcpSpanName(method string, p mcp.Params) string {
	switch params := p.(type) {
	case *mcp.CallToolParams:
		if params.Name != "" {
			return method + " " + params.Name
		}
	case *mcp.GetPromptParams:
		if params.Name != "" {
			return method + " " + params.Name
		}
	}
	return method
}
