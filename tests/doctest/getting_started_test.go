//go:build test_doctest

package doctest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGettingStarted tests the code blocks of docs/getting_started.md file.
func TestGettingStarted(t *testing.T) {
	requireNewKindCluster(t, "envoy-ai-gateway-getting-started")
	requireExecutableInPath(t, "curl", "helm", "kubectl")

	indexPath := "../../site/docs/getting-started/index.md"
	indexCodeBlocks := requireExtractCodeBlocks(t, indexPath)

	for _, block := range indexCodeBlocks {
		t.Log(block)
	}

	t.Run("AI GW Quickstart", func(t *testing.T) {
		aiGatewayQuickstartBlock := indexCodeBlocks[0]
		require.Greater(t, len(aiGatewayQuickstartBlock.lines), 2)
		aiGatewayQuickstartBlock.requireRunAllLines(t)
	})

	// TODO: add tests for the child pages

}
