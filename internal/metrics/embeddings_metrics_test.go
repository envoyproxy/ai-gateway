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

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewEmbeddings(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		mr := sdkmetric.NewManualReader()
		meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
		em := NewEmbeddings(meter).(*embeddings)

		assert.NotNil(t, em)
		assert.IsType(t, &embeddings{}, em)
	})
}

func TestDefaultEmbeddings(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddings(meter).(*embeddings)

	assert.NotNil(t, em)
	assert.NotNil(t, em.metrics)
	assert.Equal(t, "unknown", em.model)
	assert.Equal(t, "unknown", em.backend)
	assert.True(t, em.requestStart.IsZero())
}

func TestEmbeddings_StartRequest(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddings(meter).(*embeddings)

	before := time.Now()
	em.StartRequest(map[string]string{"test": "value"})
	after := time.Now()

	assert.GreaterOrEqual(t, em.requestStart, before)
	assert.LessOrEqual(t, em.requestStart, after)
}

func TestEmbeddings_SetModel(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddings(meter).(*embeddings)

	em.SetModel("text-embedding-ada-002")
	assert.Equal(t, "text-embedding-ada-002", em.model)

	em.SetModel("text-embedding-3-small")
	assert.Equal(t, "text-embedding-3-small", em.model)
}

func TestEmbeddings_SetBackend(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddings(meter).(*embeddings)

	tests := []struct {
		name     string
		backend  *filterapi.Backend
		expected string
	}{
		{
			name: "openai",
			backend: &filterapi.Backend{
				Name:   "openai-backend",
				Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
			},
			expected: genaiSystemOpenAI,
		},
		{
			name: "aws_bedrock",
			backend: &filterapi.Backend{
				Name:   "bedrock-backend",
				Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock},
			},
			expected: genAISystemAWSBedrock,
		},
		{
			name: "custom",
			backend: &filterapi.Backend{
				Name:   "custom-backend",
				Schema: filterapi.VersionedAPISchema{Name: "CustomAPI"},
			},
			expected: "custom-backend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			em.SetBackend(tt.backend)
			assert.Equal(t, tt.expected, em.backend)
		})
	}
}

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

	// For embeddings, input tokens and total tokens should be the same (no output tokens)
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

	// Record multiple token usages
	em.RecordTokenUsage(t.Context(), 5, 5)
	em.RecordTokenUsage(t.Context(), 15, 15)
	em.RecordTokenUsage(t.Context(), 20, 20)

	// Check input tokens: 5 + 15 + 20 = 40
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(3), count)
	assert.Equal(t, 40.0, sum)

	// Check total tokens: 5 + 15 + 20 = 40
	count, sum = getHistogramValues(t, mr, genaiMetricClientTokenUsage, totalAttrs)
	assert.Equal(t, uint64(3), count)
	assert.Equal(t, 40.0, sum)
}

func TestEmbeddings_RecordRequestCompletion(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddings(meter).(*embeddings)

	extra := attribute.Key("extra").String("value")
	attrs := []attribute.KeyValue{
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeSystemName).String(genAISystemAWSBedrock),
		attribute.Key(genaiAttributeRequestModel).String("text-embedding-ada-002"),
		extra,
	}
	attrsSuccess := attribute.NewSet(attrs...)
	attrsFailure := attribute.NewSet(append(attrs, attribute.Key(genaiAttributeErrorType).String(genaiErrorTypeFallback))...)

	em.StartRequest(nil)
	em.SetModel("text-embedding-ada-002")
	em.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}})

	// Test successful request
	time.Sleep(10 * time.Millisecond)
	em.RecordRequestCompletion(t.Context(), true, extra)
	count, sum := getHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsSuccess)
	assert.Equal(t, uint64(1), count)
	assert.Greater(t, sum, 0.0)

	// Test failed requests
	em.RecordRequestCompletion(t.Context(), false, extra)
	em.RecordRequestCompletion(t.Context(), false, extra)
	count, sum = getHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsFailure)
	assert.Equal(t, uint64(2), count)
	assert.Greater(t, sum, 0.0)
}

func TestEmbeddings_RecordRequestCompletion_WithoutStartRequest(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	em := NewEmbeddings(meter).(*embeddings)

	em.SetModel("test-model")
	em.SetBackend(&filterapi.Backend{Name: "test-backend"})

	// Record completion without calling StartRequest (requestStart is zero)
	em.RecordRequestCompletion(t.Context(), true)

	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationEmbedding),
		attribute.Key(genaiAttributeSystemName).String("test-backend"),
		attribute.Key(genaiAttributeRequestModel).String("test-model"),
	)

	count, sum := getHistogramValues(t, mr, genaiMetricServerRequestDuration, attrs)
	assert.Equal(t, uint64(1), count)
	// Duration should be very large since requestStart is zero time
	assert.Greater(t, sum, 1000.0)
}
