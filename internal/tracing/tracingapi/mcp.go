// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracingapi

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPTracer creates spans for MCP requests.
type MCPTracer interface {
	// StartSpanAndInjectMeta starts a span and injects trace context into
	// the _meta mutation.
	//
	// Parameters:
	//   - ctx: might include a parent span context.
	//   - req: Incoming MCP request message.
	//   - param: Incoming MCP parameter used to extract parent trace context.
	//   - headers: Request HTTP request headers.
	//
	// Returns nil unless the span is sampled.
	StartSpanAndInjectMeta(ctx context.Context, req *jsonrpc.Request, param mcp.Params, headers http.Header) MCPSpan
}

// MCPSpan represents an MCP span.
type MCPSpan interface {
	// RecordRouteToBackend records the backend that was routed to.
	RecordRouteToBackend(backend string, session string, isNew bool)
	// EndSpan finalizes and ends the span.
	EndSpan()
	// EndSpanOnError finalizes and ends the span with an error status.
	EndSpanOnError(errType string, err error)
}

// Ensure NoopMCPTracer implements [MCPTracer].
var _ MCPTracer = NoopMCPTracer{}

// NoopMCPTracer is a no-op implementation of [MCPTracer].
type NoopMCPTracer struct{}

// StartSpanAndInjectMeta implements [MCPTracer.StartSpanAndInjectMeta].
func (NoopMCPTracer) StartSpanAndInjectMeta(context.Context, *jsonrpc.Request, mcp.Params, http.Header) MCPSpan {
	return nil
}
