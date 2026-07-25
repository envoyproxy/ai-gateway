// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package usageevent implements the per-request AI usage event export described in
// docs/proposals/012-per-request-ai-usage-event-export/proposal.md: a normalized UsageEvent
// is constructed for every completed request and synchronously published through a pluggable
// UsageEventSink. The gateway stores no events, performs no retries, and makes no delivery
// guarantees beyond sink acknowledgement.
package usageevent

import "context"

// SchemaVersion identifies the current, additive-only UsageEvent schema.
const SchemaVersion = "v1"

// UsageEvent is the normalized per-request AI usage record emitted by the gateway. Every
// request reaching a terminal response emits exactly one event. When the provider returns no
// usage, token fields are zero. The schema is stable and additive: fields may be added over
// time, but existing fields will not change semantics or be removed.
type UsageEvent struct {
	// SchemaVersion allows consumers to detect breaking changes. Fields are additive-only
	// within a version.
	SchemaVersion string `json:"schema_version"`
	// EventID is a stable identifier for deduplication, derived from the gateway-generated
	// request UUID, route, and backend. The gateway does not persist IDs across restarts.
	EventID string `json:"event_id"`
	// EmittedAt is the Unix timestamp in milliseconds when the event was emitted.
	EmittedAt int64 `json:"emitted_at"`
	// RequestID is the stable request identifier.
	RequestID string `json:"request_id"`
	// Status is the gateway-authoritative request outcome.
	Status string `json:"status"`
	// StatusCode is the HTTP status returned to the client.
	StatusCode int `json:"status_code"`
	// Provider is the upstream provider.
	Provider string `json:"provider"`
	// Backend is the selected backend.
	Backend string `json:"backend"`
	// ModelRequested is the model requested by the client.
	ModelRequested string `json:"model_requested"`
	// ModelResponse is the model returned by the provider.
	ModelResponse string `json:"model_response"`

	// InputTokens, OutputTokens, CachedInputTokens, CacheWriteInputTokens, and ReasoningTokens
	// are usage counters reported by the provider, normalized into non-overlapping components.
	InputTokens           int `json:"input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	CacheWriteInputTokens int `json:"cache_write_input_tokens"`
	ReasoningTokens       int `json:"reasoning_tokens"`

	// Attributes carries optional, operator-allowlisted attribution metadata (e.g. tenant.id,
	// user.id) extracted from the request.
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Status values for UsageEvent.Status.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// Sink publishes a single UsageEvent and acknowledges receipt. Implementations must return
// promptly: callers publish synchronously on the request path and treat any error, including
// a context deadline, as a dropped event.
type Sink interface {
	Publish(ctx context.Context, event *UsageEvent) error
}
