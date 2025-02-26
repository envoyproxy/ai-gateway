// Copyright Envoy AI Gateway Authors.
// SPDX-License-Identifier: Apache-2.0.
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

func TestNewProcessorMetrics(t *testing.T) {
	pm := NewProcessorMetrics()
	assert.NotNil(t, pm)
	assert.NotNil(t, pm.metrics)
	assert.False(t, pm.firstTokenSent)
}

func TestStartRequest(t *testing.T) {
	pm := NewProcessorMetrics()
	before := time.Now()
	pm.StartRequest()
	after := time.Now()

	assert.False(t, pm.firstTokenSent)
	assert.True(t, pm.requestStart.After(before) || pm.requestStart.Equal(before))
	assert.True(t, pm.requestStart.Before(after) || pm.requestStart.Equal(after))
}

func TestUpdateTokenMetrics(t *testing.T) {
	// Reset the default registry to avoid conflicts with other tests.
	prometheus.DefaultRegisterer = prometheus.NewRegistry()

	pm := NewProcessorMetrics()
	pm.UpdateTokenMetrics("test-backend", "test-model", 10, 5, 15)

	// Get the current value of the metrics.
	completion := getCounterValue(t, pm.metrics.TokensTotal, map[string]string{
		"backend": "test-backend",
		"model":   "test-model",
		"type":    "completion",
	})
	prompt := getCounterValue(t, pm.metrics.TokensTotal, map[string]string{
		"backend": "test-backend",
		"model":   "test-model",
		"type":    "prompt",
	})
	total := getCounterValue(t, pm.metrics.TokensTotal, map[string]string{
		"backend": "test-backend",
		"model":   "test-model",
		"type":    "total",
	})

	assert.Equal(t, float64(10), completion)
	assert.Equal(t, float64(5), prompt)
	assert.Equal(t, float64(15), total)
}

func TestUpdateLatencyMetrics(t *testing.T) {
	// Reset the default registry to avoid conflicts with other tests.
	prometheus.DefaultRegisterer = prometheus.NewRegistry()

	pm := NewProcessorMetrics()
	pm.StartRequest()

	// Test first token.
	time.Sleep(10 * time.Millisecond)
	pm.UpdateLatencyMetrics("test-backend", "test-model", 1)
	assert.True(t, pm.firstTokenSent)

	firstTokenLatency := getHistogramValue(t, pm.metrics.FirstTokenLatency, map[string]string{
		"backend": "test-backend",
		"model":   "test-model",
	})
	assert.Greater(t, firstTokenLatency, 0.0)

	// Test subsequent tokens.
	time.Sleep(10 * time.Millisecond)
	pm.UpdateLatencyMetrics("test-backend", "test-model", 2)

	interTokenLatency := getHistogramValue(t, pm.metrics.InterTokenLatency, map[string]string{
		"backend": "test-backend",
		"model":   "test-model",
	})
	assert.Greater(t, interTokenLatency, 0.0)

	// Test zero tokens case.
	time.Sleep(10 * time.Millisecond)
	pm.UpdateLatencyMetrics("test-backend", "test-model", 0)
	assert.True(t, pm.firstTokenSent)
}

func TestRecordRequestCompletion(t *testing.T) {
	// Reset the default registry to avoid conflicts with other tests.
	prometheus.DefaultRegisterer = prometheus.NewRegistry()

	pm := NewProcessorMetrics()
	pm.StartRequest()

	time.Sleep(10 * time.Millisecond)
	pm.RecordRequestCompletion("test-backend", "test-model", true)

	// Test requests counter.
	requests := getCounterValue(t, pm.metrics.RequestsTotal, map[string]string{
		"backend": "test-backend",
		"model":   "test-model",
		"status":  requestStatusSuccess,
	})
	assert.Equal(t, float64(1), requests)

	// Test total latency histogram.
	totalLatency := getHistogramValue(t, pm.metrics.TotalLatency, map[string]string{
		"backend": "test-backend",
		"model":   "test-model",
		"status":  requestStatusSuccess,
	})
	assert.Greater(t, totalLatency, 0.0)

	// Test failed request.
	pm.RecordRequestCompletion("test-backend", "test-model", false)
	failedRequests := getCounterValue(t, pm.metrics.RequestsTotal, map[string]string{
		"backend": "test-backend",
		"model":   "test-model",
		"status":  requestStatusError,
	})
	assert.Equal(t, float64(1), failedRequests)
}

// Helper function to get the current value of a counter metric.
func getCounterValue(t *testing.T, metric *prometheus.CounterVec, labels map[string]string) float64 {
	t.Helper()
	m, err := metric.GetMetricWith(labels)
	assert.NoError(t, err, "Error getting metric")

	metric_pb := &dto.Metric{}
	assert.NoError(t, m.(prometheus.Metric).Write(metric_pb), "Error writing metric")
	return metric_pb.Counter.GetValue()
}

// Helper function to get the current sum of a histogram metric.
func getHistogramValue(t *testing.T, metric *prometheus.HistogramVec, labels map[string]string) float64 {
	t.Helper()
	m, err := metric.GetMetricWith(labels)
	assert.NoError(t, err, "Error getting metric")

	metric_pb := &dto.Metric{}
	assert.NoError(t, m.(prometheus.Metric).Write(metric_pb), "Error writing metric")
	return metric_pb.Histogram.GetSampleSum()
}
