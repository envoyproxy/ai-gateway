package token_rotators

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockIAMOperations struct {
	createKeyFunc func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error)
	deleteKeyFunc func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error)
}

func (m *mockIAMOperations) CreateAccessKey(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
	if m.createKeyFunc != nil {
		return m.createKeyFunc(ctx, params, optFns...)
	}
	return nil, nil
}

func (m *mockIAMOperations) DeleteAccessKey(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
	if m.deleteKeyFunc != nil {
		return m.deleteKeyFunc(ctx, params, optFns...)
	}
	return nil, nil
}

type mockSTSOperations struct {
	assumeRoleFunc func(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

func (m *mockSTSOperations) AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	if m.assumeRoleFunc != nil {
		return m.assumeRoleFunc(ctx, params, optFns...)
	}
	return nil, nil
}

func TestAWSCredentialsRotator_Rotate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	k8sClient := ctrlfake.NewClientBuilder().Build()
	k8sClientset := kubefake.NewSimpleClientset()

	// Create a test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			credentialsKey: []byte("[default]\naws_access_key_id = OLDKEY\naws_secret_access_key = OLDSECRET"),
		},
	}
	require.NoError(t, k8sClient.Create(ctx, secret))

	// Track IAM operations
	var deleteKeyID string
	deleteKeyCalled := make(chan struct{})

	mockIAM := &mockIAMOperations{
		createKeyFunc: func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
			return &iam.CreateAccessKeyOutput{
				AccessKey: &iamtypes.AccessKey{
					AccessKeyId:     aws.String("NEWKEY"),
					SecretAccessKey: aws.String("NEWSECRET"),
				},
			}, nil
		},
		deleteKeyFunc: func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			deleteKeyID = *params.AccessKeyId
			close(deleteKeyCalled)
			return &iam.DeleteAccessKeyOutput{}, nil
		},
	}

	rotator, err := NewAWSCredentialsRotator(k8sClient, k8sClientset, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	rotator.KeyDeletionDelay = 100 * time.Millisecond
	rotator.MinPropagationDelay = 10 * time.Millisecond
	rotator.IAMOps = mockIAM

	event := RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      RotationTypeAWSCredentials,
		Metadata:  map[string]string{"old_access_key_id": "OLDKEY"},
	}

	err = rotator.Rotate(ctx, event)
	require.NoError(t, err)

	// Verify the secret was updated immediately
	var updatedSecret corev1.Secret
	err = k8sClient.Get(ctx, ctrlclient.ObjectKey{Namespace: "default", Name: "test-secret"}, &updatedSecret)
	require.NoError(t, err)

	creds := parseAWSCredentialsFile(string(updatedSecret.Data[credentialsKey]))
	require.NotNil(t, creds)
	require.Contains(t, creds.profiles, "default")
	assert.Equal(t, "NEWKEY", creds.profiles["default"].accessKeyID)
	assert.Equal(t, "NEWSECRET", creds.profiles["default"].secretAccessKey)

	// Verify the old key was deleted
	select {
	case <-deleteKeyCalled:
		assert.Equal(t, "OLDKEY", deleteKeyID, "wrong key was deleted")
	case <-time.After(time.Second):
		t.Fatal("old key was not deleted within timeout")
	}

	// Alternative approach with direct assertion
	require.Eventually(t, func() bool {
		return deleteKeyID == "OLDKEY"
	}, time.Second, 10*time.Millisecond, "old key was not deleted as expected")

	t.Run("cancellation triggers immediate deletion", func(t *testing.T) {
		deleteKeyID = ""
		deleteKeyCalled = make(chan struct{})

		event.Metadata["old_access_key_id"] = "OLDKEY2"
		err := rotator.Rotate(ctx, event)
		require.NoError(t, err)

		// Cancel immediately
		cancel()

		// Verify immediate deletion attempt
		select {
		case <-deleteKeyCalled:
			assert.Equal(t, "OLDKEY2", deleteKeyID, "wrong key was deleted")
		case <-time.After(time.Second):
			t.Fatal("old key was not deleted after cancellation")
		}
	})
}

func TestAWSOIDCRotator_Rotate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	k8sClient := ctrlfake.NewClientBuilder().Build()
	k8sClientset := kubefake.NewSimpleClientset()

	// Create test channels
	rotationChan := make(chan RotationEvent)
	scheduleChan := make(chan RotationEvent, 100)

	// Create a test secret first
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			credentialsKey: []byte("[default]\naws_access_key_id = OLDKEY\naws_secret_access_key = OLDSECRET"),
		},
	}
	require.NoError(t, k8sClient.Create(ctx, secret))

	mockSTS := &mockSTSOperations{
		assumeRoleFunc: func(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
			return &sts.AssumeRoleWithWebIdentityOutput{
				Credentials: &ststypes.Credentials{
					AccessKeyId:     aws.String("STSKEY"),
					SecretAccessKey: aws.String("STSSECRET"),
					SessionToken:    aws.String("STSTOKEN"),
					Expiration:      aws.Time(time.Now().Add(time.Hour)),
				},
			}, nil
		},
	}

	rotator, err := NewAWSOIDCRotator(k8sClient, k8sClientset, ctrl.Log.WithName("test"), rotationChan, scheduleChan)
	require.NoError(t, err)
	rotator.SetSTSOperations(mockSTS)

	event := RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      RotationTypeAWSOIDC,
		Metadata: map[string]string{
			"role_arn": "arn:aws:iam::123456789012:role/test",
			"id_token": "token123",
		},
	}

	err = rotator.Rotate(ctx, event)
	require.NoError(t, err)

	// Verify the secret was updated
	var updatedSecret corev1.Secret
	err = k8sClient.Get(ctx, ctrlclient.ObjectKey{Namespace: "default", Name: "test-secret"}, &updatedSecret)
	require.NoError(t, err)

	creds := parseAWSCredentialsFile(string(updatedSecret.Data[credentialsKey]))
	require.NotNil(t, creds)
	require.Contains(t, creds.profiles, defaultProfile)
	assert.Equal(t, "STSKEY", creds.profiles[defaultProfile].accessKeyID)
	assert.Equal(t, "STSSECRET", creds.profiles[defaultProfile].secretAccessKey)
	assert.Equal(t, "STSTOKEN", creds.profiles[defaultProfile].sessionToken)

	// Verify that a rotation event was scheduled
	select {
	case scheduledEvent := <-scheduleChan:
		assert.Equal(t, event.Namespace, scheduledEvent.Namespace)
		assert.Equal(t, event.Name, scheduledEvent.Name)
		assert.Equal(t, event.Type, scheduledEvent.Type)
		assert.Equal(t, event.Metadata["role_arn"], scheduledEvent.Metadata["role_arn"])
		assert.Equal(t, event.Metadata["id_token"], scheduledEvent.Metadata["id_token"])
		assert.NotEmpty(t, scheduledEvent.Metadata["rotate_at"])
	case <-time.After(time.Second):
		t.Fatal("no rotation event was scheduled")
	}

	// Wait a moment for any cleanup
	time.Sleep(100 * time.Millisecond)
}

func TestParseAWSCredentialsFile(t *testing.T) {
	input := `[default]
aws_access_key_id = AKIATEST
aws_secret_access_key = SECRET123
aws_session_token = TOKEN456
region = us-west-2

[other]
aws_access_key_id = AKIA2TEST
aws_secret_access_key = SECRET789
region = us-east-1`

	creds := parseAWSCredentialsFile(input)
	require.NotNil(t, creds)
	require.Len(t, creds.profiles, 2)

	defaultProfile := creds.profiles["default"]
	require.NotNil(t, defaultProfile)
	assert.Equal(t, "AKIATEST", defaultProfile.accessKeyID)
	assert.Equal(t, "SECRET123", defaultProfile.secretAccessKey)
	assert.Equal(t, "TOKEN456", defaultProfile.sessionToken)
	assert.Equal(t, "us-west-2", defaultProfile.region)

	otherProfile := creds.profiles["other"]
	require.NotNil(t, otherProfile)
	assert.Equal(t, "AKIA2TEST", otherProfile.accessKeyID)
	assert.Equal(t, "SECRET789", otherProfile.secretAccessKey)
	assert.Empty(t, otherProfile.sessionToken)
	assert.Equal(t, "us-east-1", otherProfile.region)
}

func TestFormatAWSCredentialsFile(t *testing.T) {
	creds := &awsCredentialsFile{
		profiles: map[string]*awsCredentials{
			"default": {
				profile:         "default",
				accessKeyID:     "AKIATEST",
				secretAccessKey: "SECRET123",
				sessionToken:    "TOKEN456",
				region:          "us-west-2",
			},
			"other": {
				profile:         "other",
				accessKeyID:     "AKIA2TEST",
				secretAccessKey: "SECRET789",
				region:          "us-east-1",
			},
		},
	}

	output := formatAWSCredentialsFile(creds)
	expected := `[default]
aws_access_key_id = AKIATEST
aws_secret_access_key = SECRET123
aws_session_token = TOKEN456
region = us-west-2

[other]
aws_access_key_id = AKIA2TEST
aws_secret_access_key = SECRET789
region = us-east-1
`

	assert.Equal(t, expected, output)
}
