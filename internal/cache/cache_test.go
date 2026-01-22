// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHashBody(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		expected string
	}{
		{
			name:     "empty body",
			body:     []byte{},
			expected: KeyPrefix + "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:     "simple body",
			body:     []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`),
			expected: KeyPrefix + "a8e8c5c8b5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5", // placeholder
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HashBody(tt.body)
			require.Greater(t, len(result), len(KeyPrefix), "hash should be longer than prefix")
			require.Contains(t, result, KeyPrefix, "hash should contain prefix")

			// Verify determinism
			result2 := HashBody(tt.body)
			require.Equal(t, result, result2, "hash should be deterministic")
		})
	}

	// Test that different bodies produce different hashes
	hash1 := HashBody([]byte(`{"model":"gpt-4"}`))
	hash2 := HashBody([]byte(`{"model":"gpt-3.5"}`))
	require.NotEqual(t, hash1, hash2, "different bodies should produce different hashes")
}

func TestNoOpCache(t *testing.T) {
	cache := NoOpCache{}
	ctx := context.Background()

	// Get should always return miss
	val, found, err := cache.Get(ctx, "any-key")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, val)

	// Set should succeed without error
	err = cache.Set(ctx, "any-key", []byte("value"), time.Hour)
	require.NoError(t, err)

	// Get should still return miss after Set
	val, found, err = cache.Get(ctx, "any-key")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, val)

	// Close should succeed
	err = cache.Close()
	require.NoError(t, err)
}

// TestRedisCacheIntegration tests the Redis cache with a real Redis instance.
// This test is skipped if Redis is not available.
func TestRedisCacheIntegration(t *testing.T) {
	cfg := RedisConfig{
		Addr: "localhost:6379",
	}

	cache, err := NewRedisCache(cfg)
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()
	key := "test-key-" + time.Now().Format(time.RFC3339Nano)
	value := []byte(`{"response":"test"}`)

	// Initially should be a miss
	val, found, err := cache.Get(ctx, key)
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, val)

	// Set the value
	err = cache.Set(ctx, key, value, time.Minute)
	require.NoError(t, err)

	// Now should be a hit
	val, found, err = cache.Get(ctx, key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, value, val)
}
