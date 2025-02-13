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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// -----------------------------------------------------------------------------
// Test Helper Methods
// -----------------------------------------------------------------------------

// createTestAWSSecret creates a test secret with given credentials
func createTestAWSSecret(t *testing.T, client client.Client, name string, accessKey, secretKey, sessionToken string, profile string) {
	if profile == "" {
		profile = "default"
	}
	data := map[string][]byte{
		credentialsKey: []byte(fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\naws_session_token = %s\nregion = us-west-2",
			profile, accessKey, secretKey, sessionToken)),
	}
	err := client.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: data,
	})
	require.NoError(t, err)
}

// verifyAWSSecretCredentials verifies the credentials in a secret
func verifyAWSSecretCredentials(t *testing.T, client client.Client, namespace, secretName, expectedKeyID, expectedSecret, expectedToken string, profile string) {
	if profile == "" {
		profile = "default"
	}
	secret, err := LookupSecret(context.Background(), client, namespace, secretName)
	require.NoError(t, err)
	creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
	require.NotNil(t, creds)
	require.Contains(t, creds.profiles, profile)
	assert.Equal(t, expectedKeyID, creds.profiles[profile].accessKeyID)
	assert.Equal(t, expectedSecret, creds.profiles[profile].secretAccessKey)
	assert.Equal(t, expectedToken, creds.profiles[profile].sessionToken)
}

// createClientSecret creates the OIDC client secret
func createClientSecret(t *testing.T, name string) {
	data := map[string][]byte{
		"client-secret": []byte("test-client-secret"),
	}
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	err := cl.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: data,
	})
	require.NoError(t, err)
}

// MockSTSOperations implements the STSOperations interface for testing
type MockSTSOperations struct {
	assumeRoleWithWebIdentityFunc func(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

func (m *MockSTSOperations) AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	if m.assumeRoleWithWebIdentityFunc != nil {
		return m.assumeRoleWithWebIdentityFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("mock not implemented")
}

// -----------------------------------------------------------------------------
// Test Cases
// -----------------------------------------------------------------------------

func TestAWS_OIDCRotator(t *testing.T) {
	t.Run("basic rotation", func(t *testing.T) {
		var mockSTS STSOperations = &MockSTSOperations{
			assumeRoleWithWebIdentityFunc: func(_ context.Context, _ *sts.AssumeRoleWithWebIdentityInput, _ ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
				return &sts.AssumeRoleWithWebIdentityOutput{
					Credentials: &types.Credentials{
						AccessKeyId:     aws.String("NEWKEY"),
						SecretAccessKey: aws.String("NEWSECRET"),
						SessionToken:    aws.String("NEWTOKEN"),
						Expiration:      aws.Time(time.Now().Add(1 * time.Hour)),
					},
				}, nil
			},
		}
		scheme := runtime.NewScheme()
		scheme.AddKnownTypes(corev1.SchemeGroupVersion,
			&corev1.Secret{},
		)
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		// Setup initial credentials and client secret
		createTestAWSSecret(t, cl, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
		createClientSecret(t, "test-client-secret")

		awsOidcRotator := AWSOIDCRotator{
			client:                         cl,
			stsOps:                         mockSTS,
			backendSecurityPolicyNamespace: "default",
			backendSecurityPolicyName:      "test-secret",
		}

		require.NoError(t, awsOidcRotator.Rotate(context.Background(), "us-east1", "test", "NEW-OIDC-TOKEN"))
		verifyAWSSecretCredentials(t, cl, "default", "test-secret", "NEWKEY", "NEWSECRET", "NEWTOKEN", "default")
	})

	t.Run("error handling - STS assume role failure", func(t *testing.T) {
		scheme := runtime.NewScheme()
		scheme.AddKnownTypes(corev1.SchemeGroupVersion,
			&corev1.Secret{},
		)
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		createTestAWSSecret(t, cl, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
		createClientSecret(t, "test-client-secret")
		var mockSTS STSOperations = &MockSTSOperations{
			assumeRoleWithWebIdentityFunc: func(_ context.Context, _ *sts.AssumeRoleWithWebIdentityInput, _ ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
				return nil, fmt.Errorf("failed to assume role")
			},
		}
		awsOidcRotator := AWSOIDCRotator{
			client:                         cl,
			stsOps:                         mockSTS,
			backendSecurityPolicyNamespace: "default",
			backendSecurityPolicyName:      "test-secret",
		}
		err := awsOidcRotator.Rotate(context.Background(), "us-east1", "test", "NEW-OIDC-TOKEN")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to assume role")
	})
}

func TestAWS_GetPreRotationTime(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	awsOidcRotator := AWSOIDCRotator{
		client:                         cl,
		backendSecurityPolicyNamespace: "default",
		backendSecurityPolicyName:      "test-secret",
	}

	require.Nil(t, awsOidcRotator.GetPreRotationTime())

	createTestAWSSecret(t, cl, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
	require.Nil(t, awsOidcRotator.GetPreRotationTime())

	secret, err := LookupSecret(context.Background(), cl, "default", "test-secret")
	require.NoError(t, err)

	expiredTime := time.Now().Add(-1 * time.Hour)
	updateExpirationSecretAnnotation(secret, expiredTime)
	require.NoError(t, cl.Update(context.Background(), secret))
	require.Equal(t, expiredTime.Format(time.RFC3339), awsOidcRotator.GetPreRotationTime().Format(time.RFC3339))
}

func TestAWS_IsExpired(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	awsOidcRotator := AWSOIDCRotator{
		client:                         cl,
		backendSecurityPolicyNamespace: "default",
		backendSecurityPolicyName:      "test-secret",
	}

	require.True(t, awsOidcRotator.IsExpired())

	createTestAWSSecret(t, cl, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
	require.Nil(t, awsOidcRotator.GetPreRotationTime())

	secret, err := LookupSecret(context.Background(), cl, "default", "test-secret")
	require.NoError(t, err)

	expiredTime := time.Now().Add(-1 * time.Hour)
	updateExpirationSecretAnnotation(secret, expiredTime)
	require.NoError(t, cl.Update(context.Background(), secret))
	require.True(t, awsOidcRotator.IsExpired())

	hourFromNowTime := time.Now().Add(1 * time.Hour)
	updateExpirationSecretAnnotation(secret, hourFromNowTime)
	require.NoError(t, cl.Update(context.Background(), secret))
	require.False(t, awsOidcRotator.IsExpired())
}
