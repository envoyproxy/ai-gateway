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

// testSetup contains common test setup data
type testSetup struct {
	ctx        context.Context
	cancel     context.CancelFunc
	client     ctrlclient.Client
	kubeClient *kubefake.Clientset
	rotator    *AWSCredentialsRotator
}

// setupTest creates a new test environment
func setupTest() *testSetup {
	ctx, cancel := context.WithCancel(context.Background())
	client := ctrlfake.NewClientBuilder().Build()
	kubeClient := kubefake.NewSimpleClientset()

	return &testSetup{
		ctx:        ctx,
		cancel:     cancel,
		client:     client,
		kubeClient: kubeClient,
	}
}

// createTestSecret creates a test secret with given name and credentials
func (ts *testSetup) createTestSecret(t *testing.T, name string, keyID, secret string) {
	testSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: map[string][]byte{
			credentialsKey: []byte(fmt.Sprintf("[default]\naws_access_key_id = %s\naws_secret_access_key = %s", keyID, secret)),
		},
	}
	require.NoError(t, ts.client.Create(ts.ctx, testSecret))
}

// setupRotator creates a rotator with mock IAM operations
func (ts *testSetup) setupRotator(t *testing.T, mockIAM *mockIAMOperations) {
	rotator, err := NewAWSCredentialsRotator(ts.client, ts.kubeClient, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	rotator.IAMOps = mockIAM
	rotator.KeyDeletionDelay = 100 * time.Millisecond
	rotator.MinPropagationDelay = 10 * time.Millisecond
	ts.rotator = rotator
}

// Helper functions for creating common mock configurations
func newMockIAM() *mockIAMOperations {
	return &mockIAMOperations{
		createKeyFunc: func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
			return &iam.CreateAccessKeyOutput{
				AccessKey: &iamtypes.AccessKey{
					AccessKeyId:     aws.String("NEWKEY"),
					SecretAccessKey: aws.String("NEWSECRET"),
				},
			}, nil
		},
		deleteKeyFunc: func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			return &iam.DeleteAccessKeyOutput{}, nil
		},
	}
}

func newMockIAMWithError(err error) *mockIAMOperations {
	mock := newMockIAM()
	mock.createKeyFunc = func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
		return nil, err
	}
	return mock
}

func newMockIAMWithKeys(keyID, secret string) *mockIAMOperations {
	mock := newMockIAM()
	mock.createKeyFunc = func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
		return &iam.CreateAccessKeyOutput{
			AccessKey: &iamtypes.AccessKey{
				AccessKeyId:     aws.String(keyID),
				SecretAccessKey: aws.String(secret),
			},
		}, nil
	}
	return mock
}

func newMockIAMWithCounter(counter *int32) *mockIAMOperations {
	mock := newMockIAM()
	mock.createKeyFunc = func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
		id := atomic.AddInt32(counter, 1)
		return &iam.CreateAccessKeyOutput{
			AccessKey: &iamtypes.AccessKey{
				AccessKeyId:     aws.String(fmt.Sprintf("NEWKEY%d", id)),
				SecretAccessKey: aws.String(fmt.Sprintf("NEWSECRET%d", id)),
			},
		}, nil
	}
	return mock
}

// verifySecretCredentials verifies the credentials in a secret
func (ts *testSetup) verifySecretCredentials(t *testing.T, secretName, expectedKeyID, expectedSecret string) {
	var secret corev1.Secret
	err := ts.client.Get(ts.ctx, ctrlclient.ObjectKey{Namespace: "default", Name: secretName}, &secret)
	require.NoError(t, err)

	creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
	require.NotNil(t, creds)
	require.Contains(t, creds.profiles, "default")
	assert.Equal(t, expectedKeyID, creds.profiles["default"].accessKeyID)
	assert.Equal(t, expectedSecret, creds.profiles["default"].secretAccessKey)
}

func TestAWS_CredentialsRotator(t *testing.T) {
	t.Run("basic rotation", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET")
		ts.setupRotator(t, newMockIAMWithKeys("NEWKEY", "NEWSECRET"))

		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata:  map[string]string{"old_access_key_id": "OLDKEY"},
		}

		require.NoError(t, ts.rotator.Rotate(ts.ctx, event))
		ts.verifySecretCredentials(t, "test-secret", "NEWKEY", "NEWSECRET")
	})

	t.Run("error handling", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET")
		ts.setupRotator(t, newMockIAMWithError(fmt.Errorf("AWS API error")))

		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata:  map[string]string{"old_access_key_id": "OLDKEY"},
		}

		err := ts.rotator.Rotate(ts.ctx, event)
		assert.Error(t, err)
		ts.verifySecretCredentials(t, "test-secret", "OLDKEY", "OLDSECRET")
	})

	t.Run("concurrent rotations", func(t *testing.T) {
		ts := setupTest()
		defer ts.cancel()

		numSecrets := 3
		var createKeyCount int32

		// Create test secrets
		for i := 1; i <= numSecrets; i++ {
			ts.createTestSecret(t, fmt.Sprintf("test-secret-%d", i), fmt.Sprintf("OLDKEY%d", i), fmt.Sprintf("OLDSECRET%d", i))
		}

		ts.setupRotator(t, newMockIAMWithCounter(&createKeyCount))

		// Run concurrent rotations
		var wg sync.WaitGroup
		for i := 1; i <= numSecrets; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				event := RotationEvent{
					Namespace: "default",
					Name:      fmt.Sprintf("test-secret-%d", i),
					Type:      RotationTypeAWSCredentials,
					Metadata:  map[string]string{"old_access_key_id": fmt.Sprintf("OLDKEY%d", i)},
				}
				assert.NoError(t, ts.rotator.Rotate(ts.ctx, event))
			}(i)
		}
		wg.Wait()

		// Verify results
		assert.Equal(t, int32(numSecrets), atomic.LoadInt32(&createKeyCount))
		for i := 1; i <= numSecrets; i++ {
			var secret corev1.Secret
			err := ts.client.Get(ts.ctx, ctrlclient.ObjectKey{
				Namespace: "default",
				Name:      fmt.Sprintf("test-secret-%d", i),
			}, &secret)
			require.NoError(t, err)
			assert.Contains(t, string(secret.Data[credentialsKey]), "NEWKEY")
		}
	})
}

func TestAWS_OIDCRotator(t *testing.T) {
	ts := setupTest()
	defer ts.cancel()

	rotationChan := make(chan RotationEvent)
	scheduleChan := make(chan RotationEvent, 100)

	ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET")

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
	ts.verifySecretCredentials(t, "test-secret", "STSKEY", "STSSECRET")

	// Verify scheduled rotation
	select {
	case scheduledEvent := <-scheduleChan:
		assert.Equal(t, event.Type, scheduledEvent.Type)
		assert.NotEmpty(t, scheduledEvent.Metadata["rotate_at"])
	case <-time.After(time.Second):
		t.Fatal("no rotation event was scheduled")
	}
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
	ts := setupTest()
	defer ts.cancel()

	t.Run("successful initialization", func(t *testing.T) {
		ts.setupRotator(t, newMockIAMWithKeys("INITKEY", "INITSECRET"))

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

		var createdSecret corev1.Secret
		err = ts.client.Get(ts.ctx, ctrlclient.ObjectKey{Namespace: "default", Name: "init-secret"}, &createdSecret)
		require.NoError(t, err)

		creds := parseAWSCredentialsFile(string(createdSecret.Data[credentialsKey]))
		require.NotNil(t, creds)
		require.Contains(t, creds.profiles, "custom")
		assert.Equal(t, "INITKEY", creds.profiles["custom"].accessKeyID)
		assert.Equal(t, "INITSECRET", creds.profiles["custom"].secretAccessKey)
		assert.Equal(t, "us-west-2", creds.profiles["custom"].region)
	})

	t.Run("initialization with AWS API error", func(t *testing.T) {
		ts.setupRotator(t, newMockIAMWithError(fmt.Errorf("AWS API error: limit exceeded")))

		event := RotationEvent{
			Namespace: "default",
			Name:      "error-secret",
			Type:      RotationTypeAWSCredentials,
		}

		err := ts.rotator.Initialize(ts.ctx, event)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create access key")
	})
}

func TestAWS_CredentialsRotator_ErrorHandling(t *testing.T) {
	ts := setupTest()
	defer ts.cancel()

	ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET")

	t.Run("handle AWS API errors during rotation", func(t *testing.T) {
		ts.setupRotator(t, newMockIAMWithError(fmt.Errorf("AWS API error: service unavailable")))

		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata:  map[string]string{"old_access_key_id": "OLDKEY"},
		}

		err := ts.rotator.Rotate(ts.ctx, event)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create new access key")

		ts.verifySecretCredentials(t, "test-secret", "OLDKEY", "OLDSECRET")
	})

	t.Run("handle key deletion errors", func(t *testing.T) {
		deleteError := make(chan error, 1)
		mockIAM := newMockIAMWithKeys("NEWKEY", "NEWSECRET")
		mockIAM.deleteKeyFunc = func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			err := fmt.Errorf("AWS API error: delete failed")
			deleteError <- err
			return nil, err
		}

		ts.setupRotator(t, mockIAM)

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
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "delete failed")
		case <-time.After(time.Second):
			t.Fatal("deletion error not received")
		}

		ts.verifySecretCredentials(t, "test-secret", "NEWKEY", "NEWSECRET")
	})
}

func TestAWS_CredentialsRotator_Concurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := ctrlfake.NewClientBuilder().Build()
	kubeClient := kubefake.NewSimpleClientset()

	// Create test secrets
	for i := 1; i <= 3; i++ {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("test-secret-%d", i),
				Namespace: "default",
			},
			Data: map[string][]byte{
				credentialsKey: []byte(fmt.Sprintf("[default]\naws_access_key_id = OLDKEY%d\naws_secret_access_key = OLDSECRET%d", i, i)),
			},
		}
		require.NoError(t, client.Create(ctx, secret))
	}

	t.Run("concurrent rotations", func(t *testing.T) {
		var createKeyCount int32
		var deleteKeyCount int32
		mockIAM := &mockIAMOperations{
			createKeyFunc: func(ctx context.Context, params *iam.CreateAccessKeyInput, optFns ...func(*iam.Options)) (*iam.CreateAccessKeyOutput, error) {
				atomic.AddInt32(&createKeyCount, 1)
				id := atomic.LoadInt32(&createKeyCount)
				return &iam.CreateAccessKeyOutput{
					AccessKey: &iamtypes.AccessKey{
						AccessKeyId:     aws.String(fmt.Sprintf("NEWKEY%d", id)),
						SecretAccessKey: aws.String(fmt.Sprintf("NEWSECRET%d", id)),
					},
				}, nil
			},
			deleteKeyFunc: func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
				atomic.AddInt32(&deleteKeyCount, 1)
				return &iam.DeleteAccessKeyOutput{}, nil
			},
		}

		rotator, err := NewAWSCredentialsRotator(client, kubeClient, ctrl.Log.WithName("test"))
		require.NoError(t, err)
		rotator.IAMOps = mockIAM
		rotator.KeyDeletionDelay = 100 * time.Millisecond
		rotator.MinPropagationDelay = 10 * time.Millisecond

		var wg sync.WaitGroup
		for i := 1; i <= 3; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				event := RotationEvent{
					Namespace: "default",
					Name:      fmt.Sprintf("test-secret-%d", i),
					Type:      RotationTypeAWSCredentials,
					Metadata:  map[string]string{"old_access_key_id": fmt.Sprintf("OLDKEY%d", i)},
				}
				err := rotator.Rotate(ctx, event)
				assert.NoError(t, err)
			}(i)
		}

		wg.Wait()
		time.Sleep(200 * time.Millisecond) // Wait for deletions to complete

		// Verify all operations completed
		assert.Equal(t, int32(3), atomic.LoadInt32(&createKeyCount))
		assert.Equal(t, int32(3), atomic.LoadInt32(&deleteKeyCount))

		// Verify each secret has unique credentials
		usedKeys := make(map[string]bool)
		for i := 1; i <= 3; i++ {
			var secret corev1.Secret
			err := client.Get(ctx, ctrlclient.ObjectKey{
				Namespace: "default",
				Name:      fmt.Sprintf("test-secret-%d", i),
			}, &secret)
			require.NoError(t, err)

			creds := parseAWSCredentialsFile(string(secret.Data[credentialsKey]))
			require.NotNil(t, creds)
			key := creds.profiles["default"].accessKeyID
			assert.False(t, usedKeys[key], "duplicate key found: %s", key)
			usedKeys[key] = true
		}
	})
}

func TestAWS_CredentialsRotator_Security(t *testing.T) {
	ts := setupTest()
	defer ts.cancel()

	ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET")

	t.Run("verify old credentials are securely deleted", func(t *testing.T) {
		var deletedKeyID string
		var deletedKeyMutex sync.Mutex

		mockIAM := newMockIAM()
		mockIAM.deleteKeyFunc = func(ctx context.Context, params *iam.DeleteAccessKeyInput, optFns ...func(*iam.Options)) (*iam.DeleteAccessKeyOutput, error) {
			deletedKeyMutex.Lock()
			deletedKeyID = *params.AccessKeyId
			deletedKeyMutex.Unlock()
			return &iam.DeleteAccessKeyOutput{}, nil
		}

		ts.setupRotator(t, mockIAM)

		event := RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      RotationTypeAWSCredentials,
			Metadata:  map[string]string{"old_access_key_id": "OLDKEY"},
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

	ts.createTestSecret(t, "test-secret", "OLDKEY", "OLDSECRET")

	t.Run("measure rotation time", func(t *testing.T) {
		ts.setupRotator(t, newMockIAMWithKeys("NEWKEY", "NEWSECRET"))

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

		ts.verifySecretCredentials(t, "test-secret", "NEWKEY", "NEWSECRET")
	})

	t.Run("handle high volume of rotation requests", func(t *testing.T) {
		var createKeyCount int32
		ts.setupRotator(t, newMockIAMWithCounter(&createKeyCount))

		// Create multiple secrets
		numSecrets := 10
		for i := 1; i <= numSecrets; i++ {
			ts.createTestSecret(t, fmt.Sprintf("perf-secret-%d", i), fmt.Sprintf("OLDKEY%d", i), fmt.Sprintf("OLDSECRET%d", i))
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
					errChan <- fmt.Errorf("rotation %d failed: %v", i, err)
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
