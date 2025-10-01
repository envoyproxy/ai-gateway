// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"testing"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

func TestMain(m *testing.M) {
	e2elib.TestMain(m, e2elib.AIGatewayHelmOption{
		AdditionalArgs: []string{
			// Configure the additional prometheus metrics label for user ID.
			"--set", "controller.metricsRequestHeaderLabels=x-user-id:" + userIDMetricsLabel,
		},
	}, false, true,
	)
}
