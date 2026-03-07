package metrics

import (
	"context"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

type noopMetrics struct{}

func (n *noopMetrics) StartRequest(headers map[string]string) {}

func (n *noopMetrics) SetOriginalModel(originalModel internalapi.OriginalModel) {}

func (n *noopMetrics) SetRequestModel(requestModel internalapi.RequestModel) {}

func (n *noopMetrics) SetResponseModel(responseModel internalapi.ResponseModel) {}

func (n *noopMetrics) SetBackend(backend *filterapi.Backend) {}

func (n *noopMetrics) RecordRequestCompletion(
	ctx context.Context,
	success bool,
	requestHeaders map[string]string,
) {}

func (n *noopMetrics) RecordTokenUsage(
	ctx context.Context,
	usage TokenUsage,
	requestHeaders map[string]string,
) {}

func (n *noopMetrics) GetTimeToFirstTokenMs() float64 {
	return 0
}

func (n *noopMetrics) GetInterTokenLatencyMs() float64 {
	return 0
}

func (n *noopMetrics) RecordTokenLatency(
	ctx context.Context,
	accumulatedOutputToken uint32,
	endOfStream bool,
	requestHeaders map[string]string,
) {}

type noopFactory struct{}

func (f *noopFactory) NewMetrics() Metrics {
	return &noopMetrics{}
}

func NewNoopMetricsFactory() Factory {
	return &noopFactory{}
}
