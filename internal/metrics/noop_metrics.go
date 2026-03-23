package metrics

import (
	"context"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

type noOpMetrics struct{}

func (n *noOpMetrics) StartRequest(headers map[string]string) {}

func (n *noOpMetrics) SetOriginalModel(originalModel internalapi.OriginalModel) {}

func (n *noOpMetrics) SetRequestModel(requestModel internalapi.RequestModel) {}

func (n *noOpMetrics) SetResponseModel(responseModel internalapi.ResponseModel) {}

func (n *noOpMetrics) SetBackend(backend *filterapi.Backend) {}

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
