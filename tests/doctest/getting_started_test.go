//go:build test_doctest

package doctest

import (
	"os"
	"strings"
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
		kill := requireStartBackgroundBashCommand(t, makeRequestBlock.lines[0])
		defer kill()
		// Then make the request.
		requireRunBashCommandEventually(t, makeRequestBlock.lines[1], time.Minute, 2*time.Second)
	})

	// The next code block is just the example output of the previous code block.
	_ = codeBlocks[5]

	t.Run("Delete Gateway", func(t *testing.T) {
		deleteGatewayBlock := codeBlocks[6]
		require.Len(t, deleteGatewayBlock.lines, 2)
		deleteGatewayBlock.requireRunAllLines(t)
	})

	t.Run("OpenAI and AWS", func(t *testing.T) {
		openAIAPIKey := getEnvVarOrSkip(t, "TEST_OPENAI_API_KEY")
		awsAccessKeyID := getEnvVarOrSkip(t, "TEST_AWS_ACCESS_KEY_ID")
		awsSecretAccessKey := getEnvVarOrSkip(t, "TEST_AWS_SECRET_ACCESS_KEY")

		tmpFile := t.TempDir() + "/openai-and-aws.yaml"
		// Replace the placeholders with the actual values.
		_f, err := os.ReadFile("../../examples/basic/basic.yaml")
		require.NoError(t, err)
		f := strings.Replace(string(_f), "OPENAI_API_KEY", openAIAPIKey, -1)
		f = strings.Replace(f, "AWS_ACCESS_KEY_ID", awsAccessKeyID, -1)
		f = strings.Replace(f, "AWS_SECRET_ACCESS_KEY", awsSecretAccessKey, -1)
		require.NoError(t, os.WriteFile(tmpFile, []byte(f), 0644))

		// Apply the configuration.
		requireRunBashCommand(t, "kubectl apply -f "+tmpFile)

		openAIAndAWSBlock := codeBlocks[7]
		require.Len(t, openAIAndAWSBlock.lines, 4)
		// Wait for the gateway to be ready.
		requireRunBashCommandEventually(t, openAIAndAWSBlock.lines[0], time.Minute, 2*time.Second)
		// Run the port-forward command in the background.
		kill := requireStartBackgroundBashCommand(t, openAIAndAWSBlock.lines[1])
		defer kill()
		// Then make the request to OpenAI and AWS.
		requireRunBashCommandEventually(t, openAIAndAWSBlock.lines[2], 30*time.Second, 2*time.Second)
		requireRunBashCommandEventually(t, openAIAndAWSBlock.lines[3], 30*time.Second, 2*time.Second)
	})
}
