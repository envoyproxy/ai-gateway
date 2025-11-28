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

func TestNewAudioTranscriptionFactory(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		am    = NewAudioTranscriptionFactory(meter, nil).NewMetrics().(*metricsImpl)
	)

	assert.NotNil(t, am)
	assert.Equal(t, string(GenAIOperationAudioTranscription), am.operation)
}

func TestAudioTranscription_StartRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		t.Helper()
		var (
			mr    = metric.NewManualReader()
			meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
			am    = NewAudioTranscriptionFactory(meter, nil).NewMetrics().(*metricsImpl)
		)

		before := time.Now()
		am.StartRequest(nil)
		after := time.Now()

		assert.Equal(t, before, am.requestStart)
		assert.Equal(t, after, am.requestStart)
	})
}

func TestAudioTranscription_SetMethods(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		am    = NewAudioTranscriptionFactory(meter, nil).NewMetrics().(*metricsImpl)
	)

	am.SetOriginalModel("whisper-1")
	assert.Equal(t, "whisper-1", am.originalModel)

	am.SetRequestModel("whisper-1-large")
	assert.Equal(t, "whisper-1-large", am.requestModel)

	am.SetResponseModel("whisper-1-large-v2")
	assert.Equal(t, "whisper-1-large-v2", am.responseModel)

	am.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	assert.Equal(t, genaiProviderOpenAI, am.backend)
}

func TestAudioTranscription_RecordTokenUsage(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		am    = NewAudioTranscriptionFactory(meter, nil).NewMetrics().(*metricsImpl)

		attrs = []attribute.KeyValue{
			attribute.Key(genaiAttributeOperationName).String(string(GenAIOperationAudioTranscription)),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			attribute.Key(genaiAttributeOriginalModel).String("whisper-1"),
			attribute.Key(genaiAttributeRequestModel).String("whisper-1"),
			attribute.Key(genaiAttributeResponseModel).String("whisper-1"),
		}
		inputAttrs = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)
	)

	am.SetOriginalModel("whisper-1")
	am.SetRequestModel("whisper-1")
	am.SetResponseModel("whisper-1")
	am.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})

	var usage TokenUsage
	usage.SetInputTokens(300)
	am.RecordTokenUsage(t.Context(), usage, nil)

	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 300.0, sum)
}

func TestAudioTranscription_RecordTokenUsage_WithHeaders(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

		headerMapping = map[string]string{
			"x-user-id": "user.id",
			"x-org-id":  "org.id",
		}
		am = NewAudioTranscriptionFactory(meter, headerMapping).NewMetrics().(*metricsImpl)

		requestHeaders = map[string]string{
			"x-user-id": "user456",
			"x-org-id":  "org789",
		}
	)

	am.SetOriginalModel("whisper-1")
	am.SetRequestModel("whisper-1")
	am.SetResponseModel("whisper-1")
	am.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})

	var usage TokenUsage
	usage.SetInputTokens(250)
	am.RecordTokenUsage(t.Context(), usage, requestHeaders)

	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(string(GenAIOperationAudioTranscription)),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("whisper-1"),
		attribute.Key(genaiAttributeRequestModel).String("whisper-1"),
		attribute.Key(genaiAttributeResponseModel).String("whisper-1"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
		attribute.Key("user.id").String("user456"),
		attribute.Key("org.id").String("org789"),
	)

	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 250.0, sum)
}

func TestAudioTranscription_RecordRequestCompletion(t *testing.T) {
	synctest.Test(t, testAudioTranscriptionRecordRequestCompletion)
}

func testAudioTranscriptionRecordRequestCompletion(t *testing.T) {
	t.Helper()

	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		am    = NewAudioTranscriptionFactory(meter, nil).NewMetrics().(*metricsImpl)
		attrs = []attribute.KeyValue{
			attribute.Key(genaiAttributeOperationName).String(string(GenAIOperationAudioTranscription)),
			attribute.Key(genaiAttributeProviderName).String("custom"),
			attribute.Key(genaiAttributeOriginalModel).String("whisper-1"),
			attribute.Key(genaiAttributeRequestModel).String("whisper-1"),
			attribute.Key(genaiAttributeResponseModel).String("whisper-1"),
		}
		attrsSuccess = attribute.NewSet(attrs...)
		attrsFailure = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeErrorType).String(genaiErrorTypeFallback))...)
	)

	am.StartRequest(nil)
	am.SetOriginalModel("whisper-1")
	am.SetRequestModel("whisper-1")
	am.SetResponseModel("whisper-1")
	am.SetBackend(&filterapi.Backend{Name: "custom"})

	time.Sleep(15 * time.Millisecond)
	am.RecordRequestCompletion(t.Context(), true, nil)
	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsSuccess)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 15*time.Millisecond.Seconds(), sum)

	am.RecordRequestCompletion(t.Context(), false, nil)
	count, sum = testotel.GetHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsFailure)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 15*time.Millisecond.Seconds(), sum)
}

func TestAudioTranscription_ModelNameDiffers(t *testing.T) {
	t.Parallel()
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	am := NewAudioTranscriptionFactory(meter, nil).NewMetrics().(*metricsImpl)

	am.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	am.SetOriginalModel("whisper-1")
	am.SetRequestModel("whisper-1-large")
	am.SetResponseModel("whisper-1-large-v2-20231101")

	var usage TokenUsage
	usage.SetInputTokens(400)
	am.RecordTokenUsage(t.Context(), usage, nil)

	inputAttrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(string(GenAIOperationAudioTranscription)),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("whisper-1"),
		attribute.Key(genaiAttributeRequestModel).String("whisper-1-large"),
		attribute.Key(genaiAttributeResponseModel).String("whisper-1-large-v2-20231101"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
	)
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 400.0, sum)
}

func TestAudioTranscription_MultipleBackends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		backendSchema   filterapi.APISchemaName
		backendName     string
		expectedBackend string
	}{
		{
			name:            "OpenAI",
			backendSchema:   filterapi.APISchemaOpenAI,
			backendName:     "openai-backend",
			expectedBackend: genaiProviderOpenAI,
		},
		{
			name:            "AWS Bedrock",
			backendSchema:   filterapi.APISchemaAWSBedrock,
			backendName:     "bedrock-backend",
			expectedBackend: genaiProviderAWSBedrock,
		},
		{
			name:            "Custom Provider",
			backendSchema:   "custom-schema",
			backendName:     "custom-backend",
			expectedBackend: "custom-backend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mr := metric.NewManualReader()
			meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
			am := NewAudioTranscriptionFactory(meter, nil).NewMetrics().(*metricsImpl)

			backend := &filterapi.Backend{
				Name:   tt.backendName,
				Schema: filterapi.VersionedAPISchema{Name: tt.backendSchema},
			}

			am.SetOriginalModel("whisper-1")
			am.SetRequestModel("whisper-1")
			am.SetResponseModel("whisper-1")
			am.SetBackend(backend)

			var usage TokenUsage
			usage.SetInputTokens(100)
			am.RecordTokenUsage(t.Context(), usage, nil)

			attrs := attribute.NewSet(
				attribute.Key(genaiAttributeOperationName).String(string(GenAIOperationAudioTranscription)),
				attribute.Key(genaiAttributeProviderName).String(tt.expectedBackend),
				attribute.Key(genaiAttributeOriginalModel).String("whisper-1"),
				attribute.Key(genaiAttributeRequestModel).String("whisper-1"),
				attribute.Key(genaiAttributeResponseModel).String("whisper-1"),
				attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
			)

			count, _ := getHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
			require.Equal(t, uint64(1), count)
		})
	}
}
