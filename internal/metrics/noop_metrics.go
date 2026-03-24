// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

type noOpMetrics struct{}

func (n *noOpMetrics) StartRequest(_ map[string]string) {}

func (n *noOpMetrics) SetOriginalModel(_ internalapi.OriginalModel) {}

func (n *noOpMetrics) SetRequestModel(_ internalapi.RequestModel) {}

func (n *noOpMetrics) SetResponseModel(_ internalapi.ResponseModel) {}

func (n *noOpMetrics) SetBackend(_ *filterapi.Backend) {}

func (n *noOpMetrics) RecordRequestCompletion(
	_ context.Context,
	_ bool,
	_ map[string]string,
) {
}

func (n *noOpMetrics) RecordTokenUsage(
	_ context.Context,
	_ TokenUsage,
	_ map[string]string,
) {
}

func (n *noOpMetrics) GetTimeToFirstTokenMs() float64 {
	return 0
}

func (n *noOpMetrics) GetInterTokenLatencyMs() float64 {
	return 0
}

func (n *noOpMetrics) RecordTokenLatency(
	_ context.Context,
	_ uint32,
	_ bool,
	_ map[string]string,
) {
}

type noOpFactory struct{}

func (f *noOpFactory) NewMetrics() Metrics {
	return &noOpMetrics{}
}

func NewNoOpMetricsFactory() Factory {
	return &noOpFactory{}
}
