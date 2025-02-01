package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/envoyproxy/ai-gateway/internal/controller/token_rotators"
)

// RotationType defines the type of rotation to be performed
type RotationType string

const (
	// RotationTypeAWSCredentials represents AWS IAM credentials rotation
	RotationTypeAWSCredentials RotationType = "aws-credentials"
	// RotationTypeAWSOIDC represents AWS OIDC token rotation
	RotationTypeAWSOIDC RotationType = "aws-oidc"
)

// RotationEventType defines the type of rotation event
type RotationEventType string

const (
	// RotationEventStarted indicates a rotation has started
	RotationEventStarted RotationEventType = "Started"
	// RotationEventSucceeded indicates a rotation has completed successfully
	RotationEventSucceeded RotationEventType = "Succeeded"
	// RotationEventFailed indicates a rotation has failed
	RotationEventFailed RotationEventType = "Failed"
	// RotationEventInitialization indicates an initialization event
	RotationEventInitialization RotationEventType = "Initialization"
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

// TokenRotationEvent represents an event related to token rotation
type TokenRotationEvent struct {
	// Type of the event (Started, Succeeded, Failed)
	EventType RotationEventType
	// The rotation event that triggered this event
	RotationEvent token_rotators.RotationEvent
	// Error message if the event type is Failed
	Error string
	// Timestamp when the event occurred
	Timestamp time.Time
}

// Rotator defines the interface for credential rotators
type Rotator interface {
	// Initialize performs the initial token retrieval
	Initialize(ctx context.Context, event RotationEvent) error
	// Rotate performs the credential rotation
	Rotate(ctx context.Context, event RotationEvent) error
	// Type returns the type of rotator
	Type() RotationType
}

// TokenManager manages credential rotation across different rotator implementations
type TokenManager struct {
	rotationChan chan token_rotators.RotationEvent
	rotators     map[token_rotators.RotationType]token_rotators.Rotator
	logger       logr.Logger
	mu           sync.RWMutex
	wg           sync.WaitGroup
	stopChan     chan struct{} // Channel to signal goroutines to stop
	// scheduledRotations tracks scheduled rotations by namespace/name
	scheduledRotations sync.Map
	// rotationWindow is how long before expiry to rotate credentials
	rotationWindow time.Duration
	// publishChan is used to publish rotation events to subscribers
	publishChan chan token_rotators.RotationEvent
	// eventChan is used to publish events to the configSink
	eventChan chan ConfigSinkEvent
	// client is used for Kubernetes API operations
	client client.Client
}

// scheduledRotation represents a scheduled token rotation
type scheduledRotation struct {
	timer    *time.Timer
	cancelFn context.CancelFunc
	event    token_rotators.RotationEvent
}

// NewTokenManager creates a new token manager
func NewTokenManager(logger logr.Logger, eventChan chan ConfigSinkEvent, client client.Client) *TokenManager {
	return &TokenManager{
		logger:         logger,
		rotators:       make(map[token_rotators.RotationType]token_rotators.Rotator),
		rotationChan:   make(chan token_rotators.RotationEvent, 100), // Buffer size of 100
		publishChan:    make(chan token_rotators.RotationEvent, 100), // Buffer size of 100
		stopChan:       make(chan struct{}),
		client:         client,
		eventChan:      eventChan,
		rotationWindow: 5 * time.Minute, // Default to rotating 5 minutes before expiry
	}
}

// RegisterRotator registers a new rotator implementation
func (tm *TokenManager) RegisterRotator(r token_rotators.Rotator) error {
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
func (tm *TokenManager) RotationChannel() <-chan token_rotators.RotationEvent {
	return tm.rotationChan
}

// publishRotationEvent publishes a token rotation event to the configSink
func (tm *TokenManager) publishRotationEvent(event TokenRotationEvent) {
	if tm.eventChan == nil {
		return
	}

	// Create a Kubernetes event
	k8sEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("token-rotation-%s-", strings.ToLower(string(event.EventType))),
			Namespace:    event.RotationEvent.Namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Secret", // This might need to be dynamic based on the rotation type
			Name:      event.RotationEvent.Name,
			Namespace: event.RotationEvent.Namespace,
		},
		Reason:    fmt.Sprintf("TokenRotation%s", event.EventType),
		Message:   tm.formatEventMessage(event),
		Type:      tm.getEventType(event.EventType),
		EventTime: metav1.MicroTime{Time: event.Timestamp},
	}

	select {
	case tm.eventChan <- k8sEvent:
		tm.logger.V(1).Info("published rotation event",
			"type", event.EventType,
			"namespace", event.RotationEvent.Namespace,
			"name", event.RotationEvent.Name)
	default:
		tm.logger.Error(fmt.Errorf("event channel is full"), "failed to publish rotation event",
			"type", event.EventType,
			"namespace", event.RotationEvent.Namespace,
			"name", event.RotationEvent.Name)
	}
}

// formatEventMessage formats the event message based on the event type
func (tm *TokenManager) formatEventMessage(event TokenRotationEvent) string {
	switch event.EventType {
	case RotationEventStarted:
		return fmt.Sprintf("Started rotation of %s token", event.RotationEvent.Type)
	case RotationEventSucceeded:
		return fmt.Sprintf("Successfully rotated %s token", event.RotationEvent.Type)
	case RotationEventFailed:
		return fmt.Sprintf("Failed to rotate %s token: %s", event.RotationEvent.Type, event.Error)
	default:
		return fmt.Sprintf("Unknown rotation event for %s token", event.RotationEvent.Type)
	}
}

// getEventType returns the appropriate Kubernetes event type
func (tm *TokenManager) getEventType(eventType RotationEventType) string {
	switch eventType {
	case RotationEventStarted:
		return corev1.EventTypeNormal
	case RotationEventSucceeded:
		return corev1.EventTypeNormal
	case RotationEventFailed:
		return corev1.EventTypeWarning
	default:
		return corev1.EventTypeNormal
	}
}

// isTokenInitialized checks if a token has been initialized by verifying if its secret exists
func (tm *TokenManager) isTokenInitialized(event token_rotators.RotationEvent) bool {
	secret := &corev1.Secret{}
	err := tm.client.Get(context.Background(), client.ObjectKey{
		Namespace: event.Namespace,
		Name:      event.Name,
	}, secret)
	return err == nil && len(secret.Data) > 0
}

// validateRotationEvent validates that the rotation event is valid and a rotator exists for its type
func (tm *TokenManager) validateRotationEvent(event token_rotators.RotationEvent) (bool, error) {
	if event.Type == "" {
		return false, fmt.Errorf("rotation type cannot be empty")
	}
	return true, nil
}

// checkRotatorExists checks if a rotator exists for the given type
func (tm *TokenManager) checkRotatorExists(rotationType token_rotators.RotationType) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	_, exists := tm.rotators[rotationType]
	return exists
}

// publishEvent is a helper method to publish rotation events with consistent error handling
func (tm *TokenManager) publishEvent(event token_rotators.RotationEvent, eventType RotationEventType, err error) {
	rotationEvent := TokenRotationEvent{
		EventType:     eventType,
		RotationEvent: event,
		Timestamp:     time.Now(),
	}
	if err != nil {
		rotationEvent.Error = err.Error()
	}
	tm.publishRotationEvent(rotationEvent)
}

// handleError publishes an error event and returns a formatted error
func (tm *TokenManager) handleError(event token_rotators.RotationEvent, errMsg string, err error) error {
	tm.publishEvent(event, RotationEventFailed, err)
	if err != nil {
		return fmt.Errorf("%s: %w", errMsg, err)
	}
	return fmt.Errorf("%s", errMsg)
}

// RequestRotation requests a rotation for a secret. This is a non-blocking operation.
func (tm *TokenManager) RequestRotation(ctx context.Context, event token_rotators.RotationEvent) error {
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
	needsInit := !tm.isTokenInitialized(event)
	if needsInit {
		if err := tm.RequestInitialization(ctx, event); err != nil {
			tm.mu.Unlock()
			return err
		}
	}
	tm.mu.Unlock()

	// Publish started event
	tm.publishEvent(event, RotationEventStarted, nil)

	// Try non-blocking sends to both channels
	select {
	case tm.rotationChan <- event:
		// Successfully sent to rotation channel
	default:
		return tm.handleError(event, "rotation channel is full", nil)
	}

	// Try non-blocking send to publish channel
	select {
	case <-ctx.Done():
		// Context was cancelled after rotation channel send
		// We don't return an error here since the rotation is already queued
		tm.logger.Info("context cancelled before publishing event",
			"type", event.Type,
			"namespace", event.Namespace,
			"name", event.Name)
	case tm.publishChan <- event:
		// Successfully sent to publish channel
	default:
		// Channel is full, log warning but don't fail the rotation
		tm.logger.Info("publish channel is full, dropping rotation event",
			"type", event.Type,
			"namespace", event.Namespace,
			"name", event.Name)
	}

	return nil
}

// getScheduledRotationKey returns a consistent key format for scheduled rotations
func (tm *TokenManager) getScheduledRotationKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// createScheduledRotation creates and stores a new scheduled rotation
func (tm *TokenManager) createScheduledRotation(ctx context.Context, event token_rotators.RotationEvent, rotateAt time.Time) (*scheduledRotation, context.CancelFunc) {
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

	key := tm.getScheduledRotationKey(event.Namespace, event.Name)
	tm.scheduledRotations.Store(key, sr)

	return sr, cancel
}

// ScheduleRotation schedules a rotation to occur at a specific time
func (tm *TokenManager) ScheduleRotation(ctx context.Context, event token_rotators.RotationEvent, rotateAt time.Time) {
	// Cancel any existing scheduled rotation for this resource
	tm.cancelExistingRotation(event.Namespace, event.Name)

	// If we're already past or very close to the rotation time, trigger immediately
	if time.Until(rotateAt) < time.Second {
		tm.RequestRotation(ctx, event)
		return
	}

	_, _ = tm.createScheduledRotation(ctx, event, rotateAt)

	tm.logger.Info("scheduled rotation",
		"namespace", event.Namespace,
		"name", event.Name,
		"rotateAt", rotateAt)
}

// ScheduleNextRotation schedules the next rotation based on expiry time
func (tm *TokenManager) ScheduleNextRotation(ctx context.Context, event token_rotators.RotationEvent, expiry time.Time) {
	rotateAt := expiry.Add(-tm.rotationWindow)
	tm.ScheduleRotation(ctx, event, rotateAt)
}

// cancelExistingRotation cancels any existing scheduled rotation for the given resource
func (tm *TokenManager) cancelExistingRotation(namespace, name string) {
	key := tm.getScheduledRotationKey(namespace, name)
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
func (tm *TokenManager) Cleanup() {
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
			go func(e token_rotators.RotationEvent) {
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

// RequestInitialization handles the initialization of tokens
func (tm *TokenManager) RequestInitialization(ctx context.Context, event token_rotators.RotationEvent) error {
	tm.logger.Info("requesting token initialization",
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

	// Publish initialization started event
	tm.publishEvent(event, RotationEventInitialization, nil)

	// Perform initialization
	if err := r.Initialize(ctx, event); err != nil {
		return tm.handleError(event, "failed to initialize token", err)
	}

	// Publish success event
	tm.publishEvent(event, RotationEventSucceeded, nil)

	return nil
}
