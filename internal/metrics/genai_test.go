// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestNewGenAI(t *testing.T) {
	// Setup OTel SDK with a manual reader
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := provider.Meter("test.ai.gateway")

	// Create the GenAI metrics struct
	g := newGenAI(meter)
	require.NotNil(t, g)
	require.NotNil(t, g.tokenUsage)
	require.NotNil(t, g.requestLatency)
	require.NotNil(t, g.firstTokenLatency)
	require.NotNil(t, g.outputTokenLatency)

	// Since instruments often don't show up in Collect unless they have data,
	// and we are in the same package, we can record some values to verify registration.
	ctx := context.Background()
	g.tokenUsage.Record(ctx, 100)
	g.requestLatency.Record(ctx, 1.5)
	g.firstTokenLatency.Record(ctx, 0.5)
	g.outputTokenLatency.Record(ctx, 0.1)

	// Collect metrics
	var rm metricdata.ResourceMetrics
	err := reader.Collect(ctx, &rm)
	require.NoError(t, err)

	// Verify scope metrics exist
	require.NotEmpty(t, rm.ScopeMetrics)
	scopeMetrics := rm.ScopeMetrics[0]
	assert.Equal(t, "test.ai.gateway", scopeMetrics.Scope.Name)

	// Map metrics by name for easy lookup
	metricMap := make(map[string]metricdata.Metrics)
	for _, m := range scopeMetrics.Metrics {
		metricMap[m.Name] = m
	}

	// 1. Verify Token Usage Metric
	tokenUsage, exists := metricMap[genaiMetricClientTokenUsage]
	require.True(t, exists, "Expected metric %s", genaiMetricClientTokenUsage)
	assert.Equal(t, "token", tokenUsage.Unit)
	assert.Equal(t, "Number of tokens processed.", tokenUsage.Description)
	// Verify histogram data points
	histToken, ok := tokenUsage.Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	assert.NotEmpty(t, histToken.DataPoints)
	assert.Equal(t, float64(100), histToken.DataPoints[0].Sum)

	// 2. Verify Request Latency Metric
	reqLatency, exists := metricMap[genaiMetricServerRequestDuration]
	require.True(t, exists, "Expected metric %s", genaiMetricServerRequestDuration)
	assert.Equal(t, "s", reqLatency.Unit)
	assert.Equal(t, "Generative AI server request duration such as time-to-last byte or last output token.", reqLatency.Description)

	// 3. Verify First Token Latency Metric
	firstToken, exists := metricMap[genaiMetricServerTimeToFirstToken]
	require.True(t, exists, "Expected metric %s", genaiMetricServerTimeToFirstToken)
	assert.Equal(t, "s", firstToken.Unit)
	assert.Equal(t, "Time to receive first token in streaming responses.", firstToken.Description)

	// 4. Verify Output Token Latency Metric
	outputToken, exists := metricMap[genaiMetricServerTimePerOutputToken]
	require.True(t, exists, "Expected metric %s", genaiMetricServerTimePerOutputToken)
	assert.Equal(t, "s", outputToken.Unit)
	assert.Equal(t, "Time per output token generated after the first token for successful responses.", outputToken.Description)
}

func TestGenAiConstants(t *testing.T) {
	// Consistency check ensuring nobody accidentally changes standard semantic conventions
	assert.Equal(t, "gen_ai.client.token.usage", genaiMetricClientTokenUsage)
	assert.Equal(t, "gen_ai.server.request.duration", genaiMetricServerRequestDuration)
	assert.Equal(t, "gen_ai.server.time_to_first_token", genaiMetricServerTimeToFirstToken)
	assert.Equal(t, "gen_ai.server.time_per_output_token", genaiMetricServerTimePerOutputToken)

	assert.Equal(t, "openai", genaiProviderOpenAI)
	assert.Equal(t, "azure.openai", genaiProviderAzureOpenAI)
	assert.Equal(t, "aws.bedrock", genaiProviderAWSBedrock)
	assert.Equal(t, "aws.anthropic", genaiProviderAWSAnthropic)
	assert.Equal(t, "gcp.vertex_ai", genaiProviderGCPVertexAI)
	assert.Equal(t, "gcp.anthropic", genaiProviderGCPAnthropic)
	assert.Equal(t, "anthropic", genaiProviderAnthropic)
	assert.Equal(t, "cohere", genaiProviderCohere)
}
