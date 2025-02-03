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
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// mockIAMOperations implements IAMOperations for testing
type mockIAMOperations struct {
	createKeyFunc func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error)
	deleteKeyFunc func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error)
}

func (m *mockIAMOperations) CreateAccessKey(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
	if m.createKeyFunc != nil {
		return m.createKeyFunc(ctx, params, optFns...)
	}
	return &iam.CreateAccessKeyOutput{
		AccessKey: &iamtypes.AccessKey{
			AccessKeyId:     aws.String("MOCKKEY"),
			SecretAccessKey: aws.String("MOCKSECRET"),
		},
	}, nil
}

func (m *mockIAMOperations) DeleteAccessKey(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
	if m.deleteKeyFunc != nil {
		return m.deleteKeyFunc(ctx, params, optFns...)
	}
	return &iam.DeleteAccessKeyOutput{}, nil
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

// testSetup contains common test setup data
type testSetup struct {
	ctx        context.Context
	cancel     context.CancelFunc
	client     ctrlclient.Client
	kubeClient *kubefake.Clientset
	rotator    *AWSCredentialsRotator
	mockIAM    *mockIAMOperations
}

// setupTest creates a new test environment
func setupTest() *testSetup {
	ctx, cancel := context.WithCancel(context.Background())
	client := ctrlfake.NewClientBuilder().Build()
	kubeClient := kubefake.NewSimpleClientset()

	// Create mock IAM operations
	mockIAM := newMockIAM()

	rotator, err := NewAWSCredentialsRotator(RotatorConfig{
		Client:        client,
		KubeClient:    kubeClient,
		Logger:        ctrl.Log.WithName("test"),
		IAMOperations: mockIAM,
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create rotator: %v", err))
	}

	rotator.KeyDeletionDelay = 100 * time.Millisecond
	rotator.MinPropagationDelay = 10 * time.Millisecond

	return &testSetup{
		ctx:        ctx,
		cancel:     cancel,
		client:     client,
		kubeClient: kubeClient,
		rotator:    rotator,
		mockIAM:    mockIAM,
	}
}

// cleanup removes any test secrets
func (ts *testSetup) cleanup(t *testing.T) {
	// Create a new client for a clean state
	ts.client = ctrlfake.NewClientBuilder().Build()
	ts.rotator.client = ts.client
}

// createTestSecret creates a test secret with given name and credentials
func (ts *testSetup) createTestSecret(t *testing.T, name string, keyID, secret string, profile string) {
	if profile == "" {
		profile = "default"
	}
	testSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: map[string][]byte{
			credentialsKey: []byte(fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\nregion = us-west-2", profile, keyID, secret)),
		},
	}
	require.NoError(t, ts.client.Create(ts.ctx, testSecret))
}

// setupRotatorWithMock creates a rotator with the given mock IAM operations
func (ts *testSetup) setupRotatorWithMock(mockIAM *mockIAMOperations) {
	ts.mockIAM = mockIAM
	ts.rotator.iamMutex.Lock()
	ts.rotator.IAMOps = mockIAM
	ts.rotator.iamMutex.Unlock()
}

// newMockIAM creates a new mock IAM operations instance with default behavior
func newMockIAM() *mockIAMOperations {
	return &mockIAMOperations{}
}

// newMockIAMWithKeys creates a new mock IAM operations instance that returns specific keys
func newMockIAMWithKeys(accessKeyID, secretAccessKey string) *mockIAMOperations {
	return &mockIAMOperations{
		createKeyFunc: func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
			return &iam.CreateAccessKeyOutput{
				AccessKey: &iamtypes.AccessKey{
					AccessKeyId:     aws.String(accessKeyID),
					SecretAccessKey: aws.String(secretAccessKey),
				},
			}, nil
		},
		deleteKeyFunc: func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			return &iam.DeleteAccessKeyOutput{}, nil
		},
	}
}

// newMockIAMWithError creates a new mock IAM operations instance that returns an error
func newMockIAMWithError(err error) *mockIAMOperations {
	return &mockIAMOperations{
		createKeyFunc: func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
			return nil, err
		},
		deleteKeyFunc: func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			return nil, err
		},
	}
}

// newMockIAMWithCounter creates a new mock IAM operations instance that returns incrementing keys
func newMockIAMWithCounter(counter *int32) *mockIAMOperations {
	return &mockIAMOperations{
		createKeyFunc: func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
			id := atomic.AddInt32(counter, 1)
			return &iam.CreateAccessKeyOutput{
				AccessKey: &iamtypes.AccessKey{
					AccessKeyId:     aws.String(fmt.Sprintf("NEWKEY%d", id)),
					SecretAccessKey: aws.String(fmt.Sprintf("NEWSECRET%d", id)),
				},
			}, nil
		},
		deleteKeyFunc: func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			return &iam.DeleteAccessKeyOutput{}, nil
		},
	}
}

// verifySecretCredentials verifies the credentials in a secret
func (ts *testSetup) verifySecretCredentials(t *testing.T, secretName, expectedKeyID, expectedSecret string, profile string) {
	if profile == "" {
		profile = "default"
	}
	var secret corev1.Secret
	err := ts.client.Get(ts.ctx, ctrlclient.ObjectKey{Namespace: "default", Name: secretName}, &secret)
	require.NoError(t, err)

	creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
	require.NotNil(t, creds)
	require.Contains(t, creds.profiles, profile)
	assert.Equal(t, expectedKeyID, creds.profiles[profile].accessKeyID)
	assert.Equal(t, expectedSecret, creds.profiles[profile].secretAccessKey)
}

func TestAWS_CredentialsRotator(t *testing.T) {
	t.Run("basic rotation", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET", "")
		ts.setupRotatorWithMock(newMockIAMWithKeys("NEWKEY", "NEWSECRET"))

		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata: map[string]string{
				"old_access_key_id": "OLDKEY",
			},
		}

		require.NoError(t, ts.rotator.Rotate(ts.ctx, event))
		ts.verifySecretCredentials(t, "test-secret", "NEWKEY", "NEWSECRET", "")
	})

	t.Run("multi profile rotation", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		// Create a secret with multiple profiles
		testSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				credentialsKey: []byte(`[prod]
aws_access_key_id = OLDKEY_PROD
aws_secret_access_key = OLDSECRET_PROD
[dev]
aws_access_key_id = OLDKEY_DEV
aws_secret_access_key = OLDSECRET_DEV`),
			},
		}
		require.NoError(t, ts.client.Create(ts.ctx, testSecret))

		ts.setupRotatorWithMock(newMockIAMWithKeys("NEWKEY_PROD", "NEWSECRET_PROD"))

		// Rotate prod profile
		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata: map[string]string{
				"old_access_key_id": "OLDKEY_PROD",
				"profile":           "prod",
			},
		}

		require.NoError(t, ts.rotator.Rotate(ts.ctx, event))

		// Verify prod profile was updated but dev profile remains unchanged
		var secret corev1.Secret
		err := ts.client.Get(ts.ctx, ctrlclient.ObjectKey{Namespace: "default", Name: "test-secret"}, &secret)
		require.NoError(t, err)

		creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
		require.NotNil(t, creds)
		require.Contains(t, creds.profiles, "prod")
		require.Contains(t, creds.profiles, "dev")
		assert.Equal(t, "NEWKEY_PROD", creds.profiles["prod"].accessKeyID)
		assert.Equal(t, "NEWSECRET_PROD", creds.profiles["prod"].secretAccessKey)
		assert.Equal(t, "OLDKEY_DEV", creds.profiles["dev"].accessKeyID)
		assert.Equal(t, "OLDSECRET_DEV", creds.profiles["dev"].secretAccessKey)
	})

	t.Run("error handling", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET", "")
		ts.setupRotatorWithMock(newMockIAMWithError(fmt.Errorf("AWS API error")))

		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata: map[string]string{
				"old_access_key_id": "OLDKEY",
			},
		}

		err := ts.rotator.Rotate(ts.ctx, event)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AWS API error")
	})

	t.Run("concurrent rotations", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		var counter int32
		ts.setupRotatorWithMock(newMockIAMWithCounter(&counter))

		// Create test secrets
		const numRotations = 3
		var wg sync.WaitGroup
		wg.Add(numRotations)

		for i := 1; i <= numRotations; i++ {
			secretName := fmt.Sprintf("test-secret-%d", i)
			ts.createTestSecret(t, secretName, fmt.Sprintf("OLDKEY%d", i), fmt.Sprintf("OLDSECRET%d", i), "")

			go func(name string, idx int) {
				defer wg.Done()
				event := RotationEvent{
					Namespace: "default",
					Name:      name,
					Type:      RotationTypeAWSCredentials,
					Metadata: map[string]string{
						"old_access_key_id": fmt.Sprintf("OLDKEY%d", idx),
					},
				}
				err := ts.rotator.Rotate(ts.ctx, event)
				assert.NoError(t, err)
			}(secretName, i)
		}

		wg.Wait()

		// Verify all rotations completed successfully
		assert.Equal(t, int32(numRotations), atomic.LoadInt32(&counter))
		for i := 1; i <= numRotations; i++ {
			secretName := fmt.Sprintf("test-secret-%d", i)
			var secret corev1.Secret
			err := ts.client.Get(ts.ctx, ctrlclient.ObjectKey{Namespace: "default", Name: secretName}, &secret)
			require.NoError(t, err)

			creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
			require.NotNil(t, creds)
			require.Contains(t, creds.profiles, "default")
			assert.Contains(t, creds.profiles["default"].accessKeyID, "NEWKEY")
			assert.Contains(t, creds.profiles["default"].secretAccessKey, "NEWSECRET")
		}
	})
}

func TestAWS_OIDCRotator(t *testing.T) {
	t.Run("basic rotation", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		rotationChan := make(chan RotationEvent)
		scheduleChan := make(chan RotationEvent, 100)

		ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET", "")

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

		rotator, err := NewAWSOIDCRotator(ts.client, ts.kubeClient, ctrl.Log.WithName("test"), rotationChan, scheduleChan)
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

		require.NoError(t, rotator.Rotate(ts.ctx, event))
		ts.verifySecretCredentials(t, "test-secret", "STSKEY", "STSSECRET", "")

		// Verify scheduled rotation
		select {
		case scheduledEvent := <-scheduleChan:
			assert.Equal(t, event.Type, scheduledEvent.Type)
			assert.NotEmpty(t, scheduledEvent.Metadata["rotate_at"])
		case <-time.After(time.Second):
			t.Fatal("no rotation event was scheduled")
		}
	})

	t.Run("multi profile rotation", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		rotationChan := make(chan RotationEvent)
		scheduleChan := make(chan RotationEvent, 100)

		// Create a secret with multiple profiles
		testSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				credentialsKey: []byte(`[prod]
aws_access_key_id = OLDKEY_PROD
aws_secret_access_key = OLDSECRET_PROD
aws_session_token = OLDTOKEN_PROD
region = us-west-2

[dev]
aws_access_key_id = OLDKEY_DEV
aws_secret_access_key = OLDSECRET_DEV
aws_session_token = OLDTOKEN_DEV
region = us-east-1`),
			},
		}
		require.NoError(t, ts.client.Create(ts.ctx, testSecret))

		mockSTS := &mockSTSOperations{
			assumeRoleFunc: func(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
				return &sts.AssumeRoleWithWebIdentityOutput{
					Credentials: &ststypes.Credentials{
						AccessKeyId:     aws.String("NEWKEY_PROD"),
						SecretAccessKey: aws.String("NEWSECRET_PROD"),
						SessionToken:    aws.String("NEWTOKEN_PROD"),
						Expiration:      aws.Time(time.Now().Add(time.Hour)),
					},
				}, nil
			},
		}

		rotator, err := NewAWSOIDCRotator(ts.client, ts.kubeClient, ctrl.Log.WithName("test"), rotationChan, scheduleChan)
		require.NoError(t, err)
		rotator.SetSTSOperations(mockSTS)

		// Rotate prod profile
		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSOIDC,
			Metadata: map[string]string{
				"role_arn": "arn:aws:iam::123456789012:role/test",
				"id_token": "token123",
				"profile":  "prod",
				"region":   "us-west-2",
			},
		}

		require.NoError(t, rotator.Rotate(ts.ctx, event))

		// Verify prod profile was updated but dev profile remains unchanged
		var secret corev1.Secret
		err = ts.client.Get(ts.ctx, ctrlclient.ObjectKey{Namespace: "default", Name: "test-secret"}, &secret)
		require.NoError(t, err)

		creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
		require.NotNil(t, creds)
		require.Contains(t, creds.profiles, "prod")
		require.Contains(t, creds.profiles, "dev")

		// Check prod profile was updated
		assert.Equal(t, "NEWKEY_PROD", creds.profiles["prod"].accessKeyID)
		assert.Equal(t, "NEWSECRET_PROD", creds.profiles["prod"].secretAccessKey)
		assert.Equal(t, "NEWTOKEN_PROD", creds.profiles["prod"].sessionToken)
		assert.Equal(t, "us-west-2", creds.profiles["prod"].region)

		// Check dev profile remains unchanged
		assert.Equal(t, "OLDKEY_DEV", creds.profiles["dev"].accessKeyID)
		assert.Equal(t, "OLDSECRET_DEV", creds.profiles["dev"].secretAccessKey)
		assert.Equal(t, "OLDTOKEN_DEV", creds.profiles["dev"].sessionToken)
		assert.Equal(t, "us-east-1", creds.profiles["dev"].region)

		// Verify scheduled rotation
		select {
		case scheduledEvent := <-scheduleChan:
			assert.Equal(t, event.Type, scheduledEvent.Type)
			assert.Equal(t, event.Metadata["profile"], scheduledEvent.Metadata["profile"])
			assert.NotEmpty(t, scheduledEvent.Metadata["rotate_at"])
		case <-time.After(time.Second):
			t.Fatal("no rotation event was scheduled")
		}
	})
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

func TestAWS_CredentialsRotator_Initialize(t *testing.T) {
	t.Run("successful initialization", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		// Ensure clean state
		ts.cleanup(t)

		// Create an empty secret
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "init-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				credentialsKey: []byte("[custom]\nregion = us-west-2"),
			},
		}
		require.NoError(t, ts.client.Create(ts.ctx, secret))

		ts.setupRotatorWithMock(newMockIAMWithKeys("INITKEY", "INITSECRET"))

		event := RotationEvent{
			Namespace: "default",
			Name:      "init-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata: map[string]string{
				"profile": "custom",
				"region":  "us-west-2",
			},
		}

		err := ts.rotator.Initialize(ts.ctx, event)
		require.NoError(t, err)

		ts.verifySecretCredentials(t, "init-secret", "INITKEY", "INITSECRET", "custom")
	})

	t.Run("initialization with AWS API error", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()
		defer ts.cleanup(t)

		// Create an empty secret first
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "error-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				credentialsKey: []byte("[default]\nregion = us-west-2"),
			},
		}
		require.NoError(t, ts.client.Create(ts.ctx, secret))

		ts.setupRotatorWithMock(newMockIAMWithError(fmt.Errorf("AWS API error: limit exceeded")))

		event := RotationEvent{
			Namespace: "default",
			Name:      "error-secret",
			Type:      RotationTypeAWSCredentials,
		}

		err := ts.rotator.Initialize(ts.ctx, event)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create access key")
	})
}

func TestAWS_CredentialsRotator_ErrorHandling(t *testing.T) {
	ts := setupTest()
	defer ts.cancel()

	ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET", "")

	t.Run("handle AWS API errors during rotation", func(t *testing.T) {
		ts.setupRotatorWithMock(newMockIAMWithError(fmt.Errorf("AWS API error: service unavailable")))

		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata:  map[string]string{"old_access_key_id": "OLDKEY"},
		}

		err := ts.rotator.Rotate(ts.ctx, event)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create new access key")

		ts.verifySecretCredentials(t, "test-secret", "OLDKEY", "OLDSECRET", "")
	})

	t.Run("handle key deletion errors", func(t *testing.T) {
		deleteError := make(chan error, 1)
		mockIAM := newMockIAMWithKeys("NEWKEY", "NEWSECRET")
		mockIAM.deleteKeyFunc = func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			err := fmt.Errorf("AWS API error: delete failed")
			deleteError <- err
			return nil, err
		}

		ts.setupRotatorWithMock(mockIAM)

		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata:  map[string]string{"old_access_key_id": "OLDKEY"},
		}

		err := ts.rotator.Rotate(ts.ctx, event)
		require.NoError(t, err)

		// Verify the error during deletion
		select {
		case err := <-deleteError:
			require.Error(t, err)
			assert.Contains(t, err.Error(), "delete failed")
		case <-time.After(time.Second):
			t.Fatal("deletion error not received")
		}

		ts.verifySecretCredentials(t, "test-secret", "NEWKEY", "NEWSECRET", "")
	})
}

func TestAWS_CredentialsRotator_Security(t *testing.T) {
	t.Run("verify old credentials are securely deleted", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()
		defer ts.cleanup(t)

		// Create initial secret with old credentials
		ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET", "")

		var deletedKeyID string
		var deletedKeyMutex sync.Mutex

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
				deletedKeyMutex.Lock()
				deletedKeyID = *params.AccessKeyId
				deletedKeyMutex.Unlock()
				return &iam.DeleteAccessKeyOutput{}, nil
			},
		}

		ts.setupRotatorWithMock(mockIAM)

		// Set up rotation event with old key ID
		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata: map[string]string{
				"old_access_key_id": "OLDKEY",
			},
		}

		require.NoError(t, ts.rotator.Rotate(ts.ctx, event))

		// Wait for deletion to complete
		time.Sleep(200 * time.Millisecond)

		// Verify old key was deleted
		deletedKeyMutex.Lock()
		assert.Equal(t, "OLDKEY", deletedKeyID, "old key was not deleted")
		deletedKeyMutex.Unlock()

		// Verify old credentials are not present in the secret
		var updatedSecret corev1.Secret
		err := ts.client.Get(ts.ctx, ctrlclient.ObjectKey{Namespace: "default", Name: "test-secret"}, &updatedSecret)
		require.NoError(t, err)
		secretData := string(updatedSecret.Data[credentialsKey])
		assert.NotContains(t, secretData, "OLDKEY")
		assert.NotContains(t, secretData, "OLDSECRET")
	})
}

func TestAWS_CredentialsRotator_Performance(t *testing.T) {
	ts := setupTest()
	defer ts.cancel()

	ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET", "")

	t.Run("measure rotation time", func(t *testing.T) {
		ts.setupRotatorWithMock(newMockIAMWithKeys("NEWKEY", "NEWSECRET"))

		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata:  map[string]string{"old_access_key_id": "OLDKEY"},
		}

		// Measure time for initial credential update
		start := time.Now()
		err := ts.rotator.Rotate(ts.ctx, event)
		require.NoError(t, err)
		initialUpdateDuration := time.Since(start)

		// Verify the initial update is quick (under 50ms)
		assert.Less(t, initialUpdateDuration.Milliseconds(), int64(50),
			"initial credential update took too long: %v", initialUpdateDuration)

		ts.verifySecretCredentials(t, "test-secret", "NEWKEY", "NEWSECRET", "")
	})

	t.Run("handle high volume of rotation requests", func(t *testing.T) {
		var createKeyCount int32
		ts.setupRotatorWithMock(newMockIAMWithCounter(&createKeyCount))

		// Create multiple secrets
		numSecrets := 10
		for i := 1; i <= numSecrets; i++ {
			ts.createTestSecret(t, fmt.Sprintf("perf-secret-%d", i), fmt.Sprintf("OLDKEY%d", i), fmt.Sprintf("OLDSECRET%d", i), "")
		}

		// Perform concurrent rotations
		start := time.Now()
		var wg sync.WaitGroup
		errChan := make(chan error, numSecrets)

		for i := 1; i <= numSecrets; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				event := RotationEvent{
					Namespace: "default",
					Name:      fmt.Sprintf("perf-secret-%d", i),
					Type:      RotationTypeAWSCredentials,
					Metadata:  map[string]string{"old_access_key_id": fmt.Sprintf("OLDKEY%d", i)},
				}
				if err := ts.rotator.Rotate(ts.ctx, event); err != nil {
					errChan <- fmt.Errorf("rotation %d failed: %w", i, err)
				}
			}(i)
		}

		// Wait for all rotations to complete
		wg.Wait()
		close(errChan)
		totalDuration := time.Since(start)

		// Check for any errors
		var errors []error
		for err := range errChan {
			errors = append(errors, err)
		}
		assert.Empty(t, errors, "rotation errors occurred: %v", errors)

		// Verify performance metrics
		averageTimePerRotation := totalDuration.Milliseconds() / int64(numSecrets)
		assert.Less(t, averageTimePerRotation, int64(100),
			"average rotation time too high: %dms per rotation", averageTimePerRotation)

		// Verify all secrets were updated correctly
		for i := 1; i <= numSecrets; i++ {
			var secret corev1.Secret
			err := ts.client.Get(ts.ctx, ctrlclient.ObjectKey{
				Namespace: "default",
				Name:      fmt.Sprintf("perf-secret-%d", i),
			}, &secret)
			require.NoError(t, err)

			creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
			require.NotNil(t, creds)
			assert.Contains(t, creds.profiles["default"].accessKeyID, "NEWKEY")
			assert.Contains(t, creds.profiles["default"].secretAccessKey, "NEWSECRET")
		}
	})
}

func TestBackendAuthManager_RotatorFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := ctrlfake.NewClientBuilder().Build()
	kubeClient := kubefake.NewSimpleClientset()
	eventChan := make(chan RotationEvent, 10)
	errorChan := make(chan error, 10)
	doneChan := make(chan struct{})

	manager := NewBackendAuthManager(client, kubeClient, ctrl.Log.WithName("test"))
	manager.eventChan = eventChan
	manager.errorChan = errorChan

	// Start the manager in a goroutine
	go func() {
		manager.Start(ctx)
		close(doneChan)
	}()

	// Register a mock rotator that always fails
	mockRotator := &mockRotator{
		rotationType: RotationTypeAWSCredentials,
		rotateFunc: func(ctx context.Context, event RotationEvent) error {
			return fmt.Errorf("mock rotation error")
		},
	}
	manager.RegisterRotator(RotationTypeAWSCredentials, mockRotator)

	// Send a rotation event
	event := RotationEvent{
		Type: RotationTypeAWSCredentials,
		Name: "test-secret",
	}
	manager.RequestRotation(event)

	// Wait for error or timeout
	select {
	case err := <-errorChan:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mock rotation error")
	case <-time.After(time.Second):
		t.Fatal("no error received")
	}

	// Cleanup
	cancel()
	<-doneChan
}

type mockRotator struct {
	rotateFunc     func(ctx context.Context, event RotationEvent) error
	initializeFunc func(ctx context.Context, event RotationEvent) error
	rotationType   RotationType
}

func (m *mockRotator) Type() RotationType {
	return m.rotationType
}

func (m *mockRotator) Rotate(ctx context.Context, event RotationEvent) error {
	if m.rotateFunc != nil {
		return m.rotateFunc(ctx, event)
	}
	return nil
}

func (m *mockRotator) Initialize(ctx context.Context, event RotationEvent) error {
	if m.initializeFunc != nil {
		return m.initializeFunc(ctx, event)
	}
	return nil
}

type BackendAuthManager struct {
	client     ctrlclient.Client
	kubeClient *kubefake.Clientset
	logger     logr.Logger
	rotators   map[RotationType]Rotator
	eventChan  chan RotationEvent
	errorChan  chan error
}

func NewBackendAuthManager(client ctrlclient.Client, kubeClient *kubefake.Clientset, logger logr.Logger) *BackendAuthManager {
	return &BackendAuthManager{
		client:     client,
		kubeClient: kubeClient,
		logger:     logger,
		rotators:   make(map[RotationType]Rotator),
		eventChan:  make(chan RotationEvent, 100),
		errorChan:  make(chan error, 100),
	}
}

func (m *BackendAuthManager) RegisterRotator(rotationType RotationType, rotator Rotator) {
	m.rotators[rotationType] = rotator
}

func (m *BackendAuthManager) RequestRotation(event RotationEvent) {
	m.eventChan <- event
}

func (m *BackendAuthManager) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-m.eventChan:
			rotator, ok := m.rotators[event.Type]
			if !ok {
				m.errorChan <- fmt.Errorf("no rotator registered for type: %s", event.Type)
				continue
			}
			if err := rotator.Rotate(ctx, event); err != nil {
				m.errorChan <- err
			}
		}
	}
}
