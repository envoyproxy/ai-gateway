package backendauthrotators

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/controller/oauth"
)

// -----------------------------------------------------------------------------
// Test Helper Methods
// -----------------------------------------------------------------------------

// createTestOIDCSecret creates a test secret with given credentials
func createTestOIDCSecret(t *testing.T, h *OIDCRotatorTestHarness, name string, accessKey, secretKey, sessionToken string, profile string) {
	if profile == "" {
		profile = "default"
	}
	data := map[string][]byte{
		credentialsKey: []byte(fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\naws_session_token = %s\nregion = us-west-2",
			profile, accessKey, secretKey, sessionToken)),
	}
	h.CreateSecret(t, name, data)
}

// verifyOIDCSecretCredentials verifies the credentials in a secret
func verifyOIDCSecretCredentials(t *testing.T, h *OIDCRotatorTestHarness, secretName, expectedKeyID, expectedSecret, expectedToken string, profile string) {
	if profile == "" {
		profile = "default"
	}
	secret := h.GetSecret(t, secretName)
	creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
	require.NotNil(t, creds)
	require.Contains(t, creds.profiles, profile)
	assert.Equal(t, expectedKeyID, creds.profiles[profile].accessKeyID)
	assert.Equal(t, expectedSecret, creds.profiles[profile].secretAccessKey)
	assert.Equal(t, expectedToken, creds.profiles[profile].sessionToken)
}

// setupOIDCRotationEvent creates a standard OIDC rotation event
func setupOIDCRotationEvent(name string) RotationEvent {
	return RotationEvent{
		Namespace: "default",
		Name:      name,
		Type:      RotationTypeAWSOIDC,
		Metadata: map[string]string{
			"token_url":          "https://token.test",
			"client_id":          "test-client",
			"client_secret_name": "test-client-secret",
			"role_arn":           "arn:aws:iam::123456789012:role/test-role",
			"profile":            "default",
		},
	}
}

// createClientSecret creates the OIDC client secret
func createClientSecret(t *testing.T, h *OIDCRotatorTestHarness, name string) {
	data := map[string][]byte{
		"client-secret": []byte("test-client-secret"),
	}
	h.CreateSecret(t, name, data)
}

// -----------------------------------------------------------------------------
// Test Cases
// -----------------------------------------------------------------------------

func TestAWS_OIDCRotator(t *testing.T) {
	t.Run("basic rotation", func(t *testing.T) {
		h := NewOIDCRotatorTestHarness(t)

		// Setup initial credentials and client secret
		createTestOIDCSecret(t, h, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
		createClientSecret(t, h, "test-client-secret")

		// Setup mock STS response
		h.MockSTS.assumeRoleWithWebIdentityFunc = func(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
			return &sts.AssumeRoleWithWebIdentityOutput{
				Credentials: &types.Credentials{
					AccessKeyId:     aws.String("NEWKEY"),
					SecretAccessKey: aws.String("NEWSECRET"),
					SessionToken:    aws.String("NEWTOKEN"),
					Expiration:      aws.Time(time.Now().Add(1 * time.Hour)),
				},
			}, nil
		}

		// Setup mock OIDC provider response
		h.MockOIDCProvider.FetchTokenFunc = func(ctx context.Context, config oauth.Config) (*oauth.TokenResponse, error) {
			return &oauth.TokenResponse{
				IDToken: "test-id-token",
			}, nil
		}

		event := setupOIDCRotationEvent("test-secret")
		require.NoError(t, h.Rotator.Rotate(h.Ctx, event))
		verifyOIDCSecretCredentials(t, h, "test-secret", "NEWKEY", "NEWSECRET", "NEWTOKEN", "default")
	})

	t.Run("error handling - OIDC token fetch failure", func(t *testing.T) {
		h := NewOIDCRotatorTestHarness(t)

		createTestOIDCSecret(t, h, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
		createClientSecret(t, h, "test-client-secret")

		// Setup mock OIDC provider to return error
		h.MockOIDCProvider.FetchTokenFunc = func(ctx context.Context, config oauth.Config) (*oauth.TokenResponse, error) {
			return nil, fmt.Errorf("failed to fetch token")
		}

		event := setupOIDCRotationEvent("test-secret")
		err := h.Rotator.Rotate(h.Ctx, event)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch token")
	})

	t.Run("error handling - STS assume role failure", func(t *testing.T) {
		h := NewOIDCRotatorTestHarness(t)

		createTestOIDCSecret(t, h, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
		createClientSecret(t, h, "test-client-secret")

		// Setup mock OIDC provider
		h.MockOIDCProvider.FetchTokenFunc = func(ctx context.Context, config oauth.Config) (*oauth.TokenResponse, error) {
			return &oauth.TokenResponse{
				IDToken: "test-id-token",
			}, nil
		}

		// Setup mock STS to return error
		h.MockSTS.assumeRoleWithWebIdentityFunc = func(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
			return nil, fmt.Errorf("failed to assume role")
		}

		event := setupOIDCRotationEvent("test-secret")
		err := h.Rotator.Rotate(h.Ctx, event)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to assume role")
	})
}
