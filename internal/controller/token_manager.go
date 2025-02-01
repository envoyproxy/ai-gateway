package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// RotationType defines the type of rotation to be performed
type RotationType string

const (
	// RotationTypeAWSCredentials represents AWS IAM credentials rotation
	RotationTypeAWSCredentials RotationType = "aws-credentials"
	// RotationTypeAWSOIDC represents AWS OIDC token rotation
	RotationTypeAWSOIDC RotationType = "aws-oidc"
)

// RotationEvent represents a credential rotation event
type RotationEvent struct {
	// Namespace where the rotation should occur
	Namespace string
	// Name of the resource requiring rotation
	Name string
	// Type of rotation to perform
	Type RotationType
	// Metadata contains any additional data needed for rotation
	Metadata map[string]string
}

// Rotator defines the interface for credential rotators
type Rotator interface {
	// Rotate performs the credential rotation
	Rotate(ctx context.Context, event RotationEvent) error
	// GetType returns the type of rotator
	GetType() RotationType
}

// TokenManager manages credential rotation across different rotator implementations
type TokenManager struct {
	rotationChan chan RotationEvent
	rotators     map[RotationType]Rotator
	logger       logr.Logger
	mu           sync.RWMutex
	wg           sync.WaitGroup
	stopChan     chan struct{} // Channel to signal goroutines to stop
}

// NewTokenManager creates a new TokenManager instance
func NewTokenManager(logger logr.Logger) *TokenManager {
	return &TokenManager{
		rotationChan: make(chan RotationEvent),
		rotators:     make(map[RotationType]Rotator),
		logger:       logger,
		stopChan:     make(chan struct{}),
	}
}

// RegisterRotator registers a new rotator implementation
func (tm *TokenManager) RegisterRotator(r Rotator) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	rotationType := r.GetType()
	if _, exists := tm.rotators[rotationType]; exists {
		return fmt.Errorf("rotator for type %q already registered", rotationType)
	}

	tm.rotators[rotationType] = r
	return nil
}

// RequestRotation sends a rotation event to the rotation channel
func (tm *TokenManager) RequestRotation(ctx context.Context, event RotationEvent) error {
	// Check context first
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	tm.mu.RLock()
	_, exists := tm.rotators[event.Type]
	tm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no rotator registered for type %q", event.Type)
	}

	// Try to send the event, respecting both context and shutdown
	select {
	case tm.rotationChan <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.stopChan:
		return fmt.Errorf("token manager is shutting down")
	}
}

// Start begins processing rotation events
func (tm *TokenManager) Start(ctx context.Context) error {
	// Create a new context that we can cancel when stopping
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start processing events
	for {
		select {
		case event := <-tm.rotationChan:
			// Check if we're shutting down before starting new work
			select {
			case <-tm.stopChan:
				continue
			default:
			}

			tm.mu.RLock()
			rotator, exists := tm.rotators[event.Type]
			tm.mu.RUnlock()

			if !exists {
				tm.logger.Error(fmt.Errorf("no rotator found"), "failed to process rotation event",
					"type", event.Type,
					"namespace", event.Namespace,
					"name", event.Name)
				continue
			}

			tm.wg.Add(1)
			go func(e RotationEvent) {
				defer tm.wg.Done()
				if err := rotator.Rotate(ctx, e); err != nil {
					if err != context.Canceled {
						tm.logger.Error(err, "failed to rotate credentials",
							"type", e.Type,
							"namespace", e.Namespace,
							"name", e.Name)
					}
				}
			}(event)

		case <-ctx.Done():
			// Signal all goroutines to stop
			close(tm.stopChan)

			// Wait for all rotations to complete with a timeout
			done := make(chan struct{})
			go func() {
				tm.wg.Wait()
				close(done)
			}()

			select {
			case <-done:
				// All goroutines completed
				return ctx.Err()
			case <-time.After(30 * time.Second):
				return fmt.Errorf("timed out waiting for rotations to complete: %w", ctx.Err())
			}

		case <-tm.stopChan:
			// Additional stop condition for clean shutdown
			return ctx.Err()
		}
	}
}
