// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewProcessorMetrics(t *testing.T) {
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm    = DefaultChatCompletion(meter, nil).(*chatCompletion)
	)

	assert.NotNil(t, pm)
	assert.False(t, pm.firstTokenSent)
}

func TestStartRequest(t *testing.T) {
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm    = DefaultChatCompletion(meter, nil).(*chatCompletion)
	)

	before := time.Now()
	pm.StartRequest(nil)
	after := time.Now()

	assert.False(t, pm.firstTokenSent)
	assert.GreaterOrEqual(t, pm.requestStart, before)
	assert.LessOrEqual(t, pm.requestStart, after)
}

func TestRecordTokenUsage(t *testing.T) {
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm    = DefaultChatCompletion(meter, nil).(*chatCompletion)

		extra = attribute.Key("extra").String("value")
		attrs = []attribute.KeyValue{
			attribute.Key(genaiAttributeOperationName).String(genaiOperationChat),
			attribute.Key(genaiAttributeSystemName).String(genaiSystemOpenAI),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			extra,
		}
		inputAttrs  = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)
		outputAttrs = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput))...)
		totalAttrs  = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeTotal))...)
	)

	pm.SetModel("test-model")
	pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	pm.RecordTokenUsage(t.Context(), 10, 5, 15, nil, extra)

	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 10.0, sum)

	count, sum = getHistogramValues(t, mr, genaiMetricClientTokenUsage, outputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 5.0, sum)

	count, sum = getHistogramValues(t, mr, genaiMetricClientTokenUsage, totalAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 15.0, sum)
}

func TestRecordTokenLatency(t *testing.T) {
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm    = DefaultChatCompletion(meter, nil).(*chatCompletion)

		extra = attribute.Key("extra").String("value")
		attrs = attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationChat),
			attribute.Key(genaiAttributeSystemName).String(genAISystemAWSBedrock),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			extra,
		)
	)

	pm.StartRequest(nil)
	pm.SetModel("test-model")
	pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}})

	// Test first token.
	time.Sleep(10 * time.Millisecond)
	pm.RecordTokenLatency(t.Context(), 1, nil, extra)
	assert.True(t, pm.firstTokenSent)
	count, sum := getHistogramValues(t, mr, genaiMetricServerTimeToFirstToken, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Greater(t, sum, 0.0)

	// Test subsequent tokens.
	time.Sleep(10 * time.Millisecond)
	pm.RecordTokenLatency(t.Context(), 5, nil, extra)
	count, sum = getHistogramValues(t, mr, genaiMetricServerTimePerOutputToken, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Greater(t, sum, 0.0)

	// Test zero tokens case.
	time.Sleep(10 * time.Millisecond)
	pm.RecordTokenLatency(t.Context(), 0, nil, extra)
	count, sum = getHistogramValues(t, mr, genaiMetricServerTimePerOutputToken, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Greater(t, sum, 0.0)
}

func TestRecordRequestCompletion(t *testing.T) {
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm    = DefaultChatCompletion(meter, nil).(*chatCompletion)

		extra = attribute.Key("extra").String("value")
		attrs = []attribute.KeyValue{
			attribute.Key(genaiAttributeOperationName).String(genaiOperationChat),
			attribute.Key(genaiAttributeSystemName).String("custom"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			extra,
		}
		attrsSuccess = attribute.NewSet(attrs...)
		attrsFailure = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeErrorType).String(genaiErrorTypeFallback))...)
	)

	pm.StartRequest(nil)
	pm.SetModel("test-model")
	pm.SetBackend(&filterapi.Backend{Name: "custom"})

	time.Sleep(10 * time.Millisecond)
	pm.RecordRequestCompletion(t.Context(), true, nil, extra)
	count, sum := getHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsSuccess)
	assert.Equal(t, uint64(1), count)
	assert.Greater(t, sum, 0.0)

	// Test some failed requests.
	pm.RecordRequestCompletion(t.Context(), false, nil, extra)
	pm.RecordRequestCompletion(t.Context(), false, nil, extra)
	count, sum = getHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsFailure)
	assert.Equal(t, uint64(2), count)
	assert.Greater(t, sum, 0.0)
}

func TestGetTimeToFirstTokenMsAndGetInterTokenLatencyMs(t *testing.T) {
	c := chatCompletion{timeToFirstToken: 1.0, interTokenLatency: 2.0}
	assert.Equal(t, 1000.0, c.GetTimeToFirstTokenMs())
	assert.Equal(t, 2000.0, c.GetInterTokenLatencyMs())
}

func TestHeaderLabelMapping(t *testing.T) {
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

		// Test header label mapping.
		headerMapping = map[string]string{
			"x-user-id": "user_id",
			"x-org-id":  "org_id",
		}

		pm = NewChatCompletion(meter, nil, headerMapping).(*chatCompletion)
	)

	// Test with headers that should be mapped.
	requestHeaders := map[string]string{
		"x-user-id": "user123",
		"x-org-id":  "org456",
		"x-other":   "ignored", // This should be ignored as it's not in the mapping.
	}

	pm.SetModel("test-model")
	pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	pm.RecordTokenUsage(t.Context(), 10, 5, 15, requestHeaders)

	// Verify that the header mapping is set correctly.
	assert.Equal(t, headerMapping, pm.requestHeaderLabelMapping)

	// Verify that the metrics are recorded with the mapped header attributes.
	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationChat),
		attribute.Key(genaiAttributeSystemName).String(genaiSystemOpenAI),
		attribute.Key(genaiAttributeRequestModel).String("test-model"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
		attribute.Key("user_id").String("user123"),
		attribute.Key("org_id").String("org456"),
	)

	count, _ := getHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	assert.Equal(t, uint64(1), count)
}

// getHistogramValues returns the count and sum of a histogram metric with the given attributes.
func getHistogramValues(t *testing.T, reader metric.Reader, metric string, attrs attribute.Set) (uint64, float64) {
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))

	var datapoints []metricdata.HistogramDataPoint[float64]
	for _, sm := range data.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metric {
				continue
			}
			data := m.Data.(metricdata.Histogram[float64])
			for _, dp := range data.DataPoints {
				if dp.Attributes.Equals(&attrs) {
					datapoints = append(datapoints, dp)
				}
			}
		}
	}

	require.Len(t, datapoints, 1, "found %d datapoints for attributes: %v", len(datapoints), attrs)

	return datapoints[0].Count, datapoints[0].Sum
}
