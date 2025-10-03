// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

func TestNewMCP(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter)
	require.NotNil(t, m)
}

func TestRecordRequestDuration(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter)
	require.NotNil(t, m)
	startAt := time.Now().Add(-1 * time.Minute)
	m.RecordRequestDuration(t.Context(), &startAt)

	count, sum := testotel.GetHistogramValues(t, mr, mcpRequestDuration, attribute.NewSet())
	require.Equal(t, uint64(1), count)
	require.Equal(t, 60, int(sum))
}

func TestRecordRequestErrorDuration(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter)
	require.NotNil(t, m)
	startAt := time.Now().Add(-30 * time.Second)
	m.RecordRequestErrorDuration(t.Context(), &startAt, MCPErrorUnsupportedProtocolVersion)

	count, sum := testotel.GetHistogramValues(t, mr, mcpRequestDuration, attribute.NewSet(
		attribute.Key(mcpAttributeErrorType).String(string(MCPErrorUnsupportedProtocolVersion)),
	))
	require.Equal(t, uint64(1), count)
	require.Equal(t, 30, int(sum))
}

func TestRecordMethodCount(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter)
	require.NotNil(t, m)

	m.RecordMethodCount(t.Context(), "test_method_name")
	attrs := attribute.NewSet(
		attribute.Key(mcpAttributeMethodName).String("test_method_name"),
		attribute.Key(mcpAttributeStatusName).String(string(mcpStatusSuccess)),
	)
	val := testotel.GetCounterValue(t, mr, mcpMethodCount, attrs)
	require.Equal(t, float64(1), val)

	m.RecordMethodErrorCount(t.Context())
	attrs = attribute.NewSet(
		attribute.Key(mcpAttributeStatusName).String(string(mcpStatusError)),
	)
	val = testotel.GetCounterValue(t, mr, mcpMethodCount, attrs)
	require.Equal(t, float64(1), val)
}

func TestRecordInitializationDuration(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter)
	require.NotNil(t, m)

	startAt := time.Now().Add(-45 * time.Second)
	m.RecordInitializationDuration(t.Context(), &startAt)

	count, sum := testotel.GetHistogramValues(t, mr, mcpInitializationDuration, attribute.NewSet())
	require.Equal(t, uint64(1), count)
	require.Equal(t, 45, int(sum))
}

func TestRecordCapabilitiesNegotiated(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter)
	require.NotNil(t, m)

	m.RecordClientCapabilities(t.Context(), &mcpsdk.ClientCapabilities{
		Experimental: map[string]any{
			"exp1": struct{}{},
			"exp2": struct{}{},
		},
		Roots: struct {
			ListChanged bool "json:\"listChanged,omitempty\""
		}{ListChanged: true},
		Sampling: &mcpsdk.SamplingCapabilities{},
	})
	m.RecordServerCapabilities(t.Context(), &mcpsdk.ServerCapabilities{
		Experimental: map[string]any{
			"exp1": struct{}{},
		},
		Completions: &mcpsdk.CompletionCapabilities{},
		Logging:     &mcpsdk.LoggingCapabilities{},
		Prompts:     &mcpsdk.PromptCapabilities{ListChanged: true},
		Resources:   &mcpsdk.ResourceCapabilities{ListChanged: true, Subscribe: true},
		Tools:       &mcpsdk.ToolCapabilities{ListChanged: true},
	})
	require.Equal(t, float64(2), testotel.GetCounterValue(t, mr, mcpCapabilitiesNegotiated, attribute.NewSet(
		attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeExperimental)),
		attribute.Key(mcpAttributeCapabilitySide).String(string(mcpCapabilitySideClient)),
	)))
	require.Equal(t, float64(1), testotel.GetCounterValue(t, mr, mcpCapabilitiesNegotiated, attribute.NewSet(
		attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeRoots)),
		attribute.Key(mcpAttributeCapabilitySide).String(string(mcpCapabilitySideClient)),
	)))

	require.Equal(t, float64(1), testotel.GetCounterValue(t, mr, mcpCapabilitiesNegotiated, attribute.NewSet(
		attribute.Key(mcpAttributeCapabilityType).String(string(mcpCapabilityTypeSampling)),
		attribute.Key(mcpAttributeCapabilitySide).String(string(mcpCapabilitySideClient)),
	)))

	for _, serverCapability := range []mcpCapabilityType{
		mcpCapabilityTypeExperimental,
		mcpCapabilityTypeCompletions,
		mcpCapabilityTypePrompts,
		mcpCapabilityTypeResources,
		mcpCapabilityTypeTools,
	} {
		require.Equal(t, float64(1), testotel.GetCounterValue(t, mr, mcpCapabilitiesNegotiated, attribute.NewSet(
			attribute.Key(mcpAttributeCapabilityType).String(string(serverCapability)),
			attribute.Key(mcpAttributeCapabilitySide).String(string(mcpCapabilitySideServer)),
		)))
	}
}

func TestRecordProgressNotifications(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter)
	require.NotNil(t, m)

	m.RecordProgress(t.Context())
	val := testotel.GetCounterValue(t, mr, mpcProgressNotifications, attribute.NewSet())
	require.Equal(t, float64(1), val)

	m.RecordProgress(t.Context())
	val = testotel.GetCounterValue(t, mr, mpcProgressNotifications, attribute.NewSet())
	require.Equal(t, float64(2), val)
}
