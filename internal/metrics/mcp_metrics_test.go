// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"net/http"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testotel"
)

func TestNewMCP(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter, nil)
	require.NotNil(t, m)
}

func TestRecordMetricWithCustomAttributes(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter, map[string]string{
		"x-tracing-enrichment-user-region": "user.region",
		"X-Session-Id":                     "session.id",
		"CustomAttr":                       "custom.attr",
	})
	require.NotNil(t, m)

	req, err := http.NewRequest("GET", "https://example.com", nil)
	require.NoError(t, err)
	req.Header.Set("X-Tracing-Enrichment-User-Region", "us-east-1") // should be included in metrics
	req.Header.Set("X-Other-Attr", "other")                         // should be ignored
	req.Header.Set("X-Session-Id", "123")                           // should be ignored as the value in the metadata takes precedence

	m = m.WithRequestAttributes(req)

	startAt := time.Now().Add(-1 * time.Minute)
	m.RecordRequestDuration(t.Context(), startAt, &mcpsdk.InitializeParams{
		Meta: map[string]any{
			"x-session-id": "sess-1234", // alphabetical order wins when multiple values match case-insensitively
			"X-SESSION-ID": "sess-4567",
			"customattr":   "custom-value1", // exact match should win over case-insensitive match
			"CustomAttr":   "custom-value2",
		},
	})

	count, sum := testotel.GetHistogramValues(t, mr, mcpRequestDuration,
		attribute.NewSet(
			attribute.String("user.region", "us-east-1"),
			attribute.String("session.id", "sess-4567"),
			attribute.String("custom.attr", "custom-value2"),
		))
	require.Equal(t, uint64(1), count)
	require.Equal(t, 60, int(sum))
}

func TestRecordRequestDuration(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter, nil)
	require.NotNil(t, m)
	startAt := time.Now().Add(-1 * time.Minute)
	m.RecordRequestDuration(t.Context(), startAt, nil)

	count, sum := testotel.GetHistogramValues(t, mr, mcpRequestDuration, attribute.NewSet())
	require.Equal(t, uint64(1), count)
	require.Equal(t, 60, int(sum))
}

func TestRecordRequestErrorDuration(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter, nil)
	require.NotNil(t, m)
	startAt := time.Now().Add(-30 * time.Second)
	m.RecordRequestErrorDuration(t.Context(), startAt, MCPErrorUnsupportedProtocolVersion, nil)

	count, sum := testotel.GetHistogramValues(t, mr, mcpRequestDuration, attribute.NewSet(
		attribute.Key(mcpAttributeErrorType).String(string(MCPErrorUnsupportedProtocolVersion)),
	))
	require.Equal(t, uint64(1), count)
	require.Equal(t, 30, int(sum))
}

func TestRecordMethodCount(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter, nil)
	require.NotNil(t, m)

	m.RecordMethodCount(t.Context(), "test_method_name", nil)
	attrs := attribute.NewSet(
		attribute.Key(mcpAttributeMethodName).String("test_method_name"),
		attribute.Key(mcpAttributeStatusName).String(string(MCPStatusSuccess)),
	)
	val := testotel.GetCounterValue(t, mr, mcpMethodCount, attrs)
	require.Equal(t, float64(1), val)

	m.RecordMethodErrorCount(t.Context(), "test_method_name", nil, MCPStatusError)
	attrs = attribute.NewSet(
		attribute.Key(mcpAttributeMethodName).String("test_method_name"),
		attribute.Key(mcpAttributeStatusName).String(string(MCPStatusError)),
	)
	val = testotel.GetCounterValue(t, mr, mcpMethodCount, attrs)
	require.Equal(t, float64(1), val)
}

func TestRecordInitializationDuration(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter, nil)
	require.NotNil(t, m)

	startAt := time.Now().Add(-45 * time.Second)
	m.RecordInitializationDuration(t.Context(), startAt, nil)

	count, sum := testotel.GetHistogramValues(t, mr, mcpInitializationDuration, attribute.NewSet())
	require.Equal(t, uint64(1), count)
	require.Equal(t, 45, int(sum))
}

func TestRecordCapabilitiesNegotiated(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

	m := NewMCP(meter, nil)
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
	}, nil)
	m.RecordServerCapabilities(t.Context(), &mcpsdk.ServerCapabilities{
		Experimental: map[string]any{
			"exp1": struct{}{},
		},
		Completions: &mcpsdk.CompletionCapabilities{},
		Logging:     &mcpsdk.LoggingCapabilities{},
		Prompts:     &mcpsdk.PromptCapabilities{ListChanged: true},
		Resources:   &mcpsdk.ResourceCapabilities{ListChanged: true, Subscribe: true},
		Tools:       &mcpsdk.ToolCapabilities{ListChanged: true},
	}, nil)
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

	m := NewMCP(meter, nil)
	require.NotNil(t, m)

	m.RecordProgress(t.Context(), nil)
	val := testotel.GetCounterValue(t, mr, mpcProgressNotifications, attribute.NewSet())
	require.Equal(t, float64(1), val)

	m.RecordProgress(t.Context(), nil)
	val = testotel.GetCounterValue(t, mr, mpcProgressNotifications, attribute.NewSet())
	require.Equal(t, float64(2), val)
}
