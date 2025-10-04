// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"

// BuildExtProcOnDemand builds the extproc binary unless EXTPROC_BIN is set.
// If EXTPROC_BIN environment variable is set, it will use that path instead.
func BuildExtProcOnDemand() (string, error) {
	return internaltesting.BuildGoBinaryOnDemand("EXTPROC_BIN", "extproc", "./cmd/extproc")
}
