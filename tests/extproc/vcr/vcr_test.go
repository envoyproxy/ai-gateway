// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package vcr

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
	"github.com/envoyproxy/ai-gateway/tests/extproc"
	"github.com/envoyproxy/ai-gateway/tests/internal/testenvironment"
	"github.com/envoyproxy/ai-gateway/tests/internal/testopenai"
)

// extprocBin holds the path to the compiled extproc binary.
var extprocBin string

//go:embed envoy.yaml
var envoyConfig string

//go:embed envoy_dynamic_module.yaml
var envoyConfigWithDynamicModules string

//go:embed extproc.yaml
var extprocConfig string

func startTestEnvironment(t *testing.T, extprocBin, extprocConfig string, extprocEnv []string, envoyConfig string) *testenvironment.TestEnvironment {
	if os.Getenv("TEST_WITH_DYNAMIC_MODULE") != "" {
		t.Log("Starting test environment with dynamic modules")
		return testenvironment.StartTestEnvironment(t,
			requireUpstream, map[string]int{"upstream": 11434},
			"", extprocConfig, extprocEnv, envoyConfigWithDynamicModules, true, false, 120*time.Second,
		)
	}
	t.Log("Starting test environment with extproc binary")
	return testenvironment.StartTestEnvironment(t,
		requireUpstream, map[string]int{"upstream": 11434},
		extprocBin, extprocConfig, extprocEnv, envoyConfig, true, false, 120*time.Second,
	)
}

func requireUpstream(t testing.TB, out io.Writer, ports map[string]int) {
	openAIServer, err := testopenai.NewServer(out, ports["upstream"])
	require.NoError(t, err, "failed to create test OpenAI server")
	t.Cleanup(openAIServer.Close)
}

// TestMain sets up the test environment once for all tests.
func TestMain(m *testing.M) {
	var err error
	// Build extproc binary once for all tests.
	if extprocBin, err = extproc.BuildExtProcOnDemand(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start tests due to build error: %v\n", err)
		os.Exit(1)
	}

	if err = internaltesting.BuildDynamicModuleOnDemand(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start tests due to dynamic module build error: %v\n", err)
		os.Exit(1)
	}

	// Run tests.
	os.Exit(m.Run())
}
