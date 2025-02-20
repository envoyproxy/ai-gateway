// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

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
func createTestAWSSecret(t *testing.T, client client.Client, bspName string, accessKey, secretKey, sessionToken string, profile string) {
	if profile == "" {
		profile = "default"
	}
	data := map[string][]byte{
		awsCredentialsKey: []byte(fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\naws_session_token = %s\nregion = us-west-2",
			profile, accessKey, secretKey, sessionToken)),
	}
	err := client.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetBSPSecretName(bspName),
			Namespace: "default",
		},
		Data: data,
	})
	require.NoError(t, err)
}

// verifyAWSSecretCredentials verifies the credentials in a secret
func verifyAWSSecretCredentials(t *testing.T, client client.Client, namespace, secretName, expectedKeyID, expectedSecret, expectedToken, profile, region string) {
	if profile == "" {
		profile = "default"
	}
	secret, err := LookupSecret(t.Context(), client, namespace, GetBSPSecretName(secretName))
	require.NoError(t, err)
	expectedSecretData := fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\naws_session_token = %s\nregion = %s\n", profile, expectedKeyID, expectedSecret, expectedToken, region)
	require.Equal(t, expectedSecretData, string(secret.Data[awsCredentialsKey]))
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
	err := cl.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: data,
	})
	require.NoError(t, err)
}

// MockSTSOperations implements the STSClient interface for testing
type mockSTSOperations struct {
	assumeRoleWithWebIdentityFunc func(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

func (m *mockSTSOperations) AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	if m.assumeRoleWithWebIdentityFunc != nil {
		return m.assumeRoleWithWebIdentityFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("mock not implemented")
}

func TestAWS_OIDCRotator(t *testing.T) {
	t.Run("basic rotation", func(t *testing.T) {
		var mockSTS STSClient = &mockSTSOperations{
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
		// Setup initial credentials and client secret.
		createTestAWSSecret(t, cl, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
		createClientSecret(t, "test-client-secret")

		awsOidcRotator := AWSOIDCRotator{
			client:                         cl,
			stsClient:                      mockSTS,
			backendSecurityPolicyNamespace: "default",
			backendSecurityPolicyName:      "test-secret",
			region:                         "us-east1",
			roleArn:                        "test-role",
		}

		require.NoError(t, awsOidcRotator.Rotate(t.Context(), "NEW-OIDC-TOKEN"))
		verifyAWSSecretCredentials(t, cl, "default", "test-secret", "NEWKEY", "NEWSECRET", "NEWTOKEN", "default", "us-east1")
	})

	t.Run("error handling - STS assume role failure", func(t *testing.T) {
		scheme := runtime.NewScheme()
		scheme.AddKnownTypes(corev1.SchemeGroupVersion,
			&corev1.Secret{},
		)
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		createTestAWSSecret(t, cl, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
		createClientSecret(t, "test-client-secret")
		var mockSTS STSClient = &mockSTSOperations{
			assumeRoleWithWebIdentityFunc: func(_ context.Context, _ *sts.AssumeRoleWithWebIdentityInput, _ ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
				return nil, fmt.Errorf("failed to assume role")
			},
		}
		awsOidcRotator := AWSOIDCRotator{
			client:                         cl,
			stsClient:                      mockSTS,
			backendSecurityPolicyNamespace: "default",
			backendSecurityPolicyName:      "test-secret",
			region:                         "us-east-1",
			roleArn:                        "test-role",
		}
		err := awsOidcRotator.Rotate(t.Context(), "NEW-OIDC-TOKEN")
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

	preRotateTime, _ := awsOidcRotator.GetPreRotationTime(t.Context())
	require.Equal(t, 0, preRotateTime.Minute())

	createTestAWSSecret(t, cl, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
	require.Equal(t, 0, preRotateTime.Minute())

	secret, err := LookupSecret(t.Context(), cl, "default", GetBSPSecretName("test-secret"))
	require.NoError(t, err)

	expiredTime := time.Now().Add(-1 * time.Hour)
	updateExpirationSecretAnnotation(secret, expiredTime)
	require.NoError(t, cl.Update(t.Context(), secret))
	preRotateTime, _ = awsOidcRotator.GetPreRotationTime(t.Context())
	require.Equal(t, expiredTime.Format(time.RFC3339), preRotateTime.Format(time.RFC3339))
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
	preRotateTime, _ := awsOidcRotator.GetPreRotationTime(t.Context())
	require.True(t, awsOidcRotator.IsExpired(preRotateTime))

	createTestAWSSecret(t, cl, "test-secret", "OLDKEY", "OLDSECRET", "OLDTOKEN", "default")
	require.Equal(t, 0, preRotateTime.Minute())

	secret, err := LookupSecret(t.Context(), cl, "default", GetBSPSecretName("test-secret"))
	require.NoError(t, err)

	expiredTime := time.Now().Add(-1 * time.Hour)
	updateExpirationSecretAnnotation(secret, expiredTime)
	require.NoError(t, cl.Update(t.Context(), secret))
	preRotateTime, _ = awsOidcRotator.GetPreRotationTime(t.Context())
	require.True(t, awsOidcRotator.IsExpired(preRotateTime))

	hourFromNowTime := time.Now().Add(1 * time.Hour)
	updateExpirationSecretAnnotation(secret, hourFromNowTime)
	require.NoError(t, cl.Update(t.Context(), secret))
	preRotateTime, _ = awsOidcRotator.GetPreRotationTime(t.Context())
	require.False(t, awsOidcRotator.IsExpired(preRotateTime))
}
