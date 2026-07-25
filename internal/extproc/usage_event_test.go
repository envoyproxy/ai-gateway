// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/usageevent"
)

func TestBuildUsageEventID(t *testing.T) {
	got := buildUsageEventID("req-abc123", "llmroute", "openai-primary")
	assert.Equal(t, "req-abc123|llmroute|openai-primary", got)
}

func TestExtractUsageEventAttributes(t *testing.T) {
	tests := []struct {
		name             string
		headers          map[string]string
		attributeHeaders map[string]string
		want             map[string]string
	}{
		{
			name:             "no allowlist configured",
			headers:          map[string]string{"x-tenant-id": "tenant-a"},
			attributeHeaders: nil,
			want:             nil,
		},
		{
			name:             "allowlisted header present",
			headers:          map[string]string{"x-tenant-id": "tenant-a", "x-user-id": "user-123"},
			attributeHeaders: map[string]string{"x-tenant-id": "tenant.id"},
			want:             map[string]string{"tenant.id": "tenant-a"},
		},
		{
			name:             "allowlisted header absent from request",
			headers:          map[string]string{},
			attributeHeaders: map[string]string{"x-tenant-id": "tenant.id"},
			want:             nil,
		},
		{
			name:             "allowlisted header present but empty",
			headers:          map[string]string{"x-tenant-id": ""},
			attributeHeaders: map[string]string{"x-tenant-id": "tenant.id"},
			want:             nil,
		},
		{
			name:             "only allowlisted headers are copied",
			headers:          map[string]string{"x-tenant-id": "tenant-a", "authorization": "secret"},
			attributeHeaders: map[string]string{"x-tenant-id": "tenant.id"},
			want:             map[string]string{"tenant.id": "tenant-a"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractUsageEventAttributes(tc.headers, tc.attributeHeaders)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBuildUsageEvent(t *testing.T) {
	var usage metrics.TokenUsage
	usage.SetInputTokens(120)
	usage.SetOutputTokens(480)
	usage.SetCachedInputTokens(10)
	usage.SetReasoningTokens(320)

	now := time.UnixMilli(1753961025123)
	event := buildUsageEvent(&usageEventParams{
		requestID:     "req-abc123",
		routeName:     "llmroute",
		backendName:   "openai-primary",
		provider:      "openai",
		reqModel:      "o4-mini",
		responseModel: "o4-mini",
		statusCode:    200,
		succeeded:     true,
		usage:         usage,
		attributes:    map[string]string{"tenant.id": "tenant-a"},
		now:           now,
	})

	assert.Equal(t, usageevent.SchemaVersion, event.SchemaVersion)
	assert.Equal(t, "req-abc123|llmroute|openai-primary", event.EventID)
	assert.Equal(t, int64(1753961025123), event.EmittedAt)
	assert.Equal(t, "req-abc123", event.RequestID)
	assert.Equal(t, usageevent.StatusSucceeded, event.Status)
	assert.Equal(t, 200, event.StatusCode)
	assert.Equal(t, "openai", event.Provider)
	assert.Equal(t, "openai-primary", event.Backend)
	assert.Equal(t, "o4-mini", event.ModelRequested)
	assert.Equal(t, "o4-mini", event.ModelResponse)
	assert.Equal(t, 120, event.InputTokens)
	assert.Equal(t, 480, event.OutputTokens)
	assert.Equal(t, 10, event.CachedInputTokens)
	assert.Equal(t, 0, event.CacheWriteInputTokens)
	assert.Equal(t, 320, event.ReasoningTokens)
	assert.Equal(t, map[string]string{"tenant.id": "tenant-a"}, event.Attributes)
}

func TestBuildUsageEvent_Failed(t *testing.T) {
	event := buildUsageEvent(&usageEventParams{
		requestID:   "req-def456",
		routeName:   "llmroute",
		backendName: "openai-primary",
		statusCode:  500,
		succeeded:   false,
		now:         time.Now(),
	})
	assert.Equal(t, usageevent.StatusFailed, event.Status)
	assert.Equal(t, 500, event.StatusCode)
}
