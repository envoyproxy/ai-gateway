package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
)

type mockRotator struct {
	rotateFunc func(ctx context.Context, event RotationEvent) error
	rotateType RotationType
}

func (m *mockRotator) Rotate(ctx context.Context, event RotationEvent) error {
	if m.rotateFunc != nil {
		return m.rotateFunc(ctx, event)
	}
	return nil
}

func (m *mockRotator) GetType() RotationType {
	return m.rotateType
}

func TestTokenManager_RegisterRotator(t *testing.T) {
	tm := NewTokenManager(ctrl.Log.WithName("test"))

	t.Run("successful registration", func(t *testing.T) {
		rotator := &mockRotator{rotateType: RotationTypeAWSCredentials}
		err := tm.RegisterRotator(rotator)
		require.NoError(t, err)
	})

	t.Run("duplicate registration", func(t *testing.T) {
		rotator := &mockRotator{rotateType: RotationTypeAWSCredentials}
		err := tm.RegisterRotator(rotator)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already registered")
	})
}

func TestTokenManager_RequestRotation(t *testing.T) {
	tm := NewTokenManager(ctrl.Log.WithName("test"))
	ctx := context.Background()

	t.Run("unknown rotator type", func(t *testing.T) {
		event := RotationEvent{Type: "unknown"}
		err := tm.RequestRotation(ctx, event)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no rotator registered")
	})

	t.Run("successful request", func(t *testing.T) {
		rotated := make(chan struct{})
		startErr := make(chan error, 1)
		rotator := &mockRotator{
			rotateType: RotationTypeAWSCredentials,
			rotateFunc: func(ctx context.Context, event RotationEvent) error {
				close(rotated)
				return nil
			},
		}
		err := tm.RegisterRotator(rotator)
		require.NoError(t, err)

		// Start the manager
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			startErr <- tm.Start(ctx)
		}()

		event := RotationEvent{Type: RotationTypeAWSCredentials}
		err = tm.RequestRotation(ctx, event)
		require.NoError(t, err)

		// Wait for rotation or timeout
		select {
		case <-rotated:
			// Success
		case <-time.After(time.Second):
			t.Fatal("rotation did not occur within timeout")
		}

		// Cancel and verify shutdown
		cancel()
		select {
		case err := <-startErr:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("manager did not shut down within timeout")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		rotator := &mockRotator{rotateType: RotationTypeAWSOIDC}
		err := tm.RegisterRotator(rotator)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		event := RotationEvent{Type: RotationTypeAWSOIDC}
		err = tm.RequestRotation(ctx, event)
		require.Error(t, err)
		assert.Equal(t, context.Canceled, err)
	})
}

func TestTokenManager_Start(t *testing.T) {
	tm := NewTokenManager(ctrl.Log.WithName("test"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rotated := make(chan struct{})
	startErr := make(chan error, 1)
	rotator := &mockRotator{
		rotateType: RotationTypeAWSCredentials,
		rotateFunc: func(ctx context.Context, event RotationEvent) error {
			close(rotated)
			return nil
		},
	}

	err := tm.RegisterRotator(rotator)
	require.NoError(t, err)

	// Start the manager in a goroutine
	go func() {
		startErr <- tm.Start(ctx)
	}()

	// Request a rotation
	event := RotationEvent{Type: RotationTypeAWSCredentials}
	err = tm.RequestRotation(ctx, event)
	require.NoError(t, err)

	// Wait for rotation or timeout
	select {
	case <-rotated:
		// Success
	case <-time.After(time.Second):
		t.Fatal("rotation did not occur within timeout")
	}

	// Test cleanup
	t.Run("cleanup on context cancel", func(t *testing.T) {
		// Create a slow rotation that will be interrupted
		slowRotated := make(chan struct{})
		slowRotator := &mockRotator{
			rotateType: RotationTypeAWSOIDC,
			rotateFunc: func(ctx context.Context, event RotationEvent) error {
				defer close(slowRotated)
				select {
				case <-time.After(2 * time.Second):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}

		err := tm.RegisterRotator(slowRotator)
		require.NoError(t, err)

		// Request the slow rotation
		event := RotationEvent{Type: RotationTypeAWSOIDC}
		err = tm.RequestRotation(ctx, event)
		require.NoError(t, err)

		// Cancel the context quickly
		cancel()

		// Verify the rotation was interrupted
		select {
		case <-slowRotated:
			// Rotation completed or was cancelled
		case <-time.After(3 * time.Second):
			t.Fatal("cleanup did not complete within timeout")
		}

		// Verify the manager shut down cleanly
		select {
		case err := <-startErr:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("manager did not shut down within timeout")
		}
	})
}
