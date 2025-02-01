package controller

import (
	"context"
	"testing"
	"time"

	"github.com/envoyproxy/ai-gateway/internal/controller/token_rotators"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockRotator struct {
	rotateFn   func(ctx context.Context, event token_rotators.RotationEvent) error
	initFn     func(ctx context.Context, event token_rotators.RotationEvent) error
	rotateType token_rotators.RotationType
}

func (m *mockRotator) Rotate(ctx context.Context, event token_rotators.RotationEvent) error {
	if m.rotateFn != nil {
		return m.rotateFn(ctx, event)
	}
	return nil
}

func (m *mockRotator) Initialize(ctx context.Context, event token_rotators.RotationEvent) error {
	if m.initFn != nil {
		return m.initFn(ctx, event)
	}
	return nil
}

func (m *mockRotator) Type() token_rotators.RotationType {
	return m.rotateType
}

func TestTokenManager_RegisterRotator(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewTokenManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

	// Create test secret to simulate initialized token
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

	rotator := &mockRotator{rotateType: token_rotators.RotationTypeAWSCredentials}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	assert.Equal(t, rotator, manager.rotators[token_rotators.RotationTypeAWSCredentials])
}

func TestTokenManager_RequestRotation(t *testing.T) {
	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewTokenManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

	// Create test secret to simulate initialized token
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
		rotateType: token_rotators.RotationTypeAWSCredentials,
		rotateFn: func(ctx context.Context, event token_rotators.RotationEvent) error {
			return nil
		},
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	event := token_rotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      token_rotators.RotationTypeAWSCredentials,
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

func TestTokenManager_Start(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventChan := make(chan ConfigSinkEvent, 10)
	fakeClient := ctrlfake.NewClientBuilder().Build()
	manager := NewTokenManager(ctrl.Log.WithName("test"), eventChan, fakeClient)

	// Create test secret to simulate initialized token
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
	rotationCalls := make(chan token_rotators.RotationEvent, 100)
	rotator := &mockRotator{
		rotateType: token_rotators.RotationTypeAWSCredentials,
		rotateFn: func(ctx context.Context, event token_rotators.RotationEvent) error {
			rotationCalls <- event
			return nil
		},
	}
	err := manager.RegisterRotator(rotator)
	require.NoError(t, err)

	// Start the manager
	go manager.Start(ctx)

	// Test immediate rotation
	immediateEvent := token_rotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      token_rotators.RotationTypeAWSCredentials,
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
	scheduledEvent := token_rotators.RotationEvent{
		Namespace: "default",
		Name:      "test-secret",
		Type:      token_rotators.RotationTypeAWSCredentials,
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
