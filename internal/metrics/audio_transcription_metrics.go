// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"go.opentelemetry.io/otel/metric"
)

// AudioTranscriptionMetricsFactory is a type alias for Factory specialized for audio transcription metrics.
type AudioTranscriptionMetricsFactory = Factory

// AudioTranscriptionMetrics is a type alias for Metrics used by audio transcription processors.
type AudioTranscriptionMetrics = Metrics

// NewAudioTranscriptionFactory creates a new factory for audio transcription metrics.
func NewAudioTranscriptionFactory(meter metric.Meter, requestHeaderAttributeMapping map[string]string) AudioTranscriptionMetricsFactory {
	return NewMetricsFactory(meter, requestHeaderAttributeMapping, GenAIOperationAudioTranscription)
}
