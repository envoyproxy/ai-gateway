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

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestEmbeddings_RecordTokenUsage(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddings(meter).(*embeddings)

	extra := attribute.Key("extra").String("value")
	attrs := []attribute.KeyValue{
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeSystemName).String(genaiSystemOpenAI),
		attribute.Key(genaiAttributeRequestModel).String("text-embedding-ada-002"),
		extra,
	}
	inputAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)
	totalAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeTotal))...)

	em.SetModel("text-embedding-ada-002")
	em.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	em.RecordTokenUsage(t.Context(), 10, 10, extra)

	// For embeddings, input tokens and total tokens should be the same (no output tokens).
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 10.0, sum)

	count, sum = getHistogramValues(t, mr, genaiMetricClientTokenUsage, totalAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 10.0, sum)
}

func TestEmbeddings_RecordTokenUsage_MultipleRecords(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddings(meter).(*embeddings)

	em.SetModel("text-embedding-3-small")
	em.SetBackend(&filterapi.Backend{
		Name:   "custom-backend",
		Schema: filterapi.VersionedAPISchema{Name: "CustomAPI"},
	})

	attrs := []attribute.KeyValue{
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeSystemName).String("custom-backend"),
		attribute.Key(genaiAttributeRequestModel).String("text-embedding-3-small"),
	}
	inputAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)
	totalAttrs := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeTotal))...)

	// Record multiple token usages.
	em.RecordTokenUsage(t.Context(), 5, 5)
	em.RecordTokenUsage(t.Context(), 15, 15)
	em.RecordTokenUsage(t.Context(), 20, 20)

	// Check input tokens: 5 + 15 + 20 = 40.
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(3), count)
	assert.Equal(t, 40.0, sum)

	// Check total tokens: 5 + 15 + 20 = 40.
	count, sum = getHistogramValues(t, mr, genaiMetricClientTokenUsage, totalAttrs)
	assert.Equal(t, uint64(3), count)
	assert.Equal(t, 40.0, sum)
}
