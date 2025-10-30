// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

func TestNewResponsesMetrics(t *testing.T) {
	t.Parallel()
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	rm := NewResponses(meter, nil).(*responses)

	assert.NotNil(t, rm)
	assert.False(t, rm.firstTokenSent)
}

func TestResponsesStartRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		t.Helper()
		mr := metric.NewManualReader()
		meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		rm := NewResponses(meter, nil).(*responses)

		before := time.Now()
		rm.StartRequest(nil)
		after := time.Now()

		assert.False(t, rm.firstTokenSent)
		// requestStart should be between before and after
		require.True(t, !rm.requestStart.Before(before) && !rm.requestStart.After(after))
	})
}

func TestResponsesRecordTokenUsage(t *testing.T) {
	t.Parallel()
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	rm := NewResponses(meter, nil).(*responses)

	attrs := []attribute.KeyValue{
		attribute.Key(genaiAttributeOperationName).String(genaiOperationResponses),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("test-model"),
		attribute.Key(genaiAttributeRequestModel).String("test-model"),
		attribute.Key(genaiAttributeResponseModel).String("test-model"),
	}

	inputAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)
	outputAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput))...)
	cachedAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeCachedInput))...)

	rm.SetOriginalModel("test-model")
	rm.SetRequestModel("test-model")
	rm.SetResponseModel("test-model")
	rm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	rm.RecordTokenUsage(t.Context(), 12, 4, 6, nil)

	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 12.0, sum)

	count, sum = testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, cachedAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 4.0, sum)

	count, sum = testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, outputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 6.0, sum)
}

func TestResponsesRecordTokenLatency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		t.Helper()
		mr := metric.NewManualReader()
		meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		rm := NewResponses(meter, nil).(*responses)

		attrs := attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationResponses),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderAWSBedrock),
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		)

		rm.StartRequest(nil)
		rm.SetOriginalModel("test-model")
		rm.SetRequestModel("test-model")
		rm.SetResponseModel("test-model")
		rm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}})

		time.Sleep(10 * time.Millisecond)
		rm.RecordTokenLatency(t.Context(), 1, false, nil)
		assert.True(t, rm.firstTokenSent)
		count, sum := testotel.GetHistogramValues(t, mr, genaiMetricServerTimeToFirstToken, attrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, 10*time.Millisecond.Seconds(), sum)

		time.Sleep(10 * time.Millisecond)
		rm.RecordTokenLatency(t.Context(), 5, true, nil)
		count, sum = testotel.GetHistogramValues(t, mr, genaiMetricServerTimePerOutputToken, attrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, (20*time.Millisecond-10*time.Millisecond).Seconds()/4, sum)
	})
}

func TestResponsesGetTimeToFirstTokenAndInterTokenMs(t *testing.T) {
	t.Parallel()
	r := responses{timeToFirstToken: 2 * time.Second, interTokenLatency: 3 * time.Second}
	assert.Equal(t, 2000.0, r.GetTimeToFirstTokenMs())
	assert.Equal(t, 3000.0, r.GetInterTokenLatencyMs())
}

// helper is provided by testotel package

// Test header mapping and model header key behavior mirroring chat tests.
func TestResponsesHeaderMappingAndModelNameHeaderKey(t *testing.T) {
	t.Parallel()
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	headerMapping := map[string]string{"x-user-id": "user.id"}
	rm := NewResponses(meter, headerMapping).(*responses)

	requestHeaders := map[string]string{"x-user-id": "u1", "x-other": "ignored"}

	rm.SetOriginalModel("orig")
	rm.SetRequestModel("req")
	rm.SetResponseModel("res")
	rm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	rm.RecordTokenUsage(t.Context(), 2, 0, 3, requestHeaders)

	assert.Equal(t, headerMapping, rm.requestHeaderAttributeMapping)

	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationResponses),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("orig"),
		attribute.Key(genaiAttributeRequestModel).String("req"),
		attribute.Key(genaiAttributeResponseModel).String("res"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
		attribute.Key("user.id").String("u1"),
	)

	count, _ := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	assert.Equal(t, uint64(1), count)
}
