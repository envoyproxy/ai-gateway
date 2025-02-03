package backendauthrotators

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// Test Helper Methods
// -----------------------------------------------------------------------------

// createTestCredentialsSecret creates a test secret with given credentials
func createTestCredentialsSecret(t *testing.T, h *CredentialsRotatorTestHarness, name string, keyID, secret string, profile string) {
	if profile == "" {
		profile = "default"
	}
	data := map[string][]byte{
		credentialsKey: []byte(fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\nregion = us-west-2", profile, keyID, secret)),
	}
	h.CreateSecret(t, name, data)
}

// verifySecretCredentials verifies the credentials in a secret
func verifySecretCredentials(t *testing.T, h *CredentialsRotatorTestHarness, secretName, expectedKeyID, expectedSecret string, profile string) {
	if profile == "" {
		profile = "default"
	}
	secret := h.GetSecret(t, secretName)
	creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
	require.NotNil(t, creds)
	require.Contains(t, creds.profiles, profile)
	assert.Equal(t, expectedKeyID, creds.profiles[profile].accessKeyID)
	assert.Equal(t, expectedSecret, creds.profiles[profile].secretAccessKey)
}

// setupRotationEvent creates a standard rotation event
func setupRotationEvent(name, oldKeyID, profile string) RotationEvent {
	return RotationEvent{
		Namespace: "default",
		Name:      name,
		Type:      RotationTypeAWSCredentials,
		Metadata: map[string]string{
			"old_access_key_id": oldKeyID,
			"profile":           profile,
		},
	}
}

// -----------------------------------------------------------------------------
// Mock Factories
// -----------------------------------------------------------------------------

func setupMockIAMWithKeys(h *CredentialsRotatorTestHarness, accessKeyID, secretAccessKey string) {
	h.MockIAM.createKeyFunc = func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
		return &iam.CreateAccessKeyOutput{
			AccessKey: &iamtypes.AccessKey{
				AccessKeyId:     aws.String(accessKeyID),
				SecretAccessKey: aws.String(secretAccessKey),
			},
		}, nil
	}
}

func setupMockIAMWithError(h *CredentialsRotatorTestHarness, err error) {
	h.MockIAM.createKeyFunc = func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
		return nil, err
	}
	h.MockIAM.deleteKeyFunc = func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
		return nil, err
	}
}

func setupMockIAMWithCounter(h *CredentialsRotatorTestHarness, counter *int32) {
	h.MockIAM.createKeyFunc = func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
		id := atomic.AddInt32(counter, 1)
		return &iam.CreateAccessKeyOutput{
			AccessKey: &iamtypes.AccessKey{
				AccessKeyId:     aws.String(fmt.Sprintf("NEWKEY%d", id)),
				SecretAccessKey: aws.String(fmt.Sprintf("NEWSECRET%d", id)),
			},
		}, nil
	}
}

// -----------------------------------------------------------------------------
// Test Cases
// -----------------------------------------------------------------------------

func TestAWS_CredentialsRotator(t *testing.T) {
	t.Run("basic rotation", func(t *testing.T) {
		h := NewCredentialsRotatorTestHarness(t)
		h.Rotator.KeyDeletionDelay = 100 * time.Millisecond
		h.Rotator.MinPropagationDelay = 10 * time.Millisecond

		createTestCredentialsSecret(t, h, "test-secret", "OLDKEY", "OLDSECRET", "default")
		setupMockIAMWithKeys(h, "NEWKEY", "NEWSECRET")

		event := setupRotationEvent("test-secret", "OLDKEY", "default")
		require.NoError(t, h.Rotator.Rotate(h.Ctx, event))
		verifySecretCredentials(t, h, "test-secret", "NEWKEY", "NEWSECRET", "default")
	})

	t.Run("error handling", func(t *testing.T) {
		h := NewCredentialsRotatorTestHarness(t)
		h.Rotator.KeyDeletionDelay = 100 * time.Millisecond
		h.Rotator.MinPropagationDelay = 10 * time.Millisecond

		createTestCredentialsSecret(t, h, "test-secret", "OLDKEY", "OLDSECRET", "default")
		setupMockIAMWithError(h, fmt.Errorf("AWS API error"))

		event := setupRotationEvent("test-secret", "OLDKEY", "default")
		err := h.Rotator.Rotate(h.Ctx, event)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AWS API error")
	})

	t.Run("concurrent rotations", func(t *testing.T) {
		h := NewCredentialsRotatorTestHarness(t)
		h.Rotator.KeyDeletionDelay = 100 * time.Millisecond
		h.Rotator.MinPropagationDelay = 10 * time.Millisecond

		var counter int32
		setupMockIAMWithCounter(h, &counter)

		const numRotations = 3
		var wg sync.WaitGroup
		wg.Add(numRotations)

		for i := 1; i <= numRotations; i++ {
			secretName := fmt.Sprintf("test-secret-%d", i)
			createTestCredentialsSecret(t, h, secretName, fmt.Sprintf("OLDKEY%d", i), fmt.Sprintf("OLDSECRET%d", i), "default")

			go func(name string, idx int) {
				defer wg.Done()
				event := setupRotationEvent(name, fmt.Sprintf("OLDKEY%d", idx), "default")
				require.NoError(t, h.Rotator.Rotate(h.Ctx, event))
			}(secretName, i)
		}

		wg.Wait()
		assert.Equal(t, int32(numRotations), atomic.LoadInt32(&counter))
	})
}

func TestAWS_DeleteAccessKey(t *testing.T) {
	t.Run("successful key deletion", func(t *testing.T) {
		h := NewCredentialsRotatorTestHarness(t)
		h.Rotator.KeyDeletionDelay = 100 * time.Millisecond
		h.Rotator.MinPropagationDelay = 50 * time.Millisecond

		// Track deletion calls
		var deletedKeyID string
		h.MockIAM.createKeyFunc = func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
			return &iam.CreateAccessKeyOutput{
				AccessKey: &iamtypes.AccessKey{
					AccessKeyId:     aws.String("NEWKEY"),
					SecretAccessKey: aws.String("NEWSECRET"),
				},
			}, nil
		}
		h.MockIAM.deleteKeyFunc = func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			deletedKeyID = aws.ToString(params.AccessKeyId)
			return &iam.DeleteAccessKeyOutput{}, nil
		}

		// Create initial secret with credentials
		createTestCredentialsSecret(t, h, "test-secret", "OLDKEY", "OLDSECRET", "default")

		// Trigger rotation which should schedule deletion of old key
		event := setupRotationEvent("test-secret", "OLDKEY", "default")
		require.NoError(t, h.Rotator.Rotate(h.Ctx, event))

		// Wait for deletion to occur
		time.Sleep(200 * time.Millisecond)

		// Verify the correct key was deleted
		assert.Equal(t, "OLDKEY", deletedKeyID)
	})

	t.Run("deletion failure", func(t *testing.T) {
		h := NewCredentialsRotatorTestHarness(t)
		h.Rotator.KeyDeletionDelay = 100 * time.Millisecond
		h.Rotator.MinPropagationDelay = 50 * time.Millisecond

		// Setup mock with successful create but failed delete
		h.MockIAM.createKeyFunc = func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
			return &iam.CreateAccessKeyOutput{
				AccessKey: &iamtypes.AccessKey{
					AccessKeyId:     aws.String("NEWKEY"),
					SecretAccessKey: aws.String("NEWSECRET"),
				},
			}, nil
		}
		h.MockIAM.deleteKeyFunc = func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			return nil, fmt.Errorf("simulated deletion error")
		}

		// Create initial secret with credentials
		createTestCredentialsSecret(t, h, "test-secret", "OLDKEY", "OLDSECRET", "default")

		// Trigger rotation which should schedule deletion of old key
		event := setupRotationEvent("test-secret", "OLDKEY", "default")
		require.NoError(t, h.Rotator.Rotate(h.Ctx, event))

		// Wait for deletion attempt
		time.Sleep(200 * time.Millisecond)
	})
}
