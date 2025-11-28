// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

type audioSpeech struct {
	metricsImpl
}

type AudioSpeechMetricsFactory func() AudioSpeechMetrics

type AudioSpeechMetrics interface {
	StartRequest(headers map[string]string)
	SetOriginalModel(originalModel internalapi.OriginalModel)
	SetRequestModel(requestModel internalapi.RequestModel)
	SetResponseModel(responseModel internalapi.ResponseModel)
	SetBackend(backend *filterapi.Backend)
	RecordTokenUsage(ctx context.Context, inputTokens uint32, requestHeaderLabelMapping map[string]string)
	RecordRequestCompletion(ctx context.Context, success bool, requestHeaderLabelMapping map[string]string)
}

func NewAudioSpeechFactory(meter metric.Meter, requestHeaderAttributeMapping map[string]string) AudioSpeechMetricsFactory {
	f := &metricsImplFactory{
		metrics:                       newGenAI(meter),
		requestHeaderAttributeMapping: requestHeaderAttributeMapping,
		operation:                     string(GenAIOperationAudioSpeech),
	}
	return func() AudioSpeechMetrics {
		impl := f.NewMetrics().(*metricsImpl)
		return &audioSpeech{metricsImpl: *impl}
	}
}

func (a *audioSpeech) RecordTokenUsage(ctx context.Context, inputTokens uint32, requestHeaders map[string]string) {
	attrs := a.buildBaseAttributes(requestHeaders)

	a.metrics.tokenUsage.Record(ctx, float64(inputTokens),
		metric.WithAttributeSet(attrs),
		metric.WithAttributes(attribute.Key(genaiAttributeTokenType).String(genaiTokenTypeInput)),
	)
}
