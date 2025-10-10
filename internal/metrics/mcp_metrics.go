// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// nolint: godot
const (
	// MCP Request Duration is histogram metric that records the duration of MCP requests.
	//
	// Dimensions:
	// - error.type
	mcpRequestDuration = "mcp.request.duration"
	// MCP Method Count is a counter metric that records the total number of MCP methods invoked.
	//
	// Dimensions:
	// - mcp.method.name
	// - status
	mcpMethodCount = "mcp.method.count"
	// MCP Initialization Duration is a histogram metric that records the duration of MCP initialization.
	mcpInitializationDuration = "mcp.initialization.duration"
	// MCP Capabilities Negotiated is a counter metric that records the total number of MCP capabilities negotiated.
	//
	// Dimensions:
	// - capability.type
	// - capability.side
	mcpCapabilitiesNegotiated = "mcp.capabilities.negotiated"
	// MCP Progress Notifications is a counter metric that records the total number of MCP progress notifications sent.
	mpcProgressNotifications = "mcp.progress.notifications"
	// MCP JSON-RPC method name attribute.
	mcpAttributeMethodName = "mcp.method.name"
	// MCP status attribute, which is either "success" or "error". See mcpStatusType for all statuses.
	mcpAttributeStatusName = "status"
	// MCP error type attribute. See MCPErrorType for all error types.
	mcpAttributeErrorType = "error.type"
	// MCP capability type, which is for example, "tools" or "resources". See mcpCapabilityType for all types.
	mcpAttributeCapabilityType = "capability.type"
	// MCP capability side, which is either "client" or "server". See mcpCapabilitySide for all sides.
	mcpAttributeCapabilitySide = "capability.side"
)

// MCPErrorType defines the type of error that occurred during an MCP request.
type MCPErrorType string

const (
	// MCPErrorUnsupportedProtocolVersion indicates that the protocol version is not supported.
	MCPErrorUnsupportedProtocolVersion MCPErrorType = "unsupported_protocol_version"
	// MCPErrorInvalidJSONRPC indicates that the JSON-RPC request is invalid.
	MCPErrorInvalidJSONRPC MCPErrorType = "invalid_json_rpc"
	// MCPErrorUnsupportedMethod indicates that the method is not supported.
	MCPErrorUnsupportedMethod MCPErrorType = "unsupported_method"
	// MCPErrorUnsupportedResponse indicates that the response is not supported.
	MCPErrorUnsupportedResponse MCPErrorType = "unsupported_response"
	// MCPErrorInvalidParam indicates that a parameter is invalid.
	MCPErrorInvalidParam MCPErrorType = "invalid_param"
	// MCPErrorInvalidSessionID indicates that the session ID is invalid.
	MCPErrorInvalidSessionID MCPErrorType = "invalid_session_id"
	// MCPErrorInternal indicates that an internal error occurred.
	MCPErrorInternal MCPErrorType = "internal_error"
)

// mcpStatusType defines the status of an MCP request.
type mcpStatusType string

const (
	mcpStatusSuccess mcpStatusType = "success"
	mcpStatusError   mcpStatusType = "error"
)

// mcpCapabilityType defines the type of capability that is negotiated between client and server.
type mcpCapabilityType string

const (
	mcpCapabilityTypeTools        mcpCapabilityType = "tools"
	mcpCapabilityTypeResources    mcpCapabilityType = "resources"
	mcpCapabilityTypePrompts      mcpCapabilityType = "prompts"
	mcpCapabilityTypeSampling     mcpCapabilityType = "sampling"
	mcpCapabilityTypeRoots        mcpCapabilityType = "roots"
	mcpCapabilityTypeExperimental mcpCapabilityType = "experimental"
	mcpCapabilityTypeElicitation  mcpCapabilityType = "elicitation"
	mcpCapabilityTypeCompletions  mcpCapabilityType = "completions"
	mcpCapabilityTypeLogging      mcpCapabilityType = "logging"
)

// mcpCapabilitySide defines whether the capability is from the client or server.
type mcpCapabilitySide string

const (
	mcpCapabilitySideClient mcpCapabilitySide = "client"
	mcpCapabilitySideServer mcpCapabilitySide = "server"
)

// MCPMetrics holds metrics for MCP.
type MCPMetrics interface {
	// RecordRequestDuration records the duration of a success MCP request.
	RecordRequestDuration(ctx context.Context, startAt *time.Time)
	// RecordRequestErrorDuration records the duration of an MCP request that resulted in an error.
	RecordRequestErrorDuration(ctx context.Context, startAt *time.Time, errType MCPErrorType)
	// RecordMethodCount records the count of method invocations.
	RecordMethodCount(ctx context.Context, methodName string)
	// RecordMethodErrorCount records the count of method invocations with error status.
	RecordMethodErrorCount(ctx context.Context)
	// RecordInitializationDuration records the duration of MCP initialization.
	RecordInitializationDuration(ctx context.Context, startAt *time.Time)
	// RecordClientCapabilities records the negotiated client capabilities.
	RecordClientCapabilities(ctx context.Context, capabilities *mcpsdk.ClientCapabilities)
	// RecordServerCapabilities records the negotiated server capabilities.
	RecordServerCapabilities(ctx context.Context, capabilities *mcpsdk.ServerCapabilities)
	// RecordProgress records a progress notification sent/received.
	RecordProgress(ctx context.Context)
}

type mcp struct {
	requestDuration        metric.Float64Histogram
	methodCount            metric.Float64Counter
	initializationDuration metric.Float64Histogram
	capabilitiesNegotiated metric.Float64Counter
	progressNotifications  metric.Float64Counter
}

// NewMCP creates a new mcp metrics instance.
func NewMCP(meter metric.Meter) MCPMetrics {
	return &mcp{
		requestDuration: mustRegisterHistogram(meter,
			mcpRequestDuration,
			metric.WithDescription("Duration of MCP requests"),
			metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10)),
		methodCount: mustRegisterCounter(
			meter,
			mcpMethodCount,
			metric.WithDescription("Total number of MCP methods invoked"),
		),
		initializationDuration: mustRegisterHistogram(meter,
			mcpInitializationDuration,
			metric.WithDescription("Duration of MCP initialization"),
			metric.WithUnit("token"),
			metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10),
		),
		capabilitiesNegotiated: mustRegisterCounter(
			meter,
			mcpCapabilitiesNegotiated,
			metric.WithDescription("Total number of MCP capabilities negotiated"),
		),
		progressNotifications: mustRegisterCounter(
			meter,
			mpcProgressNotifications,
			metric.WithDescription("Total number of MCP progress notifications sent"),
		),
	}
}

// RecordMethodCount implements [MCPMetrics.RecordMethodCount].
func (m *mcp) RecordMethodCount(ctx context.Context, methodName string) {
	if methodName == "" {
		return
	}
	m.methodCount.Add(ctx, 1,
		metric.WithAttributes(
			attribute.Key(mcpAttributeMethodName).String(methodName),
			attribute.String(mcpAttributeStatusName, string(mcpStatusSuccess)),
		))
}

// RecordMethodErrorCount implements [MCPMetrics.RecordMethodErrorCount].
func (m *mcp) RecordMethodErrorCount(ctx context.Context) {
	m.methodCount.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String(mcpAttributeStatusName, string(mcpStatusError)),
		))
}

// RecordRequestDuration implements [MCPMetrics.RecordRequestDuration].
func (m *mcp) RecordRequestDuration(ctx context.Context, startAt *time.Time) {
	if startAt == nil {
		return
	}

	duration := time.Since(*startAt).Seconds()
	m.requestDuration.Record(ctx, duration)
}

// RecordRequestErrorDuration implements [MCPMetrics.RecordRequestErrorDuration].
func (m *mcp) RecordRequestErrorDuration(ctx context.Context, startAt *time.Time, errType MCPErrorType) {
	if startAt == nil {
		return
	}

	duration := time.Since(*startAt).Seconds()
	m.requestDuration.Record(ctx, duration, metric.WithAttributes(
		attribute.Key(mcpAttributeErrorType).String(string(errType)),
	))
}

// RecordInitializationDuration implements [MCPMetrics.RecordInitializationDuration].
func (m *mcp) RecordInitializationDuration(ctx context.Context, startAt *time.Time) {
	if startAt == nil {
		return
	}
	duration := time.Since(*startAt).Seconds()
	m.initializationDuration.Record(ctx, duration)
}

// RecordProgress implements [MCPMetrics.RecordProgress].
func (m *mcp) RecordProgress(ctx context.Context) {
	m.progressNotifications.Add(ctx, 1)
}

// RecordClientCapabilities implements [MCPMetrics.RecordClientCapabilities].
func (m *mcp) RecordClientCapabilities(ctx context.Context, capabilities *mcpsdk.ClientCapabilities) {
	if capabilities == nil {
		return
	}

	side := string(mcpCapabilitySideClient)
	if l := len(capabilities.Experimental); l > 0 {
		m.capabilitiesNegotiated.Add(ctx, float64(l), metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeExperimental)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}

	if capabilities.Roots.ListChanged {
		m.capabilitiesNegotiated.Add(ctx, 1, metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeRoots)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}

	if capabilities.Sampling != nil {
		m.capabilitiesNegotiated.Add(ctx, 1, metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeSampling)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}

	if capabilities.Elicitation != nil {
		m.capabilitiesNegotiated.Add(ctx, 1, metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeElicitation)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}
}

// RecordServerCapabilities implements [MCPMetrics.RecordServerCapabilities].
func (m *mcp) RecordServerCapabilities(ctx context.Context, serverCapa *mcpsdk.ServerCapabilities) {
	if serverCapa == nil {
		return
	}

	side := string(mcpCapabilitySideServer)
	if serverCapa.Completions != nil {
		m.capabilitiesNegotiated.Add(ctx, 1, metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeCompletions)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}

	if l := len(serverCapa.Experimental); l > 0 {
		m.capabilitiesNegotiated.Add(ctx, float64(l), metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeExperimental)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}

	if serverCapa.Logging != nil {
		m.capabilitiesNegotiated.Add(ctx, 1, metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeLogging)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}

	if serverCapa.Prompts != nil {
		m.capabilitiesNegotiated.Add(ctx, 1, metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypePrompts)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}

	if serverCapa.Resources != nil {
		m.capabilitiesNegotiated.Add(ctx, 1, metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeResources)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}

	if serverCapa.Tools != nil {
		m.capabilitiesNegotiated.Add(ctx, 1, metric.WithAttributes(
			attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeTools)),
			attribute.Key(mcpAttributeCapabilitySide).String(side),
		))
	}
}
