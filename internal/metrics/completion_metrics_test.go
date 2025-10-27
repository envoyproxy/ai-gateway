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
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
)

func TestNewCompletionMetrics(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm    = NewCompletionFactory(meter, nil)().(*completion)
	)

	assert.NotNil(t, pm)
	assert.False(t, pm.firstTokenSent)
}

func TestCompletion_StartRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		t.Helper()
		var (
			mr    = metric.NewManualReader()
			meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
			pm    = NewCompletionFactory(meter, nil)().(*completion)
		)

		before := time.Now()
		pm.StartRequest(nil)
		after := time.Now()

		assert.False(t, pm.firstTokenSent)
		assert.Equal(t, before, pm.requestStart)
		assert.Equal(t, after, pm.requestStart)
		assert.Equal(t, uint32(0), pm.totalOutputTokens)
		assert.Equal(t, time.Duration(0), pm.timeToFirstToken)
		assert.Equal(t, time.Duration(0), pm.interTokenLatency)
	})
}

func TestCompletion_RecordTokenUsage(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm    = NewCompletionFactory(meter, nil)().(*completion)

		attrs = []attribute.KeyValue{
			// gen_ai.operation.name - https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#common-attributes
			attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
			// gen_ai.provider.name - https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#common-attributes
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			// gen_ai.original.model - the original model from the request
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			// gen_ai.request.model - https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#common-attributes
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		}
		// gen_ai.token.type values - https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#common-attributes
		inputAttrs  = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput))...)
		outputAttrs = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput))...)
	)

	pm.SetOriginalModel("test-model")
	pm.SetRequestModel("test-model")
	pm.SetResponseModel("test-model")
	pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	pm.RecordTokenUsage(t.Context(), 10, 5, nil)

	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 10.0, sum)

	count, sum = testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, outputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 5.0, sum)
}

func TestCompletion_RecordTokenLatency(t *testing.T) {
	synctest.Test(t, testCompletionRecordTokenLatency)
}

func testCompletionRecordTokenLatency(t *testing.T) {
	t.Helper()

	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm    = NewCompletionFactory(meter, nil)().(*completion)
		attrs = attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderAWSBedrock),
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		)
	)

	pm.StartRequest(nil)
	pm.SetOriginalModel("test-model")
	pm.SetRequestModel("test-model")
	pm.SetResponseModel("test-model")
	pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}})

	time.Sleep(10 * time.Millisecond)
	pm.RecordTokenLatency(t.Context(), 1, false, nil)
	assert.True(t, pm.firstTokenSent)
	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricServerTimeToFirstToken, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 10*time.Millisecond.Seconds(), sum)

	time.Sleep(10 * time.Millisecond)
	pm.RecordTokenLatency(t.Context(), 5, true, nil)
	count, sum = getHistogramValues(t, mr, genaiMetricServerTimePerOutputToken, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, (20*time.Millisecond-10*time.Millisecond).Seconds()/4, sum)

	time.Sleep(10 * time.Millisecond)
	pm.RecordTokenLatency(t.Context(), 6, false, nil)
	count, sum = getHistogramValues(t, mr, genaiMetricServerTimePerOutputToken, attrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, (20*time.Millisecond-10*time.Millisecond).Seconds()/4, sum)
}

func TestCompletion_RecordRequestCompletion(t *testing.T) {
	synctest.Test(t, testCompletionRecordRequestCompletion)
}

func testCompletionRecordRequestCompletion(t *testing.T) {
	t.Helper()

	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm    = NewCompletionFactory(meter, nil)().(*completion)
		attrs = []attribute.KeyValue{
			attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
			attribute.Key(genaiAttributeProviderName).String("custom"),
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		}
		attrsSuccess = attribute.NewSet(attrs...)
		attrsFailure = attribute.NewSet(append(attrs, attribute.Key(genaiAttributeErrorType).String(genaiErrorTypeFallback))...)
	)

	pm.StartRequest(nil)
	pm.SetOriginalModel("test-model")
	pm.SetRequestModel("test-model")
	pm.SetResponseModel("test-model")
	pm.SetBackend(&filterapi.Backend{Name: "custom"})

	time.Sleep(10 * time.Millisecond)
	pm.RecordRequestCompletion(t.Context(), true, nil)
	count, sum := testotel.GetHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsSuccess)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 10*time.Millisecond.Seconds(), sum)

	pm.RecordRequestCompletion(t.Context(), false, nil)
	pm.RecordRequestCompletion(t.Context(), false, nil)
	count, sum = testotel.GetHistogramValues(t, mr, genaiMetricServerRequestDuration, attrsFailure)
	assert.Equal(t, uint64(2), count)
	assert.Equal(t, 2*10*time.Millisecond.Seconds(), sum)
}

func TestCompletion_GetTimeToFirstTokenMsAndGetInterTokenLatencyMs(t *testing.T) {
	t.Parallel()
	c := completion{timeToFirstToken: 1 * time.Second, interTokenLatency: 2 * time.Second}
	assert.Equal(t, 1000.0, c.GetTimeToFirstTokenMs())
	assert.Equal(t, 2000.0, c.GetInterTokenLatencyMs())
}

func TestCompletion_HeaderLabelMapping(t *testing.T) {
	t.Parallel()
	var (
		mr    = metric.NewManualReader()
		meter = metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")

		// Test header label mapping.
		headerMapping = map[string]string{
			"x-user-id": "user.id",
			"x-org-id":  "org_id",
		}

		pm = NewCompletionFactory(meter, headerMapping)().(*completion)
	)

	// Test with headers that should be mapped.
	requestHeaders := map[string]string{
		"x-user-id": "user123",
		"x-org-id":  "org456",
		"x-other":   "ignored", // This should be ignored as it's not in the mapping.
	}

	pm.SetOriginalModel("test-model")
	pm.SetRequestModel("test-model")
	pm.SetResponseModel("test-model")
	pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	pm.RecordTokenUsage(t.Context(), 10, 5, requestHeaders)

	// Verify that the header mapping is set correctly.
	assert.Equal(t, headerMapping, pm.requestHeaderAttributeMapping)

	// Verify that the metrics are recorded with the mapped header attributes.
	attrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("test-model"),
		attribute.Key(genaiAttributeRequestModel).String("test-model"),
		attribute.Key(genaiAttributeResponseModel).String("test-model"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
		attribute.Key("user.id").String("user123"),
		attribute.Key("org_id").String("org456"),
	)

	count, _ := testotel.GetHistogramValues(t, mr, genaiMetricClientTokenUsage, attrs)
	assert.Equal(t, uint64(1), count)
}

// TestCompletion_ModelNameHeaderKey tests that the model used in metrics is taken from
// the internalapi.ModelNameHeaderKey when present, which allows backend-specific
// model overrides to be tracked in metrics.
func TestCompletion_ModelNameHeaderKey(t *testing.T) {
	t.Parallel()
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	pm := NewCompletionFactory(meter, nil)().(*completion)

	// Simulate headers with model override
	headers := map[string]string{
		internalapi.ModelNameHeaderKeyDefault: "backend-specific-model",
	}

	pm.StartRequest(headers)
	pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}})

	// Simulate the original model from request body before override
	pm.SetOriginalModel("llama3-2-1b")
	// This simulates the processor setting the model from the header
	pm.SetRequestModel("backend-specific-model")
	// Response model is what the backend actually used
	pm.SetResponseModel("us.meta.llama3-2-1b-instruct-v1:0")

	pm.RecordTokenUsage(t.Context(), 10, 5, headers)

	// Verify metrics use the overridden request model and original model
	inputAttrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderAWSBedrock),
		attribute.Key(genaiAttributeOriginalModel).String("llama3-2-1b"),
		attribute.Key(genaiAttributeRequestModel).String("backend-specific-model"),
		attribute.Key(genaiAttributeResponseModel).String("us.meta.llama3-2-1b-instruct-v1:0"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
	)
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 10.0, sum)
}

func TestCompletion_Labels_SetModel_RequestAndResponseDiffer(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	pm := NewCompletionFactory(meter, nil)().(*completion)

	pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})
	pm.SetOriginalModel("orig-model")
	pm.SetRequestModel("req-model")
	pm.SetResponseModel("res-model")
	pm.RecordTokenUsage(t.Context(), 1, 2, nil)

	inputAttrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("orig-model"),
		attribute.Key(genaiAttributeRequestModel).String("req-model"),
		attribute.Key(genaiAttributeResponseModel).String("res-model"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput),
	)
	count, sum := getHistogramValues(t, mr, genaiMetricClientTokenUsage, inputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 1.0, sum)

	outputAttrs := attribute.NewSet(
		attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
		attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
		attribute.Key(genaiAttributeOriginalModel).String("orig-model"),
		attribute.Key(genaiAttributeRequestModel).String("req-model"),
		attribute.Key(genaiAttributeResponseModel).String("res-model"),
		attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeOutput),
	)
	count, sum = getHistogramValues(t, mr, genaiMetricClientTokenUsage, outputAttrs)
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, 2.0, sum)
}

// TestCompletion_RecordTokenLatency_MaxAcrossStream_EndHasNoUsage tests that we track the maximum token count
// across all streaming chunks, even when the final chunk has no usage data.
func TestCompletion_RecordTokenLatency_MaxAcrossStream_EndHasNoUsage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mr := metric.NewManualReader()
		meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm := NewCompletionFactory(meter, nil)().(*completion)

		attrs := attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderAWSBedrock),
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		)

		pm.StartRequest(nil)
		pm.SetOriginalModel("test-model")
		pm.SetRequestModel("test-model")
		pm.SetResponseModel("test-model")
		pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}})

		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 1, false, nil)

		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 5, false, nil)

		// A later event with a smaller number (simulate out-of-order/delta); should not reduce max.
		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 3, false, nil)

		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 0, true, nil)

		count, sum := getHistogramValues(t, mr, genaiMetricServerTimePerOutputToken, attrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, (20*time.Millisecond-5*time.Millisecond).Seconds()/4, sum)
	})
}

// TestCompletion_RecordTokenLatency_OnlyFinalUsage tests that time_per_output_token is calculated correctly
// when token usage is only provided in the final chunk (non-streaming responses).
func TestCompletion_RecordTokenLatency_OnlyFinalUsage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mr := metric.NewManualReader()
		meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm := NewCompletionFactory(meter, nil)().(*completion)

		attrs := attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderAWSBedrock),
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		)

		pm.StartRequest(nil)
		pm.SetOriginalModel("test-model")
		pm.SetRequestModel("test-model")
		pm.SetResponseModel("test-model")
		pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}})

		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 1, false, nil)

		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 3, false, nil)

		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 7, true, nil)

		count, sum := getHistogramValues(t, mr, genaiMetricServerTimePerOutputToken, attrs)
		assert.Equal(t, uint64(1), count)
		expectedDuration := 10 * time.Millisecond / 6
		expectedSeconds := expectedDuration.Seconds()
		assert.Equal(t, expectedSeconds, sum)
	})
}

// TestCompletion_RecordTokenLatency_ZeroTokensFirst tests that time_to_first_token is recorded on the first chunk
// even when it has zero tokens (streaming responses without usage in initial chunks).
func TestCompletion_RecordTokenLatency_ZeroTokensFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mr := metric.NewManualReader()
		meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm := NewCompletionFactory(meter, nil)().(*completion)

		attrs := attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		)

		pm.StartRequest(nil)
		pm.SetOriginalModel("test-model")
		pm.SetRequestModel("test-model")
		pm.SetResponseModel("test-model")
		pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})

		// First token (records TTFT).
		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 0, false, nil)
		count, sum := getHistogramValues(t, mr, genaiMetricServerTimeToFirstToken, attrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, 5*time.Millisecond.Seconds(), sum, "Should record TTFT at 5ms on first call")

		// Second chunk with 2 tokens.
		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 2, false, nil)
		count, sum = getHistogramValues(t, mr, genaiMetricServerTimeToFirstToken, attrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, 5*time.Millisecond.Seconds(), sum, "TTFT should remain at 5ms")

		// Final chunk with total of 5 tokens.
		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 5, true, nil)
		count, sum = getHistogramValues(t, mr, genaiMetricServerTimePerOutputToken, attrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, (10*time.Millisecond).Seconds()/4, sum)
	})
}

// TestCompletion_RecordTokenLatency_SingleToken tests that time_per_output_token is NOT recorded
// when there's only one output token (division by zero case).
func TestCompletion_RecordTokenLatency_SingleToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mr := metric.NewManualReader()
		meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm := NewCompletionFactory(meter, nil)().(*completion)

		attrs := attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		)

		pm.StartRequest(nil)
		pm.SetOriginalModel("test-model")
		pm.SetRequestModel("test-model")
		pm.SetResponseModel("test-model")
		pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})

		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 1, true, nil)

		count, sum := getHistogramValues(t, mr, genaiMetricServerTimeToFirstToken, attrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, 5*time.Millisecond.Seconds(), sum)

		var data2 metricdata.ResourceMetrics
		require.NoError(t, mr.Collect(t.Context(), &data2))
		hasTimePerToken := false
		for _, sm := range data2.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name == genaiMetricServerTimePerOutputToken {
					hasTimePerToken = true
				}
			}
		}
		assert.False(t, hasTimePerToken, "Should not record time_per_output_token with only 1 token")
	})
}

// TestCompletion_RecordTokenLatency_MultipleChunksFormula tests the OTEL spec formula:
// time_per_output_token = (request_duration - time_to_first_token) / (output_tokens - 1)
// with multiple streaming chunks to verify correct calculation.
func TestCompletion_RecordTokenLatency_MultipleChunksFormula(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mr := metric.NewManualReader()
		meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
		pm := NewCompletionFactory(meter, nil)().(*completion)

		attrs := attribute.NewSet(
			attribute.Key(genaiAttributeOperationName).String(genaiOperationCompletion),
			attribute.Key(genaiAttributeProviderName).String(genaiProviderOpenAI),
			attribute.Key(genaiAttributeOriginalModel).String("test-model"),
			attribute.Key(genaiAttributeRequestModel).String("test-model"),
			attribute.Key(genaiAttributeResponseModel).String("test-model"),
		)

		pm.StartRequest(nil)
		pm.SetOriginalModel("test-model")
		pm.SetRequestModel("test-model")
		pm.SetResponseModel("test-model")
		pm.SetBackend(&filterapi.Backend{Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}})

		// First chunk: 3 tokens at 5ms (records TTFT).
		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 3, false, nil)

		// Second chunk: 5 tokens total at 10ms.
		time.Sleep(5 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 5, false, nil)

		// Final chunk: 10 tokens total at 20ms.
		time.Sleep(10 * time.Millisecond)
		pm.RecordTokenLatency(t.Context(), 10, true, nil)

		count, sum := getHistogramValues(t, mr, genaiMetricServerTimeToFirstToken, attrs)
		assert.Equal(t, uint64(1), count)
		assert.Equal(t, 5*time.Millisecond.Seconds(), sum)

		count, sum = getHistogramValues(t, mr, genaiMetricServerTimePerOutputToken, attrs)
		assert.Equal(t, uint64(1), count)
		expected := (15 * time.Millisecond).Seconds() / 9
		assert.InDelta(t, expected, sum, 1e-9)
	})
}
