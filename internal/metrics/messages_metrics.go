// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import "go.opentelemetry.io/otel/metric"

// MessagesMetrics is the interface for the /messages endpoint AI Gateway metrics.
//
// Semantically, it is identical to ChatCompletionMetrics, so it embeds that interface.
//
// The only different is that it has the operation name "messages" instead of "chat".
type MessagesMetrics interface {
	ChatCompletionMetrics
}

// NewMessages creates a new x.MessagesMetrics instance.
func NewMessages(meter metric.Meter, requestHeaderLabelMapping map[string]string) MessagesMetrics {
	return &chatCompletion{
		baseMetrics: newBaseMetrics(meter, genaiOperationMessages, requestHeaderLabelMapping),
	}
}
