// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
)

func TestNewMessages(t *testing.T) {
	mr := metric.NewManualReader()
	meter := metric.NewMeterProvider(metric.WithReader(mr)).Meter("test")
	pm, ok := NewMessagesFactory(meter, nil)().(*chatCompletion)
	require.True(t, ok)
	require.NotNil(t, pm)
	require.NotNil(t, pm.baseMetrics)
	require.Equal(t, genaiOperationMessages, pm.operation)
}
