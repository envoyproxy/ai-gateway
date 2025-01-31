//go:build test_e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_AWS_Credentials_Rotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const manifest = "./init/aws_credentials/manifest.yaml"
	require.NoError(t, kubectlApplyManifest(ctx, manifest))
	t.Cleanup(func() {
		_ = kubectlDeleteManifest(ctx, manifest)
	})

	// Wait for mock OIDC server to be ready
	requireWaitForPodReady(t, "aws-creds-test", "app=mock-oidc-server")

	// Test OIDC token exchange
	t.Run("OIDC token exchange", func(t *testing.T) {
		// Wait for OIDC credentials secret to be created
		require.Eventually(t, func() bool {
			cmd := kubectl(ctx, "get", "secret",
				"test-oidc-policy-oidc-creds",
				"-n", "aws-creds-test")
			return cmd.Run() == nil
		}, 15*time.Second, time.Second, "OIDC credentials secret not created")

		// Verify the secret contains AWS credentials
		cmd := kubectl(ctx, "get", "secret", "test-oidc-policy-oidc-creds",
			"-n", "aws-creds-test", "-o", "jsonpath={.data.credentials}")
		out, err := cmd.Output()
		require.NoError(t, err)
		require.NotEmpty(t, out, "OIDC credentials should not be empty")
	})

	// Test IAM credentials rotation
	t.Run("IAM credentials rotation", func(t *testing.T) {
		// Get initial secret version
		var initialVersion string
		cmd := kubectl(ctx, "get", "secret", "test-aws-creds", "-n", "aws-creds-test",
			"-o", "jsonpath={.metadata.resourceVersion}")
		out, err := cmd.Output()
		require.NoError(t, err)
		initialVersion = string(out)

		// Wait for secret to be updated (credentials rotated)
		require.Eventually(t, func() bool {
			cmd := kubectl(ctx, "get", "secret", "test-aws-creds", "-n", "aws-creds-test",
				"-o", "jsonpath={.metadata.resourceVersion}")
			out, err := cmd.Output()
			if err != nil {
				return false
			}
			return string(out) != initialVersion
		}, 30*time.Second, time.Second, "Credentials not rotated")

		// Verify policy status is updated
		cmd = kubectl(ctx, "get", "backendSecurityPolicy", "test-aws-policy", "-n", "aws-creds-test",
			"-o", "jsonpath={.status.lastRotationTime}")
		out, err = cmd.Output()
		require.NoError(t, err)
		require.NotEmpty(t, string(out))
	})
}
