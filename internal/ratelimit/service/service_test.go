// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	commonrlv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage"
)

// mockStore implements storage.Store for testing.
type mockStore struct {
	mu       sync.Mutex
	counters map[string]mockCounter
}

type mockCounter struct {
	count   uint32
	resetAt time.Time
}

func newMockStore() *mockStore {
	return &mockStore{counters: make(map[string]mockCounter)}
}

func (m *mockStore) Increment(_ context.Context, counter storage.Counter, limit storage.Limit, delta uint32) (uint32, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := counter.Key()
	now := time.Now().UTC()
	duration := limit.Unit.UnitDuration()
	resetAt := now.Truncate(duration).Add(duration)

	c, ok := m.counters[key]
	if !ok || now.After(c.resetAt) {
		m.counters[key] = mockCounter{count: delta, resetAt: resetAt}
		return delta, resetAt, nil
	}
	c.count += delta
	m.counters[key] = c
	return c.count, c.resetAt, nil
}

func (m *mockStore) Reset(_ context.Context, counter storage.Counter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.counters, counter.Key())
	return nil
}

func (m *mockStore) Ping(_ context.Context) error { return nil }
func (m *mockStore) Close() error                 { return nil }

// errorStore returns an error on every Increment call.
type errorStore struct{ err error }

func (e *errorStore) Increment(_ context.Context, _ storage.Counter, _ storage.Limit, _ uint32) (uint32, time.Time, error) {
	return 0, time.Time{}, e.err
}
func (e *errorStore) Reset(_ context.Context, _ storage.Counter) error { return nil }
func (e *errorStore) Ping(_ context.Context) error                     { return nil }
func (e *errorStore) Close() error                                     { return nil }

func TestShouldRateLimit_NoConfig(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	resp, err := svc.ShouldRateLimit(t.Context(), &ratelimitv3.RateLimitRequest{
		Domain: "test",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "key1", Value: "val1"},
			}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.OverallCode)
}

func TestShouldRateLimit_UnderLimit(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:   "test",
		Domain: "test",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{
			{
				Key:   "key1",
				Value: "val1",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 5,
					Unit:            rlsconfv3.RateLimitUnit_MINUTE,
				},
			},
		},
	})

	req := &ratelimitv3.RateLimitRequest{
		Domain: "test",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "key1", Value: "val1"},
			}},
		},
	}

	// First 5 requests should be OK.
	for i := 0; i < 5; i++ {
		resp, err := svc.ShouldRateLimit(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.OverallCode)
		require.Len(t, resp.Statuses, 1)
		require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.Statuses[0].Code)
	}

	// 6th request should be OVER_LIMIT.
	resp, err := svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OVER_LIMIT, resp.OverallCode)
	require.Len(t, resp.Statuses, 1)
	require.Equal(t, ratelimitv3.RateLimitResponse_OVER_LIMIT, resp.Statuses[0].Code)
}

func TestShouldRateLimit_NonMatchingDescriptor(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:   "test",
		Domain: "test",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{
			{
				Key:   "known_key",
				Value: "known_val",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 1,
					Unit:            rlsconfv3.RateLimitUnit_MINUTE,
				},
			},
		},
	})

	req := &ratelimitv3.RateLimitRequest{
		Domain: "test",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "unknown_key", Value: "unknown_val"},
			}},
		},
	}

	resp, err := svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.OverallCode)
}

func TestShouldRateLimit_NestedDescriptors(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:   "test",
		Domain: "test",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{
			{
				Key:   "backend",
				Value: "my-backend",
				Descriptors: []*rlsconfv3.RateLimitDescriptor{
					{
						Key:   "model",
						Value: "gpt-4",
						RateLimit: &rlsconfv3.RateLimitPolicy{
							RequestsPerUnit: 3,
							Unit:            rlsconfv3.RateLimitUnit_HOUR,
						},
					},
				},
			},
		},
	})

	req := &ratelimitv3.RateLimitRequest{
		Domain: "test",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "backend", Value: "my-backend"},
				{Key: "model", Value: "gpt-4"},
			}},
		},
	}

	for i := 0; i < 3; i++ {
		resp, err := svc.ShouldRateLimit(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.OverallCode)
	}

	resp, err := svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OVER_LIMIT, resp.OverallCode)
}

func TestShouldRateLimit_MultipleDescriptors_MixedResults(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:   "test",
		Domain: "test",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{
			{
				Key:   "a",
				Value: "1",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 1,
					Unit:            rlsconfv3.RateLimitUnit_HOUR,
				},
			},
			{
				Key:   "b",
				Value: "2",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 10,
					Unit:            rlsconfv3.RateLimitUnit_HOUR,
				},
			},
		},
	})

	req := &ratelimitv3.RateLimitRequest{
		Domain: "test",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "a", Value: "1"},
			}},
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "b", Value: "2"},
			}},
		},
	}

	// First request: both pass.
	resp, err := svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.OverallCode)

	// Second request: "a/1" should be over limit, "b/2" still under.
	resp, err = svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OVER_LIMIT, resp.OverallCode)
	require.Len(t, resp.Statuses, 2)
	require.Equal(t, ratelimitv3.RateLimitResponse_OVER_LIMIT, resp.Statuses[0].Code)
	require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.Statuses[1].Code)
}

func TestBuildCounter(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	counter := svc.buildCounter("my-domain", []*commonrlv3.RateLimitDescriptor_Entry{
		{Key: "backend", Value: "b1"},
		{Key: "model", Value: "gpt-4"},
	})
	require.Equal(t, "my-domain", counter.Domain)
	require.Equal(t, "backend_b1/model_gpt-4", counter.DescriptorKey)
}

func TestFindLimit_EmptyEntries(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	result := svc.findLimit(nil, nil)
	require.Nil(t, result)
}

func TestFindLimit_NoMatch(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	descriptors := []*rlsconfv3.RateLimitDescriptor{
		{Key: "x", Value: "y"},
	}
	result := svc.findLimit(descriptors, []*commonrlv3.RateLimitDescriptor_Entry{
		{Key: "a", Value: "b"},
	})
	require.Nil(t, result)
}

func TestFindLimit_PartialMatch(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	descriptors := []*rlsconfv3.RateLimitDescriptor{
		{
			Key:   "a",
			Value: "b",
			Descriptors: []*rlsconfv3.RateLimitDescriptor{
				{Key: "c", Value: "d"},
			},
		},
	}
	// Matches "a/b" but "c/d" doesn't exist in nested descriptor.
	result := svc.findLimit(descriptors, []*commonrlv3.RateLimitDescriptor_Entry{
		{Key: "a", Value: "b"},
		{Key: "c", Value: "e"},
	})
	require.Nil(t, result)
}

func TestShouldRateLimit_EmptyRequest(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:        "test",
		Domain:      "test",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{},
	})
	resp, err := svc.ShouldRateLimit(t.Context(), &ratelimitv3.RateLimitRequest{
		Domain: "test",
	})
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.OverallCode)
}

func TestShouldRateLimit_DescriptorStatus_Fields(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:   "test",
		Domain: "test",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{
			{
				Key:   "k",
				Value: "v",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 10,
					Unit:            rlsconfv3.RateLimitUnit_MINUTE,
				},
			},
		},
	})

	req := &ratelimitv3.RateLimitRequest{
		Domain: "test",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "k", Value: "v"},
			}},
		},
	}

	resp, err := svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, resp.Statuses, 1)
	st := resp.Statuses[0]
	require.Equal(t, ratelimitv3.RateLimitResponse_OK, st.Code)
	require.NotNil(t, st.CurrentLimit)
	require.Equal(t, uint32(10), st.CurrentLimit.RequestsPerUnit)
	require.Equal(t, uint32(9), st.LimitRemaining)
	require.NotNil(t, st.DurationUntilReset)
	require.Positive(t, st.DurationUntilReset.Seconds)
}

func TestShouldRateLimit_StorageError(t *testing.T) {
	storeErr := errors.New("connection refused")
	svc := New(&errorStore{err: storeErr}, logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:   "test",
		Domain: "test",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{
			{
				Key:   "k",
				Value: "v",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 10,
					Unit:            rlsconfv3.RateLimitUnit_MINUTE,
				},
			},
		},
	})

	req := &ratelimitv3.RateLimitRequest{
		Domain: "test",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "k", Value: "v"},
			}},
		},
	}

	resp, err := svc.ShouldRateLimit(t.Context(), req)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Contains(t, err.Error(), "storage unavailable")
}

func TestShouldRateLimit_ZeroLimit(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:   "test",
		Domain: "test",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{
			{
				Key:   "k",
				Value: "v",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 0,
					Unit:            rlsconfv3.RateLimitUnit_MINUTE,
				},
			},
		},
	})

	req := &ratelimitv3.RateLimitRequest{
		Domain: "test",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "k", Value: "v"},
			}},
		},
	}

	// First request: count=1, limit=0 → instantly over limit.
	resp, err := svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OVER_LIMIT, resp.OverallCode)
}

func TestConvertRateLimitPolicy_OutOfRangeUnit(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	svc.UpdateConfig(&rlsconfv3.RateLimitConfig{
		Name:   "test",
		Domain: "test",
		Descriptors: []*rlsconfv3.RateLimitDescriptor{
			{
				Key:   "k",
				Value: "v",
				RateLimit: &rlsconfv3.RateLimitPolicy{
					RequestsPerUnit: 5,
					Unit:            rlsconfv3.RateLimitUnit(999),
				},
			},
		},
	})

	req := &ratelimitv3.RateLimitRequest{
		Domain: "test",
		Descriptors: []*commonrlv3.RateLimitDescriptor{
			{Entries: []*commonrlv3.RateLimitDescriptor_Entry{
				{Key: "k", Value: "v"},
			}},
		},
	}

	resp, err := svc.ShouldRateLimit(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, ratelimitv3.RateLimitResponse_OK, resp.OverallCode)
	// Out-of-range unit (999) defaults to SECOND in the response proto,
	// which maps to storage.RateLimitUnitSecond (1s window).
	require.Len(t, resp.Statuses, 1)
	require.Equal(t, ratelimitv3.RateLimitResponse_RateLimit_SECOND, resp.Statuses[0].CurrentLimit.Unit)
	require.NotNil(t, resp.Statuses[0].DurationUntilReset)
}

func TestBuildCounter_SingleEntry(t *testing.T) {
	svc := New(newMockStore(), logr.Discard())
	counter := svc.buildCounter("domain", []*commonrlv3.RateLimitDescriptor_Entry{
		{Key: "k", Value: "v"},
	})
	require.Equal(t, "domain", counter.Domain)
	require.Equal(t, "k_v", counter.DescriptorKey)
	require.Equal(t, "domain:k_v", counter.Key())
}
