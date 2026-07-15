// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopeninference

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	for _, key := range []string{
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
		"AZURE_OPENAI_API_KEY",
		"AZURE_OPENAI_ENDPOINT",
		"AZURE_OPENAI_DEPLOYMENT",
		"OPENAI_API_VERSION",
	} {
		_ = os.Unsetenv(key)
	}
	os.Exit(m.Run())
}
