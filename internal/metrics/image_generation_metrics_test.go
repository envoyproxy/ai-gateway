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
		im    = NewImageGenerationFactory(meter, nil)().(*imageGeneration)

		attrsBase = []attribute.KeyValue{
			attribute.Key(genaiAttributeOperationName).String(genaiOperationImageGeneration),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		}
		inputAttrs  = attribute.NewSet(append(attrsBase, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)
		outputAttrs = attribute.NewSet(append(attrsBase, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput))...)
	)

	// Set labels and record usage.
	im.SetOriginalModel("test-model")
	im.SetRequestModel("test-model")
	im.SetResponseModel("test-model")
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
		im := NewImageGenerationFactory(meter, nil)().(*imageGeneration)

		// Base attributes for request duration metric (no image-specific attributes)
		baseAttrs := attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationImageGeneration),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			attribute.Key(genaiAttributeOriginalModel).String("img-model"),
			attribute.Key(genaiAttributeRequestModel).String("img-model"),
			attribute.Key(genaiAttributeResponseModel).String("img-model"),
		)
		// Extended attributes for image-info metric
		imageAttrs := attribute.NewSet(
			append(baseAttrs.ToSlice(),
				attribute.Key(genaiAttributeImageCount).Int(2),
				attribute.Key(genaiAttributeImageModel).String("img-model"),
				attribute.Key(genaiAttributeImageSize).String("1024x1024"),
			)...,
		)

		im.StartRequest(nil)
		im.SetOriginalModel("img-model")
		im.SetRequestModel("img-model")
		im.SetResponseModel("img-model")
		im.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})

		time.Sleep(10 * time.Millisecond)
		im.RecordImageGeneration(t.Context(), 2, "img-model", "1024x1024", nil)

		count, sum := getHistogramValues(t, mr, genaiMetricServerRequestDuration, baseAttrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, 10*time.Millisecond.Seconds(), sum)

		// Verify the auxiliary image-info metric captured the image-specific attributes
		count, sum = getHistogramValues(t, mr, "ai_gateway.image.generation", imageAttrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, 1.0, sum)
	})
}

func TestImageGeneration_HeaderLabelMapping(t *testing.T) {
	// Verify header mapping is honored for token usage metrics.
	var (
		mr            = metric.NewManualReader()
		meter         = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		headerMapping = map[string]string{"x-user-id": "user_id", "x-org-id": "org_id"}
		im            = NewImageGenerationFactory(meter, headerMapping)().(*imageGeneration)
	)

	requestHeaders := map[string]string{
		"x-user-id": "user123",
		"x-org-id":  "org456",
	}

	im.SetOriginalModel("test-model")
	im.SetRequestModel("test-model")
	im.SetResponseModel("test-model")
	im.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	im.RecordTokenUsage(t.Context(), 5, 0, requestHeaders)

	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationImageGeneration),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("test-model"),
		attribute.Key(genaiAttributeRequestModel).String("test-model"),
		attribute.Key(genaiAttributeResponseModel).String("test-model"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
		attribute.Key("user_id").String("user123"),
		attribute.Key("org_id").String("org456"),
	)

	count, _ := getHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	require.Equal(t, uint64(1), count)
}
