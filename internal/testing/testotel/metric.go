// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testotel

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func GetCounterValue(t testing.TB, reader metric.Reader, metric string, attrs attribute.Set) float64 {
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))

	for _, sm := range data.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metric {
				continue
			}
			d := m.Data.(metricdata.Sum[float64])
			for _, dp := range d.DataPoints {
				if dp.Attributes.Equals(&attrs) {
					return dp.Value
				}
			}
		}
	}

	t.Fatalf("no counter value found for metric %s with attributes: %v", metric, attrs)
	return 0.0
}

// GetHistogramValues returns the count and sum of a histogram metric with the given attributes.
func GetHistogramValues(t testing.TB, reader metric.Reader, metric string, attrs attribute.Set) (uint64, float64) {
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))

	var dataPoints []metricdata.HistogramDataPoint[float64]
	for _, sm := range data.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metric {
				continue
			}
			data := m.Data.(metricdata.Histogram[float64])
			for _, dp := range data.DataPoints {
				if dp.Attributes.Equals(&attrs) {
					dataPoints = append(dataPoints, dp)
				}
			}
		}
	}

	require.Len(t, dataPoints, 1, "found %d datapoints for attributes: %v", len(dataPoints), attrs)

	return dataPoints[0].Count, dataPoints[0].Sum
}
