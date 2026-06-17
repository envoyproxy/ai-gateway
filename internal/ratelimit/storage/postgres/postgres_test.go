// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage"
)

func TestNew_InvalidDSN(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, err := New(ctx, Config{DSN: "postgres://invalid:[REDACTED]@localhost:5432/nonexistent?sslmode=disable&connect_timeout=1"})
	require.Error(t, err)
}

func TestNew_EmptyDSN(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	_, err := New(ctx, Config{DSN: ""})
	require.Error(t, err)
}

func TestStore_ImplementsInterface(_ *testing.T) {
	var s storage.Store = (*Store)(nil)
	_ = s
}

func TestStore_Close_NilDB(t *testing.T) {
	// Close on nil db panics; this is expected — a nil Store should never exist in practice.
	require.Panics(t, func() {
		s := &Store{db: nil}
		s.Close()
	})
}

func TestIncrement_CounterStruct(t *testing.T) {
	c := storage.Counter{
		Domain:        "test-domain",
		DescriptorKey: "backend_name_default/model_gpt-4",
	}
	require.Equal(t, "test-domain:backend_name_default/model_gpt-4", c.Key())
}

func TestLimit_Durations(t *testing.T) {
	tests := []struct {
		unit     storage.RateLimitUnit
		expected time.Duration
	}{
		{storage.RateLimitUnitSecond, time.Second},
		{storage.RateLimitUnitMinute, time.Minute},
		{storage.RateLimitUnitHour, time.Hour},
		{storage.RateLimitUnitDay, 24 * time.Hour},
	}
	for _, tt := range tests {
		require.Equal(t, tt.expected, tt.unit.UnitDuration())
	}
}

func TestRateLimitUnit_DefaultDuration(t *testing.T) {
	unit := storage.RateLimitUnit(999)
	require.Equal(t, time.Second, unit.UnitDuration())
}

// compile-time interface compliance check.
var _ storage.Store = (*Store)(nil)
