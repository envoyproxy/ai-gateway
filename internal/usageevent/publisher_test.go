// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package usageevent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// fakeSink is a test Sink whose behavior is controlled by err.
type fakeSink struct {
	err       error
	published []UsageEvent
}

func (f *fakeSink) Publish(_ context.Context, event UsageEvent) error {
	f.published = append(f.published, event)
	return f.err
}

func collectCounterValue(t *testing.T, mr *metric.ManualReader, metricName string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, mr.Collect(context.Background(), &rm))
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "expected Sum[int64] for %s", metricName)
			var total int64
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
			return total
		}
	}
	return 0
}

func collectHistogramCount(t *testing.T, mr *metric.ManualReader, metricName string) uint64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, mr.Collect(context.Background(), &rm))
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			require.True(t, ok, "expected Histogram[float64] for %s", metricName)
			var total uint64
			for _, dp := range hist.DataPoints {
				total += dp.Count
			}
			return total
		}
	}
	return 0
}

func TestNewPublisher_NilSink(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	_, err := NewPublisher(nil, meter, nil)
	assert.Error(t, err)
}

func TestPublisher_Publish_Success_RecordsConstructedAndExported(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	sink := &fakeSink{}
	p, err := NewPublisher(sink, meter, nil)
	require.NoError(t, err)

	p.Publish(context.Background(), testEvent())

	assert.Equal(t, int64(1), collectCounterValue(t, mr, metricUsageEventsConstructed))
	assert.Equal(t, int64(1), collectCounterValue(t, mr, metricUsageEventsExported))
	assert.Equal(t, int64(0), collectCounterValue(t, mr, metricUsageEventsDropped))
	assert.Equal(t, uint64(1), collectHistogramCount(t, mr, metricUsageEventsPublishLatency))
	require.Len(t, sink.published, 1)
	assert.Equal(t, testEvent().EventID, sink.published[0].EventID)
}

func TestPublisher_Publish_SinkError_RecordsDropped(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	sink := &fakeSink{err: errors.New("boom")}
	p, err := NewPublisher(sink, meter, nil)
	require.NoError(t, err)

	p.Publish(context.Background(), testEvent())

	assert.Equal(t, int64(1), collectCounterValue(t, mr, metricUsageEventsConstructed))
	assert.Equal(t, int64(0), collectCounterValue(t, mr, metricUsageEventsExported))
	assert.Equal(t, int64(1), collectCounterValue(t, mr, metricUsageEventsDropped))
}

func TestPublisher_Publish_MultipleEvents_Accumulate(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	sink := &fakeSink{}
	p, err := NewPublisher(sink, meter, nil)
	require.NoError(t, err)

	p.Publish(context.Background(), testEvent())
	p.Publish(context.Background(), testEvent())
	p.Publish(context.Background(), testEvent())

	assert.Equal(t, int64(3), collectCounterValue(t, mr, metricUsageEventsConstructed))
	assert.Equal(t, int64(3), collectCounterValue(t, mr, metricUsageEventsExported))
}
