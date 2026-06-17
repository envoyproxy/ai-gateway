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
			// Configure the additional span, access log, and metrics label for user ID.
			"--set", "controller.spanRequestHeaderAttributes=x-tenant-id:" + tenantIDAttribute,
			"--set", "controller.metricsRequestHeaderAttributes=x-tenant-id:" + tenantIDAttribute,
			"--set", "controller.logRequestHeaderAttributes=x-tenant-id:" + tenantIDAttribute,
			// Use file storage by default so the controller can start without Redis.
			"--set", "controller.storage.backend=file",
			"--set", "controller.storage.fileDir=/tmp/ratelimit",
			// Enable JWT group claim fan-out (no-op when no JWT authn filter is present).
			"--set", "controller.enableJWTGroupFanout=true",
		},
	}, false, true,
	)
}
