package controller

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/envoyproxy/ai-gateway/internal/controller/backendauthrotators"
)

type mockRotator struct {
	rotateFn   func(ctx context.Context, event backendauthrotators.RotationEvent) error
	initFn     func(ctx context.Context, event backendauthrotators.RotationEvent) error
	rotateType backendauthrotators.RotationType
}

func (m *mockRotator) Rotate(ctx context.Context, event backendauthrotators.RotationEvent) error {
	if m.rotateFn != nil {
		return m.rotateFn(ctx, event)
	}
	return nil
}

func (m *mockRotator) Initialize(ctx context.Context, event backendauthrotators.RotationEvent) error {
	if m.initFn != nil {
		return m.initFn(ctx, event)
	}
	return nil
}

func (m *mockRotator) Type() backendauthrotators.RotationType {
	return m.rotateType
}

func TestBackendAuthManager_RegisterRotator(t *testing.T) {
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

	// Create test secret to simulate initialized BackendAuth
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"credentials": []byte("test"),
		},
	}
	require.NoError(t, fakeClient.Create(context.Background(), secret))

	// Register rotator
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	// Verify rotator is registered
	assert.Len(t, manager.rotators, 1)
	assert.Equal(t, rotator, manager.rotators[backendauthrotators.RotationTypeAWSCredentials])

	// Try to register same type again
	err = manager.RegisterRotator(rotator)
	assert.Error(t, err)
}

func TestBackendAuthManager_RequestRotation(t *testing.T) {
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

	// Create test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"credentials": []byte("test"),
		},
	}
	require.NoError(t, fakeClient.Create(context.Background(), secret))

	// Register rotator
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	// Start manager
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errChan := make(chan error, 1)
	go func() {
		errChan <- manager.Start(ctx)
	}()

	// Request rotation
	event := backendauthrotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      backendauthrotators.RotationTypeAWSCredentials,
	}
	err = manager.RequestRotation(ctx, event)
	require.NoError(t, err)

	// Wait for rotation to complete
	time.Sleep(100 * time.Millisecond)
}

func TestBackendAuthManager_Start(t *testing.T) {
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

	// Create test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"credentials": []byte("test"),
		},
	}
	require.NoError(t, fakeClient.Create(context.Background(), secret))

	// Register rotator
	var rotationCount int
	var mu sync.Mutex
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
		rotateFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
			mu.Lock()
			rotationCount++
			mu.Unlock()
			return nil
		},
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	// Start manager
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errChan := make(chan error, 1)
	go func() {
		errChan <- manager.Start(ctx)
	}()

	// Request multiple rotations
	for i := 0; i < 3; i++ {
		event := backendauthrotators.RotationEvent{
			Namespace: "default",
			Name:      "test-secret",
			Type:      backendauthrotators.RotationTypeAWSCredentials,
		}
		err = manager.RequestRotation(ctx, event)
		require.NoError(t, err)
	}

	// Wait for rotations to complete
	time.Sleep(100 * time.Millisecond)

	// Verify rotations occurred
	mu.Lock()
	assert.Equal(t, 3, rotationCount)
	mu.Unlock()
}

func TestBackendAuthManager_RegisterRotator_Errors(t *testing.T) {
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

	// Register first rotator
	rotator1 := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
	}
	err := manager.RegisterRotator(rotator1)
	require.NoError(t, err)

	// Try to register another rotator with same type
	rotator2 := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
	}
	err = manager.RegisterRotator(rotator2)
	assert.Error(t, err)
}

func TestBackendAuthManager_RequestRotation_ValidationErrors(t *testing.T) {
	tests := []struct {
		name         string
		event        backendauthrotators.RotationEvent
		setupRotator bool
	}{
		{
			name: "empty rotation type",
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
			},
			setupRotator: false,
		},
		{
			name: "unregistered rotator type",
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
				Type:      backendauthrotators.RotationTypeAWSCredentials,
			},
			setupRotator: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := ctrlfake.NewClientBuilder().Build()
			manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

			if tc.setupRotator {
				rotator := &mockRotator{
					rotateType: backendauthrotators.RotationTypeAWSCredentials,
				}
				err := manager.RegisterRotator(rotator)
				require.NoError(t, err)
			}

			err := manager.RequestRotation(context.Background(), tc.event)
			assert.Error(t, err)
		})
	}
}

func TestBackendAuthManager_RotatorFailure(t *testing.T) {
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

	// Create test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"credentials": []byte("test"),
		},
	}
	require.NoError(t, fakeClient.Create(context.Background(), secret))

	// Register rotator that will fail
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
		rotateFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
			return fmt.Errorf("simulated rotation failure")
		},
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	// Start manager
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errChan := make(chan error, 1)
	go func() {
		errChan <- manager.Start(ctx)
	}()

	// Request rotation
	event := backendauthrotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      backendauthrotators.RotationTypeAWSCredentials,
	}
	err = manager.RequestRotation(ctx, event)
	require.NoError(t, err)

	// Wait for rotation to fail
	time.Sleep(100 * time.Millisecond)
}

func TestBackendAuthManager_ConcurrentRotations(t *testing.T) {
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

	// Create test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"credentials": []byte("test"),
		},
	}
	require.NoError(t, fakeClient.Create(context.Background(), secret))

	// Register rotator with delay
	var rotationCount int
	var mu sync.Mutex
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
		rotateFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
			time.Sleep(50 * time.Millisecond) // Add delay to test concurrency
			mu.Lock()
			rotationCount++
			mu.Unlock()
			return nil
		},
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	// Start manager
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errChan := make(chan error, 1)
	go func() {
		errChan <- manager.Start(ctx)
	}()

	// Request multiple concurrent rotations
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event := backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
				Type:      backendauthrotators.RotationTypeAWSCredentials,
			}
			err := manager.RequestRotation(ctx, event)
			require.NoError(t, err)
		}()
	}

	// Wait for all requests to complete
	wg.Wait()

	// Wait for rotations to complete
	time.Sleep(300 * time.Millisecond)

	// Verify all rotations occurred
	mu.Lock()
	assert.Equal(t, 5, rotationCount)
	mu.Unlock()
}

func TestBackendAuthManager_Cleanup(t *testing.T) {
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

	// Schedule some rotations
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		event := backendauthrotators.RotationEvent{
			Namespace: "default",
			Name:      fmt.Sprintf("test-secret-%d", i),
			Type:      backendauthrotators.RotationTypeAWSCredentials,
		}
		err := manager.ScheduleRotation(ctx, event, time.Now().Add(time.Hour))
		require.NoError(t, err)
	}

	// Verify rotations are scheduled
	var count int
	manager.scheduledRotations.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 3, count)

	// Cleanup
	manager.Cleanup()

	// Verify all rotations are cleaned up
	count = 0
	manager.scheduledRotations.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 0, count)
}

func TestBackendAuthManager_RequestInitialization(t *testing.T) {
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

	// Register rotator
	var initCount int
	var mu sync.Mutex
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
		initFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
			mu.Lock()
			initCount++
			mu.Unlock()
			return nil
		},
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	// Request initialization
	event := backendauthrotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      backendauthrotators.RotationTypeAWSCredentials,
	}
	err = manager.RequestInitialization(context.Background(), event)
	require.NoError(t, err)

	// Verify initialization occurred
	mu.Lock()
	assert.Equal(t, 1, initCount)
	mu.Unlock()
}

func TestBackendAuthManager_RequestInitialization_Errors(t *testing.T) {
	tests := []struct {
		name         string
		event        backendauthrotators.RotationEvent
		setupRotator bool
		initError    error
	}{
		{
			name: "initialization failure",
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
				Type:      backendauthrotators.RotationTypeAWSCredentials,
			},
			setupRotator: true,
			initError:    fmt.Errorf("simulated initialization failure"),
		},
		{
			name: "no rotator registered",
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
				Type:      backendauthrotators.RotationTypeAWSCredentials,
			},
			setupRotator: false,
		},
		{
			name: "rotator type mismatch",
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
				Type:      backendauthrotators.RotationTypeAWSOIDC,
			},
			setupRotator: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := ctrlfake.NewClientBuilder().Build()
			manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

			if tc.setupRotator {
				rotator := &mockRotator{
					rotateType: backendauthrotators.RotationTypeAWSCredentials,
					initFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
						return tc.initError
					},
				}
				err := manager.RegisterRotator(rotator)
				require.NoError(t, err)
			}

			err := manager.RequestInitialization(context.Background(), tc.event)
			assert.Error(t, err)
		})
	}
}

func TestBackendAuthManager_ScheduleRotation(t *testing.T) {
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), fakeClient)

	// Schedule a rotation
	ctx := context.Background()
	event := backendauthrotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      backendauthrotators.RotationTypeAWSCredentials,
	}
	err := manager.ScheduleRotation(ctx, event, time.Now().Add(time.Hour))
	require.NoError(t, err)

	// Verify rotation is scheduled
	key := manager.ScheduledRotationKey(event.Namespace, event.Name)
	val, ok := manager.scheduledRotations.Load(key)
	assert.True(t, ok)
	assert.NotNil(t, val)

	// Schedule another rotation for same resource
	err = manager.ScheduleRotation(ctx, event, time.Now().Add(2*time.Hour))
	require.NoError(t, err)

	// Verify only one rotation is scheduled
	var count int
	manager.scheduledRotations.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 1, count)
}
