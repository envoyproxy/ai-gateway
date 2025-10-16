// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import "go.opentelemetry.io/otel/metric"

// MessagesMetricsFactory is a closure that creates a new MessagesMetrics instance.
type MessagesMetricsFactory func() MessagesMetrics

// MessagesMetrics is the interface for the /messages endpoint AI Gateway metrics.
//
// Semantically, it is identical to ChatCompletionMetrics, so it embeds that interface.
//
// The only different is that it has the operation name "messages" instead of "chat".
type MessagesMetrics interface {
	ChatCompletionMetrics
}

// NewMessagesFactory returns a closure that creates a new MessagesMetrics instance.
func NewMessagesFactory(meter metric.Meter, requestHeaderLabelMapping map[string]string) MessagesMetricsFactory {
	b := baseMetricsFactory{metrics: newGenAI(meter), requestHeaderAttributeMapping: requestHeaderLabelMapping}
	return func() MessagesMetrics {
		return &chatCompletion{baseMetrics: b.newBaseMetrics(genaiOperationMessages)}
	}
}
