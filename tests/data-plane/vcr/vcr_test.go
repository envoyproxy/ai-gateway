// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	_ "embed"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/dataplaneenv"
	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

//go:embed envoy.yaml
var envoyConfig string

//go:embed envoy_dynamic_module.yaml
var envoyConfigWithDynamicModules string

//go:embed extproc.yaml
var extprocConfig string

func startTestEnvironment(t *testing.T, extprocEnv []string) *dataplaneenv.TestEnvironment {
	if dm := os.Getenv("TEST_WITH_DYNAMIC_MODULE") != ""; dm {
		t.Log("Starting test environment with dynamic modules")
		return dataplaneenv.StartTestEnvironment(t,
			requireUpstream, map[string]int{"upstream": 11434},
			extprocConfig, extprocEnv, envoyConfigWithDynamicModules, true, false, 120*time.Second,
			dm,
		)
	}
	t.Log("Starting test environment with extproc binary")
	return dataplaneenv.StartTestEnvironment(t,
		requireUpstream, map[string]int{"upstream": 11434},
		extprocConfig, extprocEnv, envoyConfig, true, false, 120*time.Second, false,
	)
}

func requireUpstream(t testing.TB, out io.Writer, ports map[string]int) {
	openAIServer, err := testopenai.NewServer(out, ports["upstream"])
	require.NoError(t, err, "failed to create test OpenAI server")
	t.Cleanup(openAIServer.Close)
}

// TestMain sets up the test environment once for all tests.
func TestMain(m *testing.M) {
	// Run tests.
	os.Exit(m.Run())
}
