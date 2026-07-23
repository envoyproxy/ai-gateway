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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrl "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	testRoleArn             = "arn:aws:iam::123456789012:role/test-role"
	testBaseAccessKey       = "BASE_ACCESS_KEY"       // #nosec G101
	testBaseSecretKey       = "BASE_SECRET_KEY"       // #nosec G101
	testAssumedAccessKey    = "ASSUMED_ACCESS_KEY"    // #nosec G101
	testAssumedSecretKey    = "ASSUMED_SECRET_KEY"    // #nosec G101
	testAssumedSessionToken = "ASSUMED_SESSION_TOKEN" // #nosec G101
	testCredSecretName      = "base-cred-secret"
	testCredSecretNamespace = "default"
	testBSPName             = "test-bsp"
	testBSPNamespace        = "default"
	testRegion              = "us-east-1"
)

// createBaseCredentialsSecret creates the user-provided secret containing base AWS credentials.
func createBaseCredentialsSecret(t *testing.T, cl *fake.ClientBuilder, accessKey, secretKey string) *fake.ClientBuilder {
	t.Helper()
	credContent := fmt.Sprintf("[default]\naws_access_key_id = %s\naws_secret_access_key = %s\n", accessKey, secretKey)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testCredSecretName,
			Namespace: testCredSecretNamespace,
		},
		Data: map[string][]byte{
			AwsCredentialsKey: []byte(credContent),
		},
	}
	return cl.WithObjects(secret)
}

func TestAWSAssumeRoleRotator_BasicRotation(t *testing.T) {
	startTime := time.Now()
	mockSTS := &mockStsOperations{
		assumeRoleFunc: func(_ context.Context, params *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
			assert.Equal(t, testRoleArn, aws.ToString(params.RoleArn))
			return &sts.AssumeRoleOutput{
				Credentials: &types.Credentials{
					AccessKeyId:     aws.String(testAssumedAccessKey),
					SecretAccessKey: aws.String(testAssumedSecretKey),
					SessionToken:    aws.String(testAssumedSessionToken),
					Expiration:      aws.Time(startTime.Add(1 * time.Hour)),
				},
			}, nil
		},
	}

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	builder := fake.NewClientBuilder().WithScheme(scheme)
	builder = createBaseCredentialsSecret(t, builder, testBaseAccessKey, testBaseSecretKey)
	fakeClient := builder.Build()

	rotator := &AWSAssumeRoleRotator{
		client:                         fakeClient,
		logger:                         ctrl.Log.WithName("test"),
		stsClient:                      mockSTS,
		backendSecurityPolicyNamespace: testBSPNamespace,
		backendSecurityPolicyName:      testBSPName,
		roleArn:                        testRoleArn,
		region:                         testRegion,
		credentialSecretName:           testCredSecretName,
		credentialSecretNamespace:      testCredSecretNamespace,
	}

	expiration, err := rotator.Rotate(t.Context())
	require.NoError(t, err)
	require.WithinRange(t, expiration, startTime, startTime.Add(1*time.Hour))

	// Verify the generated secret was created with the assumed role credentials.
	verifyAwsCredentialsSecret(t, fakeClient, testBSPNamespace, testBSPName,
		testAssumedAccessKey, testAssumedSecretKey, testAssumedSessionToken, "default", testRegion)
}

func TestAWSAssumeRoleRotator_UpdateExistingSecret(t *testing.T) {
	startTime := time.Now()
	mockSTS := &mockStsOperations{
		assumeRoleFunc: func(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
			return &sts.AssumeRoleOutput{
				Credentials: &types.Credentials{
					AccessKeyId:     aws.String(testAssumedAccessKey),
					SecretAccessKey: aws.String(testAssumedSecretKey),
					SessionToken:    aws.String(testAssumedSessionToken),
					Expiration:      aws.Time(startTime.Add(1 * time.Hour)),
				},
			}, nil
		},
	}

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	builder := fake.NewClientBuilder().WithScheme(scheme)
	builder = createBaseCredentialsSecret(t, builder, testBaseAccessKey, testBaseSecretKey)
	fakeClient := builder.Build()

	// Pre-create the generated secret (simulating a previous rotation).
	createTestAwsSecret(t, fakeClient, testBSPName, "OLD_KEY", "OLD_SECRET", "OLD_TOKEN", "default", testRegion)

	rotator := &AWSAssumeRoleRotator{
		client:                         fakeClient,
		logger:                         ctrl.Log.WithName("test"),
		stsClient:                      mockSTS,
		backendSecurityPolicyNamespace: testBSPNamespace,
		backendSecurityPolicyName:      testBSPName,
		roleArn:                        testRoleArn,
		region:                         testRegion,
		credentialSecretName:           testCredSecretName,
		credentialSecretNamespace:      testCredSecretNamespace,
	}

	expiration, err := rotator.Rotate(t.Context())
	require.NoError(t, err)
	require.WithinRange(t, expiration, startTime, startTime.Add(1*time.Hour))

	// Verify the secret was updated with new credentials.
	verifyAwsCredentialsSecret(t, fakeClient, testBSPNamespace, testBSPName,
		testAssumedAccessKey, testAssumedSecretKey, testAssumedSessionToken, "default", testRegion)
}

func TestAWSAssumeRoleRotator_AssumeRoleFailure(t *testing.T) {
	mockSTS := &mockStsOperations{
		assumeRoleFunc: func(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
			return nil, fmt.Errorf("access denied")
		},
	}

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	builder := fake.NewClientBuilder().WithScheme(scheme)
	builder = createBaseCredentialsSecret(t, builder, testBaseAccessKey, testBaseSecretKey)
	fakeClient := builder.Build()

	rotator := &AWSAssumeRoleRotator{
		client:                         fakeClient,
		logger:                         ctrl.Log.WithName("test"),
		stsClient:                      mockSTS,
		backendSecurityPolicyNamespace: testBSPNamespace,
		backendSecurityPolicyName:      testBSPName,
		roleArn:                        testRoleArn,
		region:                         testRegion,
		credentialSecretName:           testCredSecretName,
		credentialSecretNamespace:      testCredSecretNamespace,
	}

	expiration, err := rotator.Rotate(t.Context())
	require.Error(t, err)
	require.True(t, expiration.IsZero())
	assert.Contains(t, err.Error(), "access denied")
}

func TestAWSAssumeRoleRotator_NilCredentials(t *testing.T) {
	mockSTS := &mockStsOperations{
		assumeRoleFunc: func(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
			return &sts.AssumeRoleOutput{Credentials: nil}, nil
		},
	}

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	builder := fake.NewClientBuilder().WithScheme(scheme)
	builder = createBaseCredentialsSecret(t, builder, testBaseAccessKey, testBaseSecretKey)
	fakeClient := builder.Build()

	rotator := &AWSAssumeRoleRotator{
		client:                         fakeClient,
		logger:                         ctrl.Log.WithName("test"),
		stsClient:                      mockSTS,
		backendSecurityPolicyNamespace: testBSPNamespace,
		backendSecurityPolicyName:      testBSPName,
		roleArn:                        testRoleArn,
		region:                         testRegion,
		credentialSecretName:           testCredSecretName,
		credentialSecretNamespace:      testCredSecretNamespace,
	}

	expiration, err := rotator.Rotate(t.Context())
	require.Error(t, err)
	require.True(t, expiration.IsZero())
	assert.Contains(t, err.Error(), "unexpected nil credentials")
}

func TestAWSAssumeRoleRotator_GetPreRotationTime(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	rotator := &AWSAssumeRoleRotator{
		client:                         fakeClient,
		backendSecurityPolicyNamespace: testBSPNamespace,
		backendSecurityPolicyName:      testBSPName,
	}

	// No secret exists yet.
	preRotateTime, err := rotator.GetPreRotationTime(t.Context())
	require.True(t, apierrors.IsNotFound(err))
	require.Equal(t, 0, preRotateTime.Minute())

	// Create the secret with an expiration annotation.
	createTestAwsSecret(t, fakeClient, testBSPName, testBaseAccessKey, testBaseSecretKey, "", "default", testRegion)
	secret, err := LookupSecret(t.Context(), fakeClient, testBSPNamespace, GetBSPSecretName(testBSPName))
	require.NoError(t, err)

	expiredTime := time.Now().Add(-1 * time.Hour)
	updateExpirationSecretAnnotation(secret, expiredTime)
	require.NoError(t, fakeClient.Update(t.Context(), secret))

	preRotateTime, err = rotator.GetPreRotationTime(t.Context())
	require.NoError(t, err)
	require.Equal(t, expiredTime.Format(time.RFC3339), preRotateTime.Format(time.RFC3339))
}

func TestAWSAssumeRoleRotator_IsExpired(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	rotator := &AWSAssumeRoleRotator{
		client:                         fakeClient,
		backendSecurityPolicyNamespace: testBSPNamespace,
		backendSecurityPolicyName:      testBSPName,
	}

	// Zero time should be expired.
	require.True(t, rotator.IsExpired(time.Time{}))

	// Past time should be expired.
	require.True(t, rotator.IsExpired(time.Now().Add(-1*time.Hour)))

	// Future time should not be expired.
	require.False(t, rotator.IsExpired(time.Now().Add(1*time.Hour)))
}

//nolint:gosec
func TestParseAWSCredentialsFile(t *testing.T) {
	t.Run("valid credentials", func(t *testing.T) {
		content := "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n"
		parsed, err := ParseAWSCredentialsFile(content)
		require.NoError(t, err)
		require.Equal(t, "AKIAIOSFODNN7EXAMPLE", parsed.AccessKeyID)
		require.Equal(t, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", parsed.SecretAccessKey)
		require.Empty(t, parsed.RoleARN)
	})

	t.Run("with session token and region", func(t *testing.T) {
		content := "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\naws_session_token = TOKEN\nregion = us-east-1\n"
		parsed, err := ParseAWSCredentialsFile(content)
		require.NoError(t, err)
		require.Equal(t, "AKIAIOSFODNN7EXAMPLE", parsed.AccessKeyID)
		require.Equal(t, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", parsed.SecretAccessKey)
		require.Empty(t, parsed.RoleARN)
	})

	t.Run("with role_arn", func(t *testing.T) {
		content := "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\nrole_arn = arn:aws:iam::123456789012:role/my-role\n"
		parsed, err := ParseAWSCredentialsFile(content)
		require.NoError(t, err)
		require.Equal(t, "AKIAIOSFODNN7EXAMPLE", parsed.AccessKeyID)
		require.Equal(t, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", parsed.SecretAccessKey)
		require.Equal(t, "arn:aws:iam::123456789012:role/my-role", parsed.RoleARN)
	})

	t.Run("missing access key", func(t *testing.T) {
		content := "[default]\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n"
		_, err := ParseAWSCredentialsFile(content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing aws_access_key_id or aws_secret_access_key")
	})

	t.Run("missing secret key", func(t *testing.T) {
		content := "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\n"
		_, err := ParseAWSCredentialsFile(content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing aws_access_key_id or aws_secret_access_key")
	})

	t.Run("empty content", func(t *testing.T) {
		_, err := ParseAWSCredentialsFile("")
		require.Error(t, err)
	})

	t.Run("spaces around equals", func(t *testing.T) {
		content := "[default]\naws_access_key_id=KEYNOSP\naws_secret_access_key=SECRETNOSP\n"
		parsed, err := ParseAWSCredentialsFile(content)
		require.NoError(t, err)
		require.Equal(t, "KEYNOSP", parsed.AccessKeyID)
		require.Equal(t, "SECRETNOSP", parsed.SecretAccessKey)
	})
}
