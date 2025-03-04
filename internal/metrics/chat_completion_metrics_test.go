// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewProcessorMetrics(t *testing.T) {
	pm := NewChatCompletion(prometheus.NewRegistry()).(*chatCompletion)
	assert.NotNil(t, pm)
	assert.False(t, pm.firstTokenSent)
}

func TestStartRequest(t *testing.T) {
	pm := NewChatCompletion(prometheus.NewRegistry()).(*chatCompletion)
	before := time.Now()
	pm.StartRequest()
	after := time.Now()

	assert.False(t, pm.firstTokenSent)
	assert.GreaterOrEqual(t, pm.requestStart, before)
	assert.LessOrEqual(t, pm.requestStart, after)
}

func TestRecordTokenUsage(t *testing.T) {
	pm := NewChatCompletion(prometheus.NewRegistry()).(*chatCompletion)
	pm.SetModel("test-model")
	pm.SetBackend(filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})

	pm.RecordTokenUsage(10, 5, 15)

	// Get the current value of the metrics.
	input, _ := getHistogramValue(t, pm.metrics.tokenUsage, map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.system":         "openai",
		"gen_ai.request.model":  "test-model",
		"gen_ai.response.model": "test-model",
		"gen_ai.token.type":     "input",
	})
	output, _ := getHistogramValue(t, pm.metrics.tokenUsage, map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.system":         "openai",
		"gen_ai.request.model":  "test-model",
		"gen_ai.response.model": "test-model",
		"gen_ai.token.type":     "output",
	})
	total, _ := getHistogramValue(t, pm.metrics.tokenUsage, map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.system":         "openai",
		"gen_ai.request.model":  "test-model",
		"gen_ai.response.model": "test-model",
		"gen_ai.token.type":     "total",
	})

	assert.Equal(t, float64(10), input)
	assert.Equal(t, float64(5), output)
	assert.Equal(t, float64(15), total)
}

func TestRecordTokenLatency(t *testing.T) {
	pm := NewChatCompletion(prometheus.NewRegistry()).(*chatCompletion)
	pm.StartRequest()
	pm.SetModel("test-model")
	pm.SetBackend(filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}})

	// Test first token.
	time.Sleep(10 * time.Millisecond)
	pm.RecordTokenLatency(1)
	assert.True(t, pm.firstTokenSent)

	firstTokenLatency, count := getHistogramValue(t, pm.metrics.firstTokenLatency, map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.system":         "aws.bedrock",
		"gen_ai.request.model":  "test-model",
		"gen_ai.response.model": "test-model",
	})
	assert.Greater(t, firstTokenLatency, 0.0)
	require.Equal(t, uint64(1), count)

	// Test subsequent tokens.
	time.Sleep(10 * time.Millisecond)
	pm.RecordTokenLatency(5)

	outputTokenLatency, count := getHistogramValue(t, pm.metrics.outputTokenLatency, map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.system":         "aws.bedrock",
		"gen_ai.request.model":  "test-model",
		"gen_ai.response.model": "test-model",
	})
	assert.Greater(t, outputTokenLatency, 0.0)
	require.Equal(t, uint64(1), count)

	// Test zero tokens case.
	time.Sleep(10 * time.Millisecond)
	pm.RecordTokenLatency(0)

	outputTokenLatency, count = getHistogramValue(t, pm.metrics.outputTokenLatency, map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.system":         "aws.bedrock",
		"gen_ai.request.model":  "test-model",
		"gen_ai.response.model": "test-model",
	})
	assert.Greater(t, outputTokenLatency, 0.0)
	require.Equal(t, uint64(1), count)
}

func TestRecordRequestCompletion(t *testing.T) {
	pm := NewChatCompletion(prometheus.NewRegistry()).(*chatCompletion)
	pm.StartRequest()
	pm.SetModel("test-model")
	pm.SetBackend(filterapi.Backend{Name: "custom"})

	time.Sleep(10 * time.Millisecond)
	pm.RecordRequestCompletion(true)

	// Test total latency histogram.
	totalLatency, count := getHistogramValue(t, pm.metrics.requestLatency, map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.system":         "custom",
		"gen_ai.request.model":  "test-model",
		"gen_ai.response.model": "test-model",
		"error.type":            "",
	})
	assert.Greater(t, totalLatency, 0.0)
	require.Equal(t, uint64(1), count)

	// Test some failed requests.
	pm.RecordRequestCompletion(false)
	pm.RecordRequestCompletion(false)
	failedRequests, count := getHistogramValue(t, pm.metrics.requestLatency, map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.system":         "custom",
		"gen_ai.request.model":  "test-model",
		"gen_ai.response.model": "test-model",
		"error.type":            "_OTHER",
	})
	assert.Greater(t, failedRequests, 0.0)
	require.Equal(t, uint64(2), count)
}

// Helper function to get the current sum of a histogram metric.
func getHistogramValue(t *testing.T, metric *prometheus.HistogramVec, labels map[string]string) (float64, uint64) {
	t.Helper()
	m, err := metric.GetMetricWith(labels)
	assert.NoError(t, err, "Error getting metric")

	metricpb := &dto.Metric{}
	assert.NoError(t, m.(prometheus.Metric).Write(metricpb), "Error writing metric")
	return metricpb.Histogram.GetSampleSum(), metricpb.Histogram.GetSampleCount()
}
