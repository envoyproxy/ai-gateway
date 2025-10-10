// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

// buildTestUpstreamOnDemand builds the testupstream binary unless TESTUPSTREAM_BIN is set.
// If TESTUPSTREAM_BIN environment variable is set, it will use that path instead.
func buildTestUpstreamOnDemand() (string, error) {
	return internaltesting.BuildGoBinaryOnDemand("TESTUPSTREAM_BIN", "testupstream", "./tests/internal/testupstreamlib/testupstream")
}
