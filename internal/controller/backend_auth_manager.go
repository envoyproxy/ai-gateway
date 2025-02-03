package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/envoyproxy/ai-gateway/internal/controller/backendauthrotators"
)

// RotationType defines the type of rotation to be performed
type RotationType string

const (
	// RotationTypeAWSCredentials represents AWS IAM credentials rotation
	RotationTypeAWSCredentials RotationType = "aws-credentials"
	// RotationTypeAWSOIDC represents AWS OIDC BackendAuth rotation
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
	// Initialize performs the initial BackendAuth retrieval
	Initialize(ctx context.Context, event RotationEvent) error
	// Rotate performs the credential rotation
	Rotate(ctx context.Context, event RotationEvent) error
	// Type returns the type of rotator
	Type() RotationType
}

// BackendAuthManager manages credential rotation across different rotator implementations
type BackendAuthManager struct {
	rotationChan chan backendauthrotators.RotationEvent
	rotators     map[backendauthrotators.RotationType]backendauthrotators.Rotator
	logger       logr.Logger
	mu           sync.RWMutex
	wg           sync.WaitGroup
	stopChan     chan struct{} // Channel to signal goroutines to stop
	// scheduledRotations tracks scheduled rotations by namespace/name
	scheduledRotations sync.Map
	// rotationWindow is how long before expiry to rotate credentials
	rotationWindow time.Duration
	// client is used for Kubernetes API operations
	client client.Client
}

// scheduledRotation represents a scheduled BackendAuth rotation
type scheduledRotation struct {
	timer    *time.Timer
	cancelFn context.CancelFunc
	event    backendauthrotators.RotationEvent
}

// NewBackendAuthManager creates a new BackendAuth manager
func NewBackendAuthManager(logger logr.Logger, client client.Client) *BackendAuthManager {
	return &BackendAuthManager{
		logger:         logger,
		rotators:       make(map[backendauthrotators.RotationType]backendauthrotators.Rotator),
		rotationChan:   make(chan backendauthrotators.RotationEvent, 100), // Buffer size of 100
		stopChan:       make(chan struct{}),
		client:         client,
		rotationWindow: 5 * time.Minute, // Default to rotating 5 minutes before expiry
	}
}

// RegisterRotator registers a new rotator implementation
func (tm *BackendAuthManager) RegisterRotator(r backendauthrotators.Rotator) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	rotationType := r.Type()
	if _, exists := tm.rotators[rotationType]; exists {
		return fmt.Errorf("rotator for type %q already registered", rotationType)
	}

	tm.rotators[rotationType] = r
	return nil
}

// RotationChannel returns a channel that will receive rotation events
func (tm *BackendAuthManager) RotationChannel() <-chan backendauthrotators.RotationEvent {
	return tm.rotationChan
}

// isBackendAuthInitialized checks if a BackendAuth has been initialized by verifying if its secret exists
func (tm *BackendAuthManager) isBackendAuthInitialized(event backendauthrotators.RotationEvent) bool {
	secret := &corev1.Secret{}
	err := tm.client.Get(context.Background(), client.ObjectKey{
		Namespace: event.Namespace,
		Name:      event.Name,
	}, secret)
	return err == nil && len(secret.Data) > 0
}

// validateRotationEvent validates that the rotation event is valid and a rotator exists for its type
func (tm *BackendAuthManager) validateRotationEvent(event backendauthrotators.RotationEvent) (bool, error) {
	if event.Type == "" {
		return false, fmt.Errorf("rotation type cannot be empty")
	}
	return true, nil
}

// checkRotatorExists checks if a rotator exists for the given type
func (tm *BackendAuthManager) checkRotatorExists(rotationType backendauthrotators.RotationType) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	_, exists := tm.rotators[rotationType]
	return exists
}

// handleError logs an error and returns a formatted error
func (tm *BackendAuthManager) handleError(event backendauthrotators.RotationEvent, errMsg string, err error) error {
	// Include namespace and name in the error message
	contextMsg := fmt.Sprintf("%s for secret %s/%s", errMsg, event.Namespace, event.Name)

	// Log the error with structured fields
	tm.logger.Error(err, contextMsg,
		"namespace", event.Namespace,
		"name", event.Name,
		"type", event.Type)

	if err != nil {
		return fmt.Errorf("%s: %w", contextMsg, err)
	}
	return fmt.Errorf("%s", contextMsg)
}

// RequestRotation requests a rotation for a secret. This is a non-blocking operation.
func (tm *BackendAuthManager) RequestRotation(ctx context.Context, event backendauthrotators.RotationEvent) error {
	// First validate the event structure without locks
	if valid, err := tm.validateRotationEvent(event); !valid {
		return tm.handleError(event, "invalid rotation event", err)
	}

	// Check if rotator exists
	if !tm.checkRotatorExists(event.Type) {
		return tm.handleError(event, "no rotator registered", fmt.Errorf("for type %q", event.Type))
	}

	// Use a mutex to make initialization check and request atomic
	tm.mu.Lock()
	needsInit := !tm.isBackendAuthInitialized(event)
	if needsInit {
		if err := tm.RequestInitialization(ctx, event); err != nil {
			tm.mu.Unlock()
			return err
		}
	}
	tm.mu.Unlock()

	tm.logger.Info("starting rotation",
		"namespace", event.Namespace,
		"name", event.Name,
		"type", event.Type)

	// Try non-blocking sends to rotation channel
	select {
	case tm.rotationChan <- event:
		// Successfully sent to rotation channel
	default:
		return tm.handleError(event, "rotation channel is full", nil)
	}

	return nil
}

// ScheduledRotationKey returns a consistent key format for scheduled rotations
func (tm *BackendAuthManager) ScheduledRotationKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// createScheduledRotation creates and stores a new scheduled rotation
func (tm *BackendAuthManager) createScheduledRotation(ctx context.Context, event backendauthrotators.RotationEvent, rotateAt time.Time) (*scheduledRotation, context.CancelFunc) {
	rotationCtx, cancel := context.WithCancel(ctx)

	timer := time.AfterFunc(time.Until(rotateAt), func() {
		if err := tm.RequestRotation(rotationCtx, event); err != nil {
			tm.logger.Error(err, "scheduled rotation failed",
				"namespace", event.Namespace,
				"name", event.Name,
				"rotateAt", rotateAt)
		}
	})

	sr := &scheduledRotation{
		timer:    timer,
		cancelFn: cancel,
		event:    event,
	}

	key := tm.ScheduledRotationKey(event.Namespace, event.Name)
	tm.scheduledRotations.Store(key, sr)

	return sr, cancel
}

// ScheduleRotation schedules a rotation to occur at a specific time
func (tm *BackendAuthManager) ScheduleRotation(ctx context.Context, event backendauthrotators.RotationEvent, rotateAt time.Time) error {
	// Cancel any existing scheduled rotation for this resource
	tm.cancelExistingRotation(event.Namespace, event.Name)

	// If we're already past or very close to the rotation time, trigger immediately
	if time.Until(rotateAt) < time.Second {
		if err := tm.RequestRotation(ctx, event); err != nil {
			return fmt.Errorf("failed to request immediate rotation: %w", err)
		}
		return nil
	}

	_, _ = tm.createScheduledRotation(ctx, event, rotateAt)

	tm.logger.Info("scheduled rotation",
		"namespace", event.Namespace,
		"name", event.Name,
		"rotateAt", rotateAt)
	return nil
}

// ScheduleNextRotation schedules the next rotation based on expiry time
func (tm *BackendAuthManager) ScheduleNextRotation(ctx context.Context, event backendauthrotators.RotationEvent, expiry time.Time) error {
	rotateAt := expiry.Add(-tm.rotationWindow)
	return tm.ScheduleRotation(ctx, event, rotateAt)
}

// cancelExistingRotation cancels any existing scheduled rotation for the given resource
func (tm *BackendAuthManager) cancelExistingRotation(namespace, name string) {
	key := tm.ScheduledRotationKey(namespace, name)
	if val, ok := tm.scheduledRotations.Load(key); ok {
		if sr, ok := val.(*scheduledRotation); ok {
			sr.timer.Stop()
			if sr.cancelFn != nil {
				sr.cancelFn()
			}
		}
		tm.scheduledRotations.Delete(key)
	}
}

// Cleanup cancels all scheduled rotations
func (tm *BackendAuthManager) Cleanup() {
	tm.scheduledRotations.Range(func(key, value interface{}) bool {
		if sr, ok := value.(*scheduledRotation); ok {
			sr.timer.Stop()
			if sr.cancelFn != nil {
				sr.cancelFn()
			}
		}
		tm.scheduledRotations.Delete(key)
		return true
	})
}

// Start begins processing rotation events
func (tm *BackendAuthManager) Start(ctx context.Context) error {
	// Create a new context that we can cancel when stopping
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start processing events
	for {
		select {
		case event := <-tm.rotationChan:
			tm.logger.Info("processing rotation event",
				"namespace", event.Namespace,
				"name", event.Name,
				"type", event.Type)

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
				if err := tm.handleError(event, "no rotator found", fmt.Errorf("for type %s", event.Type)); err != nil {
					tm.logger.Error(err, "failed to handle error for missing rotator",
						"namespace", event.Namespace,
						"name", event.Name,
						"type", event.Type)
				}
				continue
			}

			tm.wg.Add(1)
			go func(e backendauthrotators.RotationEvent) {
				defer tm.wg.Done()
				if err := rotator.Rotate(ctx, e); err != nil {
					if !errors.Is(err, context.Canceled) {
						if handleErr := tm.handleError(e, "failed to rotate credentials", err); handleErr != nil {
							tm.logger.Error(handleErr, "failed to handle rotation error",
								"namespace", e.Namespace,
								"name", e.Name,
								"type", e.Type)
						}
					}
				} else {
					tm.logger.Info("rotation completed successfully",
						"namespace", e.Namespace,
						"name", e.Name,
						"type", e.Type)
				}
			}(event)

		case <-ctx.Done():
			// Signal all goroutines to stop
			close(tm.stopChan)

			// Cancel all scheduled rotations
			tm.Cleanup()

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

// RequestInitialization handles the initialization of BackendAuths
func (tm *BackendAuthManager) RequestInitialization(ctx context.Context, event backendauthrotators.RotationEvent) error {
	tm.logger.Info("starting BackendAuth initialization",
		"namespace", event.Namespace,
		"name", event.Name,
		"type", event.Type)

	// Get the rotator for this type
	r, ok := tm.rotators[event.Type]
	if !ok {
		return tm.handleError(event, "no rotator found", fmt.Errorf("for type %s", event.Type))
	}

	// Verify the rotator type matches the event type
	if r.Type() != event.Type {
		return tm.handleError(event, "rotator type mismatch", fmt.Errorf("rotator type %s does not match event type %s", r.Type(), event.Type))
	}

	// Perform initialization
	if err := r.Initialize(ctx, event); err != nil {
		tm.logger.Error(err, "BackendAuth initialization failed",
			"namespace", event.Namespace,
			"name", event.Name,
			"type", event.Type)
		return err
	}

	tm.logger.Info("BackendAuth initialization succeeded",
		"namespace", event.Namespace,
		"name", event.Name,
		"type", event.Type)

	return nil
}
