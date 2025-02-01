//go:build test_e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_AWS_Credentials_Rotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Test OIDC token exchange and credential rotation
	t.Run("OIDC token exchange and rotation", func(t *testing.T) {
		t.Log("Waiting for OIDC credentials secret to be created...")
		// Wait for OIDC credentials secret to be created
		require.Eventually(t, func() bool {
			cmd := kubectlWithOutput(ctx, "get", "secret",
				"test-aws-creds",
				"-n", "aws-creds-test")
			err := cmd.Run()
			if err != nil {
				t.Logf("Secret not found yet: %v", err)
				return false
			}
			return true
		}, 30*time.Second, time.Second, "OIDC credentials secret not created")

		t.Log("Getting initial secret version...")
		// Get initial secret version
		var initialVersion string
		cmd := kubectlWithOutput(ctx, "get", "secret", "test-aws-creds", "-n", "aws-creds-test",
			"-o", "jsonpath={.metadata.resourceVersion}")
		out, err := cmd.Output()
		require.NoError(t, err, "Failed to get initial secret version")
		initialVersion = string(out)
		t.Logf("Initial secret version: %s", initialVersion)

		t.Log("Waiting for secret to be updated (credentials rotated)...")
		// Wait for secret to be updated (credentials rotated)
		require.Eventually(t, func() bool {
			cmd := kubectlWithOutput(ctx, "get", "secret", "test-aws-creds", "-n", "aws-creds-test",
				"-o", "jsonpath={.metadata.resourceVersion}")
			out, err := cmd.Output()
			if err != nil {
				t.Logf("Error getting secret version: %v", err)
				return false
			}
			currentVersion := string(out)
			t.Logf("Current secret version: %s", currentVersion)
			return currentVersion != initialVersion
		}, 75*time.Second, time.Second, "Credentials not rotated")

		t.Log("Verifying policy status...")
		// Verify policy status is updated
		cmd = kubectlWithOutput(ctx, "get", "backendSecurityPolicy", "test-aws-oidc-policy", "-n", "aws-creds-test",
			"-o", "jsonpath={.status.lastRotationTime}")
		out, err = cmd.Output()
		require.NoError(t, err, "Failed to get policy status")
		require.NotEmpty(t, string(out), "Last rotation time is empty")
		t.Logf("Last rotation time: %s", string(out))
	})
}
