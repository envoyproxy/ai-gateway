// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

func TestRerank_RecordTokenUsage(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	rr := NewRerank(meter, nil).(*rerank)

	attrs := []attribute.KeyValue{
		attribute.Key(genaiAttributeOperationName).String(genaiOperationRerank),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("rerank-english-v3"),
		attribute.Key(genaiAttributeRequestModel).String("rerank-english-v3"),
		attribute.Key(genaiAttributeResponseModel).String("rerank-english-v3"),
	}
	inputAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)

	rr.SetOriginalModel("rerank-english-v3")
	rr.SetRequestModel("rerank-english-v3")
	rr.SetResponseModel("rerank-english-v3")
	rr.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	rr.RecordTokenUsage(t.Context(), 10, nil)

	// Rerank only consumes input tokens.
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 10.0, sum)
}

func TestRerank_RecordTokenUsage_MultipleRecords(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	rr := NewRerank(meter, nil).(*rerank)

	rr.SetOriginalModel("rerank-english-v3")
	rr.SetRequestModel("rerank-english-v3")
	rr.SetResponseModel("rerank-english-v3")
	rr.SetBackend(&filterapi.Backend{
		Name:   "custom-backend",
		Schema: filterapi.VersionedAPISchema{Name: "CustomAPI"},
	})

	attrs := []attribute.KeyValue{
		attribute.Key(genaiAttributeOperationName).String(genaiOperationRerank),
		attribute.Key(genaiAttributeProviderName).String("custom-backend"),
		attribute.Key(genaiAttributeOriginalModel).String("rerank-english-v3"),
		attribute.Key(genaiAttributeRequestModel).String("rerank-english-v3"),
		attribute.Key(genaiAttributeResponseModel).String("rerank-english-v3"),
	}
	inputAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)

	// Record multiple token usages.
	rr.RecordTokenUsage(t.Context(), 5, nil)
	rr.RecordTokenUsage(t.Context(), 15, nil)
	rr.RecordTokenUsage(t.Context(), 20, nil)

	// Check input tokens: 5 + 15 + 20 = 40.
	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(3), count)
	assert.Equal(t, 40.0, sum)
}

func TestRerank_HeaderLabelMapping(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")

	// Test header label mapping.
	headerMapping := map[string]string{
		"x-tenant-id": "tenant_id",
		"x-api-key":   "api_key",
	}

	rr := NewRerank(meter, headerMapping).(*rerank)

	// Test with headers that should be mapped.
	requestHeaders := map[string]string{
		"x-tenant-id": "tenant789",
		"x-api-key":   "key123",
		"x-other":     "ignored", // This should be ignored as it's not in the mapping.
	}

	rr.SetOriginalModel("rerank-english-v3")
	rr.SetRequestModel("rerank-english-v3")
	rr.SetResponseModel("rerank-english-v3")
	rr.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	rr.RecordTokenUsage(t.Context(), 10, requestHeaders)

	// Verify that the header mapping is set correctly.
	assert.Equal(t, headerMapping, rr.requestHeaderLabelMapping)

	// Verify that the metrics are recorded with the mapped header attributes.
	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationRerank),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("rerank-english-v3"),
		attribute.Key(genaiAttributeRequestModel).String("rerank-english-v3"),
		attribute.Key(genaiAttributeResponseModel).String("rerank-english-v3"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
		attribute.Key("tenant_id").String("tenant789"),
		attribute.Key("api_key").String("key123"),
	)

	count, _ := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	assert.Equal(t, uint64(1), count)
}

func TestRerank_Labels_SetModel_RequestAndResponseDiffer(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	rr := NewRerank(meter, nil).(*rerank)

	rr.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	rr.SetOriginalModel("orig-rerank")
	rr.SetRequestModel("req-rerank")
	rr.SetResponseModel("res-rerank")
	rr.RecordTokenUsage(t.Context(), 7, nil)

	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationRerank),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("orig-rerank"),
		attribute.Key(genaiAttributeRequestModel).String("req-rerank"),
		attribute.Key(genaiAttributeResponseModel).String("res-rerank"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
	)
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 7.0, sum)
}

func TestRerank_RecordRequestCompletion(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	rr := NewRerank(meter, nil).(*rerank)

	attrs := []attribute.KeyValue{
		attribute.Key(genaiAttributeOperationName).String(genaiOperationRerank),
		attribute.Key(genaiAttributeProviderName).String("custom"),
		attribute.Key(genaiAttributeOriginalModel).String("rerank-english-v3"),
		attribute.Key(genaiAttributeRequestModel).String("rerank-english-v3"),
		attribute.Key(genaiAttributeResponseModel).String("rerank-english-v3"),
	}
	attrsSuccess := attribute.NewSet(attrs...)
	attrsFailure := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeErrorType).String(genaiErrorTypeFallback))...)

	rr.StartRequest(nil)
	rr.SetOriginalModel("rerank-english-v3")
	rr.SetRequestModel("rerank-english-v3")
	rr.SetResponseModel("rerank-english-v3")
	rr.SetBackend(&filterapi.Backend{Name: "custom"})

	time.Sleep(10 * time.Millisecond)
	rr.RecordRequestCompletion(t.Context(), true, nil)
	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsSuccess)
	assert.Equal(t, uint64(1), count)
	// Allow small scheduling jitter (~1ms) in timing-based assertion.
	assert.InDelta(t, 10*time.Millisecond.Seconds(), sum, 0.001)

	rr.RecordRequestCompletion(t.Context(), false, nil)
	rr.RecordRequestCompletion(t.Context(), false, nil)
	count, sum = testotel.GetHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsFailure)
	assert.Equal(t, uint64(2), count)
	// Allow small scheduling jitter (~1ms) in timing-based assertion.
	assert.InDelta(t, (2 * 10 * time.Millisecond).Seconds(), sum, 0.001)
}
