//go:build test_doctest

package doctest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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

	// TODO: more verifications on making requests, etc.

	// TODO: we can add any tutorials/docs that depend on the getting started guide setuop here.
}
