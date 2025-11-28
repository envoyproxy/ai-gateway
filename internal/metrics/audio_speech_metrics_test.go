// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

func TestNewAudioSpeechFactory(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		am    = NewAudioSpeechFactory(meter, nil)().(*audioSpeech)
	)

	assert.NotNil(t, am)
	assert.Equal(t, string(GenAIOperationAudioSpeech), am.operation)
}

func TestAudioSpeech_SetMethods(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		am    = NewAudioSpeechFactory(meter, nil)().(*audioSpeech)
	)

	am.SetOriginalModel("tts-1")
	assert.Equal(t, "tts-1", am.originalModel)

	am.SetRequestModel("tts-1-hd")
	assert.Equal(t, "tts-1-hd", am.requestModel)

	am.SetResponseModel("tts-1-hd-20231101")
	assert.Equal(t, "tts-1-hd-20231101", am.responseModel)

	am.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	assert.Equal(t, genaiProviderOpenAI, am.backend)
}

func TestAudioSpeech_RecordTokenUsage(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		am    = NewAudioSpeechFactory(meter, nil)().(*audioSpeech)

		attrs = []attribute.KeyValue{
			attribute.Key(genaiAttributeOperationName).String(string(GenAIOperationAudioSpeech)),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			attribute.Key(genaiAttributeOriginalModel).String("tts-1"),
			attribute.Key(genaiAttributeRequestModel).String("tts-1-hd"),
			attribute.Key(genaiAttributeResponseModel).String("tts-1-hd-20231101"),
		}
		inputAttrs = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)
	)

	am.SetOriginalModel("tts-1")
	am.SetRequestModel("tts-1-hd")
	am.SetResponseModel("tts-1-hd-20231101")
	am.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	am.RecordTokenUsage(t.Context(), 150, nil)

	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 150.0, sum)
}

func TestAudioSpeech_RecordTokenUsage_WithHeaders(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

		headerMapping = map[string]string{
			"x-user-id": "user.id",
		}
		am = NewAudioSpeechFactory(meter, headerMapping)().(*audioSpeech)

		requestHeaders = map[string]string{
			"x-user-id": "user123",
		}
	)

	am.SetOriginalModel("tts-1")
	am.SetRequestModel("tts-1")
	am.SetResponseModel("tts-1")
	am.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	am.RecordTokenUsage(t.Context(), 100, requestHeaders)

	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(string(GenAIOperationAudioSpeech)),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("tts-1"),
		attribute.Key(genaiAttributeRequestModel).String("tts-1"),
		attribute.Key(genaiAttributeResponseModel).String("tts-1"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
		attribute.Key("user.id").String("user123"),
	)

	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 100.0, sum)
}

func TestAudioSpeech_ModelNameDiffers(t *testing.T) {
	t.Parallel()
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	am := NewAudioSpeechFactory(meter, nil)().(*audioSpeech)

	am.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	am.SetOriginalModel("tts-1")
	am.SetRequestModel("tts-1-hd")
	am.SetResponseModel("tts-1-hd-20231101")
	am.RecordTokenUsage(t.Context(), 200, nil)

	inputAttrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(string(GenAIOperationAudioSpeech)),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("tts-1"),
		attribute.Key(genaiAttributeRequestModel).String("tts-1-hd"),
		attribute.Key(genaiAttributeResponseModel).String("tts-1-hd-20231101"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
	)
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 200.0, sum)
}

func TestAudioSpeech_MultipleBackends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		backendSchema   filterapi.APISchemaName
		expectedBackend string
	}{
		{
			name:            "OpenAI",
			backendSchema:   filterapi.APISchemaOpenAI,
			expectedBackend: genaiProviderOpenAI,
		},
		{
			name:            "AWS Bedrock",
			backendSchema:   filterapi.APISchemaAWSBedrock,
			expectedBackend: genaiProviderAWSBedrock,
		},
		{
			name:            "Custom",
			backendSchema:   "custom-provider",
			expectedBackend: "test-backend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mr := metric.NewManualReader()
			meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
			am := NewAudioSpeechFactory(meter, nil)().(*audioSpeech)

			backend := &filterapi.Backend{
				Name:   "test-backend",
				Schema: filterapi.VersionedAPISchema{Name: tt.backendSchema},
			}

			am.SetOriginalModel("tts-1")
			am.SetRequestModel("tts-1")
			am.SetResponseModel("tts-1")
			am.SetBackend(backend)
			am.RecordTokenUsage(t.Context(), 100, nil)

			attrs := attribute.NewSet(
				attribute.Key(genaiAttributeOperationName).String(string(GenAIOperationAudioSpeech)),
				attribute.Key(genaiAttributeProviderName).String(tt.expectedBackend),
				attribute.Key(genaiAttributeOriginalModel).String("tts-1"),
				attribute.Key(genaiAttributeRequestModel).String("tts-1"),
				attribute.Key(genaiAttributeResponseModel).String("tts-1"),
				attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
			)

			count, _ := getHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
			require.Equal(t, uint64(1), count)
		})
	}
}
