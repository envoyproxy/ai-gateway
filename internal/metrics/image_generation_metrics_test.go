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
)

func TestImageGeneration_RecordTokenUsage(t *testing.T) {
	// Mirrors chat/embeddings token usage tests, but for image_generation.
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		im    = NewImageGeneration(meter, nil).(*imageGeneration)

		attrsBase = []attribute.KeyValue{
			attribute.Key(genaiAttributeOperationName).String(genaiOperationImageGeneration),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		}
		inputAttrs  = attribute.NewSet(append(attrsBase, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)
		outputAttrs = attribute.NewSet(append(attrsBase, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput))...)
	)

	// Set labels and record usage.
	im.SetModel("test-model", "test-model")
	im.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	im.RecordTokenUsage(t.Context(), 3, 7, nil)

	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 3.0, sum)

	count, sum = getHistogramValues(t, mr, genaiMetricClientTokenUsage, outputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 7.0, sum)
}

func TestImageGeneration_RecordImageGeneration(t *testing.T) {
	// Use synctest to keep time-based assertions deterministic.
	synctest.Test(t, func(t *testing.T) {
		mr := metric.NewManualReader()
		meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		im := NewImageGeneration(meter, nil).(*imageGeneration)

		// Base attributes plus image-specific ones
		attrs := attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationImageGeneration),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			attribute.Key(genaiAttributeRequestModel).String("img-model"),
			attribute.Key(genaiAttributeResponseModel).String("img-model"),
			attribute.Key("gen_ai.image.count").Int(2),
			attribute.Key("gen_ai.image.model").String("img-model"),
			attribute.Key("gen_ai.image.size").String("1024x1024"),
		)

		im.StartRequest(nil)
		im.SetModel("img-model", "img-model")
		im.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})

		time.Sleep(10 * time.Millisecond)
		im.RecordImageGeneration(t.Context(), 2, "img-model", "1024x1024", nil)

		count, sum := getHistogramValues(t, mr, genaiMetricServerRequestDuration, attrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, 10*time.Millisecond.Seconds(), sum)
	})
}

func TestImageGeneration_HeaderLabelMapping(t *testing.T) {
	// Verify header mapping is honored for token usage metrics.
	var (
		mr            = metric.NewManualReader()
		meter         = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		headerMapping = map[string]string{"x-user-id": "user_id", "x-org-id": "org_id"}
		im            = NewImageGeneration(meter, headerMapping).(*imageGeneration)
	)

	requestHeaders := map[string]string{
		"x-user-id": "user123",
		"x-org-id":  "org456",
	}

	im.SetModel("test-model", "test-model")
	im.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	im.RecordTokenUsage(t.Context(), 5, 0, requestHeaders)

	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationImageGeneration),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeRequestModel).String("test-model"),
		attribute.Key(genaiAttributeResponseModel).String("test-model"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
		attribute.Key("user_id").String("user123"),
		attribute.Key("org_id").String("org456"),
	)

	count, _ := getHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	require.Equal(t, uint64(1), count)
}
