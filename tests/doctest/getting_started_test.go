//go:build test_doctest

package doctest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGettingStarted tests the code blocks of docs/getting_started.md file.
func TestGettingStarted(t *testing.T) {
	requireNewKindCluster(t, "envoy-ai-gateway-getting-started")
	requireExecutableInPath(t, "curl", "helm", "kubectl")

	path := "../../site/docs/getting_started.md"
	codeBlocks := requireExtractCodeBlocks(t, path)

	for _, block := range codeBlocks {
		t.Log(block)
	}

	t.Run("EG Install", func(t *testing.T) {
		egInstallBlock := codeBlocks[0]
		require.Len(t, egInstallBlock.lines, 2)
		egInstallBlock.requireRunAllLines(t)
	})

	t.Run("AI Gateway install", func(t *testing.T) {
		aiGatewayBlock := codeBlocks[1]
		require.Len(t, aiGatewayBlock.lines, 3)
		aiGatewayBlock.requireRunAllLines(t)
	})

	t.Run("AI Gateway EG config", func(t *testing.T) {
		aiGatewayEGConfigBlock := codeBlocks[2]
		require.Len(t, aiGatewayEGConfigBlock.lines, 4)
		aiGatewayEGConfigBlock.requireRunAllLines(t)
	})

	t.Run("Deploy Basic Gateway", func(t *testing.T) {
		deployGatewayBlock := codeBlocks[3]
		require.Len(t, deployGatewayBlock.lines, 2)
		requireRunBashCommand(t, deployGatewayBlock.lines[0])
		// Gateway deployment may take a while to be ready (managed by the EG operator).
		requireRunBashCommandEventually(t, deployGatewayBlock.lines[1], time.Minute, 2*time.Second)
	})

	t.Run("Make a request", func(t *testing.T) {
		makeRequestBlock := codeBlocks[4]
		require.Len(t, makeRequestBlock.lines, 2)
		// Run the port-forward command in the background.
		requireStartBackgroundBashCommand(t, makeRequestBlock.lines[0])
		// Then make the request.
		requireRunBashCommandEventually(t, makeRequestBlock.lines[1], time.Minute, 2*time.Second)
	})

	// TODO: we can add any tutorials/docs that depend on the getting started guide setuop here.
}
