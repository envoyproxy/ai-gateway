// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"testing"

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/e2elib"
)

func TestMain(m *testing.M) {
	e2elib.TestMain(m, e2elib.AIGatewayHelmOption{
		AdditionalArgs: []string{
			// Configure the additional span and prometheus metrics label for user ID.
			"--set", "controller.metricsRequestHeaderAttributes=x-user-id:" + userIDAttribute,
			"--set", "controller.spanRequestHeaderAttributes=x-user-id:" + userIDAttribute,
		},
	}, false, true,
	)
}
