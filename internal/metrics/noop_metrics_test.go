// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestNewNoOpMetricsFactory(t *testing.T) {
	t.Parallel()

	factory := NewNoOpMetricsFactory()
	require.NotNil(t, factory)

	metrics := factory.NewMetrics()
	require.NotNil(t, metrics)
	require.IsType(t, &noOpMetrics{}, metrics)
}

func TestNoOpMetrics(t *testing.T) {
	t.Parallel()

	m := &noOpMetrics{}
	ctx := context.Background()
	headers := map[string]string{"x-request-id": "req-123"}
	usage := TokenUsage{}
	usage.SetInputTokens(10)
	usage.SetOutputTokens(4)
	usage.SetTotalTokens(14)
	usage.SetCachedInputTokens(3)
	usage.SetCacheCreationInputTokens(2)

	require.NotPanics(t, func() {
		m.StartRequest(headers)
		m.SetOriginalModel(internalapi.OriginalModel("original-model"))
		m.SetRequestModel(internalapi.RequestModel("request-model"))
		m.SetResponseModel(internalapi.ResponseModel("response-model"))
		m.SetBackend(&filterapi.Backend{Name: "backend"})
		m.RecordRequestCompletion(ctx, true, headers)
		m.RecordRequestCompletion(ctx, false, headers)
		m.RecordTokenUsage(ctx, usage, headers)
		m.RecordTokenLatency(ctx, 4, false, headers)
		m.RecordTokenLatency(ctx, 4, true, headers)
		m.SetBackend(nil)
	})

	require.Zero(t, m.GetTimeToFirstTokenMs())
	require.Zero(t, m.GetInterTokenLatencyMs())
}
