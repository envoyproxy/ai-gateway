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
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

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

	rotator := &mockRotator{rotateType: backendauthrotators.RotationTypeAWSCredentials}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	assert.Equal(t, rotator, manager.rotators[backendauthrotators.RotationTypeAWSCredentials])
}

func TestBackendAuthManager_RequestRotation(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

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

	// Track rotation calls
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
		rotateFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
			return nil
		},
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	event := backendauthrotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      backendauthrotators.RotationTypeAWSCredentials,
		Metadata:  map[string]string{"key": "value"},
	}

	err = manager.RequestRotation(context.Background(), event)
	require.NoError(t, err)

	// Verify the event was published
	select {
	case publishedEvent := <-manager.RotationChannel():
		assert.Equal(t, event, publishedEvent)
	case <-time.After(time.Second):
		t.Fatal("rotation event was not published")
	}
}

func TestBackendAuthManager_Start(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

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

	// Track rotation calls
	rotationCalls := make(chan backendauthrotators.RotationEvent, 100)
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
		rotateFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
			rotationCalls <- event
			return nil
		},
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	// Start the manager
	errChan := make(chan error, 1)
	go func() {
		errChan <- manager.Start(ctx)
	}()

	// Test immediate rotation
	immediateEvent := backendauthrotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      backendauthrotators.RotationTypeAWSCredentials,
	}
	err = manager.RequestRotation(ctx, immediateEvent)
	require.NoError(t, err)

	select {
	case rotatedEvent := <-rotationCalls:
		assert.Equal(t, immediateEvent, rotatedEvent)
	case <-time.After(time.Second):
		t.Fatal("immediate rotation not processed")
	}

	// Test scheduled rotation
	scheduledEvent := backendauthrotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      backendauthrotators.RotationTypeAWSCredentials,
		Metadata: map[string]string{
			"rotate_at": time.Now().Add(100 * time.Millisecond).Format(time.RFC3339),
		},
	}
	err = manager.RequestRotation(ctx, scheduledEvent)
	require.NoError(t, err)

	// Wait for the scheduled rotation to occur
	select {
	case rotatedEvent := <-rotationCalls:
		assert.Equal(t, scheduledEvent.Namespace, rotatedEvent.Namespace)
		assert.Equal(t, scheduledEvent.Name, rotatedEvent.Name)
		assert.Equal(t, scheduledEvent.Type, rotatedEvent.Type)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("scheduled rotation was not executed")
	}

	// Test cancellation
	cancel()
	time.Sleep(100 * time.Millisecond) // Give time for goroutines to stop
}

func TestBackendAuthManager_RegisterRotator_Errors(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

	// Test registering duplicate rotator
	rotator1 := &mockRotator{rotateType: backendauthrotators.RotationTypeAWSCredentials}
	rotator2 := &mockRotator{rotateType: backendauthrotators.RotationTypeAWSCredentials}

	err := manager.RegisterRotator(rotator1)
	require.NoError(t, err)

	// Attempt to register second rotator of same type
	err = manager.RegisterRotator(rotator2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestBackendAuthManager_RequestRotation_ValidationErrors(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

	tests := []struct {
		name        string
		event       backendauthrotators.RotationEvent
		expectedErr string
	}{
		{
			name: "empty rotation type",
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
			},
			expectedErr: "rotation type cannot be empty",
		},
		{
			name: "unregistered rotator type",
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
				Type:      "unknown-type",
			},
			expectedErr: "no rotator registered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.RequestRotation(context.Background(), tt.event)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestBackendAuthManager_RotatorFailure(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

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

	// Skip the start event
	select {
	case <-eventChan:
		// Ignore the start event
	case <-time.After(time.Second):
		t.Fatal("no start event received")
	}

	// Verify error event is published
	select {
	case evt := <-eventChan:
		k8sEvent, ok := evt.(*corev1.Event)
		require.True(t, ok)
		assert.Equal(t, corev1.EventTypeWarning, k8sEvent.Type)
		assert.Contains(t, k8sEvent.Message, "simulated rotation failure")
	case <-time.After(time.Second):
		t.Fatal("no error event received")
	}
}

func TestBackendAuthManager_ConcurrentRotations(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

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

	// Track rotation calls with sync.Map to avoid race conditions
	var rotationCalls sync.Map
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
		rotateFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
			key := fmt.Sprintf("%s/%s", event.Namespace, event.Name)
			count := int64(1)
			if val, ok := rotationCalls.Load(key); ok {
				count = val.(int64) + 1
			}
			rotationCalls.Store(key, count)
			time.Sleep(100 * time.Millisecond) // Simulate work
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
		go func(i int) {
			defer wg.Done()
			event := backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      fmt.Sprintf("test-secret-%d", i),
				Type:      backendauthrotators.RotationTypeAWSCredentials,
			}
			err := manager.RequestRotation(ctx, event)
			assert.NoError(t, err)
		}(i)
	}

	// Wait for all rotations to complete
	wg.Wait()
	time.Sleep(500 * time.Millisecond) // Allow time for rotations to process

	// Verify each secret was rotated exactly once
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("default/test-secret-%d", i)
		val, ok := rotationCalls.Load(key)
		assert.True(t, ok)
		assert.Equal(t, int64(1), val.(int64))
	}
}

func TestBackendAuthManager_Cleanup(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

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

	// Schedule multiple rotations
	ctx := context.Background()
	futureTime := time.Now().Add(1 * time.Hour)

	for i := 0; i < 3; i++ {
		event := backendauthrotators.RotationEvent{
			Namespace: "default",
			Name:      fmt.Sprintf("test-secret-%d", i),
			Type:      backendauthrotators.RotationTypeAWSCredentials,
		}
		err := manager.ScheduleRotation(ctx, event, futureTime)
		require.NoError(t, err)
	}

	// Verify scheduled rotations exist
	count := 0
	manager.scheduledRotations.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 3, count)

	// Call cleanup
	manager.Cleanup()

	// Verify all scheduled rotations were removed
	count = 0
	manager.scheduledRotations.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 0, count)
}

func TestBackendAuthManager_RequestInitialization(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

	// Register rotator with initialization tracking
	initCalled := false
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
		initFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
			initCalled = true
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

	// Verify initialization was called
	assert.True(t, initCalled)

	// Verify initialization event was published
	select {
	case evt := <-eventChan:
		k8sEvent, ok := evt.(*corev1.Event)
		require.True(t, ok)
		assert.Equal(t, "BackendAuthRotationInitialization", k8sEvent.Reason)
	case <-time.After(time.Second):
		t.Fatal("no initialization event received")
	}
}

func TestBackendAuthManager_RequestInitialization_Errors(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()

	tests := []struct {
		name        string
		rotator     *mockRotator
		event       backendauthrotators.RotationEvent
		expectedErr string
		expectedMsg string
	}{
		{
			name: "initialization failure",
			rotator: &mockRotator{
				rotateType: backendauthrotators.RotationTypeAWSCredentials,
				initFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
					return fmt.Errorf("initialization failed")
				},
			},
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
				Type:      backendauthrotators.RotationTypeAWSCredentials,
			},
			expectedErr: "failed to initialize BackendAuth",
			expectedMsg: "failed to initialize BackendAuth for secret default/test-secret: initialization failed",
		},
		{
			name:    "no rotator registered",
			rotator: nil,
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
				Type:      backendauthrotators.RotationTypeAWSCredentials,
			},
			expectedErr: "no rotator found",
			expectedMsg: "no rotator found for secret default/test-secret: for type aws-credentials",
		},
		{
			name: "rotator type mismatch",
			rotator: &mockRotator{
				rotateType: backendauthrotators.RotationTypeAWSOIDC,
			},
			event: backendauthrotators.RotationEvent{
				Namespace: "default",
				Name:      "test-secret",
				Type:      backendauthrotators.RotationTypeAWSCredentials,
			},
			expectedErr: "no rotator found",
			expectedMsg: "no rotator found for secret default/test-secret: for type aws-credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset manager
			manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

			// Register rotator if provided
			if tt.rotator != nil {
				err := manager.RegisterRotator(tt.rotator)
				require.NoError(t, err)
			}

			// Request initialization
			err := manager.RequestInitialization(context.Background(), tt.event)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)

			// For initialization failure, we expect two events:
			// 1. Initialization event (Normal)
			// 2. Error event (Warning)
			if tt.name == "initialization failure" {
				// First event should be initialization
				select {
				case evt := <-eventChan:
					k8sEvent, ok := evt.(*corev1.Event)
					require.True(t, ok)
					assert.Equal(t, corev1.EventTypeNormal, k8sEvent.Type)
					assert.Equal(t, "BackendAuthRotationInitialization", k8sEvent.Reason)
				case <-time.After(time.Second):
					t.Fatal("no initialization event received")
				}

				// Second event should be the error
				select {
				case evt := <-eventChan:
					k8sEvent, ok := evt.(*corev1.Event)
					require.True(t, ok)
					assert.Equal(t, corev1.EventTypeWarning, k8sEvent.Type)
					assert.Contains(t, k8sEvent.Message, tt.expectedMsg)
				case <-time.After(time.Second):
					t.Fatal("no error event received")
				}
			} else {
				// For other cases, we expect only an error event
				select {
				case evt := <-eventChan:
					k8sEvent, ok := evt.(*corev1.Event)
					require.True(t, ok)
					assert.Equal(t, corev1.EventTypeWarning, k8sEvent.Type)
					assert.Contains(t, k8sEvent.Message, tt.expectedMsg)
				case <-time.After(time.Second):
					t.Fatal("no error event received")
				}
			}

			// Drain any remaining events
			select {
			case <-eventChan:
				// Ignore any additional events
			case <-time.After(100 * time.Millisecond):
				// No more events
			}
		})
	}
}

func TestBackendAuthManager_ScheduleRotation(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewBackendAuthManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

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

	// Track rotation calls
	rotationCalls := make(chan backendauthrotators.RotationEvent, 1)
	rotator := &mockRotator{
		rotateType: backendauthrotators.RotationTypeAWSCredentials,
		rotateFn: func(ctx context.Context, event backendauthrotators.RotationEvent) error {
			rotationCalls <- event
			return nil
		},
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	// Start the manager
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- manager.Start(ctx)
	}()

	// Schedule a rotation for the near future
	nearFutureTime := time.Now().Add(100 * time.Millisecond)
	event := backendauthrotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      backendauthrotators.RotationTypeAWSCredentials,
	}
	err = manager.ScheduleRotation(ctx, event, nearFutureTime)
	require.NoError(t, err)

	// Wait for the rotation to occur
	select {
	case rotatedEvent := <-rotationCalls:
		assert.Equal(t, event.Namespace, rotatedEvent.Namespace)
		assert.Equal(t, event.Name, rotatedEvent.Name)
		assert.Equal(t, event.Type, rotatedEvent.Type)
	case <-time.After(time.Second):
		t.Fatal("scheduled rotation was not executed")
	}

	// Verify no errors from the manager
	select {
	case err := <-errChan:
		require.NoError(t, err)
	default:
	}
}
