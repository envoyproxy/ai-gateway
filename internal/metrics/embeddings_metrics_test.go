// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

func TestEmbeddings_RecordTokenUsage(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddingsFactory(meter, nil)().(*embeddings)

	attrs := []attribute.KeyValue{
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("text-embedding-ada-002"),
		attribute.Key(genaiAttributeRequestModel).String("text-embedding-ada-002"),
		attribute.Key(genaiAttributeResponseModel).String("text-embedding-ada-002"),
	}
	inputAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)

	em.SetOriginalModel("text-embedding-ada-002")
	em.SetRequestModel("text-embedding-ada-002")
	em.SetResponseModel("text-embedding-ada-002")
	em.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	em.RecordTokenUsage(t.Context(), 10, nil)

	// Embeddings only consume input tokens to generate vectors.
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 10.0, sum)
}

func TestEmbeddings_RecordTokenUsage_MultipleRecords(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddingsFactory(meter, nil)().(*embeddings)

	em.SetOriginalModel("text-embedding-3-small")
	em.SetRequestModel("text-embedding-3-small")
	em.SetResponseModel("text-embedding-3-small")
	em.SetBackend(&filterapi.Backend{
		Name:   "custom-backend",
		Schema: filterapi.VersionedAPISchema{Name: "CustomAPI"},
	})

	attrs := []attribute.KeyValue{
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeProviderName).String("custom-backend"),
		attribute.Key(genaiAttributeOriginalModel).String("text-embedding-3-small"),
		attribute.Key(genaiAttributeRequestModel).String("text-embedding-3-small"),
		attribute.Key(genaiAttributeResponseModel).String("text-embedding-3-small"),
	}
	inputAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)

	// Record multiple token usages.
	em.RecordTokenUsage(t.Context(), 5, nil)
	em.RecordTokenUsage(t.Context(), 15, nil)
	em.RecordTokenUsage(t.Context(), 20, nil)

	// Check input tokens: 5 + 15 + 20 = 40.
	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(3), count)
	assert.Equal(t, 40.0, sum)
}

func TestEmbeddings_HeaderLabelMapping(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")

	// Test header label mapping.
	headerMapping := map[string]string{
		"x-tenant-id": "tenant_id",
		"x-api-key":   "api_key",
	}

	em := NewEmbeddingsFactory(meter, headerMapping)().(*embeddings)

	// Test with headers that should be mapped.
	requestHeaders := map[string]string{
		"x-tenant-id": "tenant789",
		"x-api-key":   "key123",
		"x-other":     "ignored", // This should be ignored as it's not in the mapping.
	}

	em.SetOriginalModel("text-embedding-ada-002")
	em.SetRequestModel("text-embedding-ada-002")
	em.SetResponseModel("text-embedding-ada-002")
	em.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	em.RecordTokenUsage(t.Context(), 10, requestHeaders)

	// Verify that the header mapping is set correctly.
	assert.Equal(t, headerMapping, em.requestHeaderAttributeMapping)

	// Verify that the metrics are recorded with the mapped header attributes.
	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("text-embedding-ada-002"),
		attribute.Key(genaiAttributeRequestModel).String("text-embedding-ada-002"),
		attribute.Key(genaiAttributeResponseModel).String("text-embedding-ada-002"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
		attribute.Key("tenant_id").String("tenant789"),
		attribute.Key("api_key").String("key123"),
	)

	count, _ := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	assert.Equal(t, uint64(1), count)
}

func TestEmbeddings_Labels_SetModel_RequestAndResponseDiffer(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddingsFactory(meter, nil)().(*embeddings)

	em.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	em.SetOriginalModel("orig-embed")
	em.SetRequestModel("req-embed")
	em.SetResponseModel("res-embed")
	em.RecordTokenUsage(t.Context(), 7, nil)

	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("orig-embed"),
		attribute.Key(genaiAttributeRequestModel).String("req-embed"),
		attribute.Key(genaiAttributeResponseModel).String("res-embed"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
	)
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 7.0, sum)
}
