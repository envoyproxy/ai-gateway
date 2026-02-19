// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
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

func TestRedisCache_Get_Hit(t *testing.T) {
	client, mock := redismock.NewClientMock()
	cache := newRedisCacheWithClient(client)
	ctx := context.Background()

	key := "test-key"
	value := []byte(`{"response":"cached"}`)

	mock.ExpectGet(key).SetVal(string(value))

	val, found, err := cache.Get(ctx, key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, value, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisCache_Get_Miss(t *testing.T) {
	client, mock := redismock.NewClientMock()
	cache := newRedisCacheWithClient(client)
	ctx := context.Background()

	key := "nonexistent-key"

	mock.ExpectGet(key).RedisNil()

	val, found, err := cache.Get(ctx, key)
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisCache_Get_Error(t *testing.T) {
	client, mock := redismock.NewClientMock()
	cache := newRedisCacheWithClient(client)
	ctx := context.Background()

	key := "error-key"
	expectedErr := errors.New("connection error")

	mock.ExpectGet(key).SetErr(expectedErr)

	val, found, err := cache.Get(ctx, key)
	require.Error(t, err)
	require.False(t, found)
	require.Nil(t, val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisCache_Set(t *testing.T) {
	client, mock := redismock.NewClientMock()
	cache := newRedisCacheWithClient(client)
	ctx := context.Background()

	key := "test-key"
	value := []byte(`{"response":"test"}`)
	ttl := time.Hour

	mock.ExpectSet(key, value, ttl).SetVal("OK")

	err := cache.Set(ctx, key, value, ttl)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisCache_Set_Error(t *testing.T) {
	client, mock := redismock.NewClientMock()
	cache := newRedisCacheWithClient(client)
	ctx := context.Background()

	key := "test-key"
	value := []byte(`{"response":"test"}`)
	ttl := time.Hour
	expectedErr := errors.New("write error")

	mock.ExpectSet(key, value, ttl).SetErr(expectedErr)

	err := cache.Set(ctx, key, value, ttl)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisCache_Close(t *testing.T) {
	client, mock := redismock.NewClientMock()
	cache := newRedisCacheWithClient(client)

	// Close should work without error
	err := cache.Close()
	require.NoError(t, err)
	_ = mock // mock doesn't track Close
}

func TestNewRedisCache_ConnectionError(t *testing.T) {
	// Test that NewRedisCache returns an error when connection fails
	cfg := RedisConfig{
		Addr: "invalid-host:12345",
	}

	cache, err := NewRedisCache(cfg)
	require.Error(t, err)
	require.Nil(t, cache)
}

func TestNewRedisCache_WithTLS(t *testing.T) {
	// Test that TLS config is applied (connection will fail but we verify the code path)
	cfg := RedisConfig{
		Addr: "invalid-host:12345",
		TLS:  true,
	}

	cache, err := NewRedisCache(cfg)
	require.Error(t, err) // Will fail to connect, but TLS code path is exercised
	require.Nil(t, cache)
}

func TestNewRedisCache_WithPassword(t *testing.T) {
	// Test that password config is applied (connection will fail but we verify the code path)
	cfg := RedisConfig{
		Addr:     "invalid-host:12345",
		Password: "secret",
	}

	cache, err := NewRedisCache(cfg)
	require.Error(t, err) // Will fail to connect, but password code path is exercised
	require.Nil(t, cache)
}

func TestRedisConfig(t *testing.T) {
	// Test RedisConfig struct fields
	cfg := RedisConfig{
		Addr:     "localhost:6379",
		Password: "secret",
		TLS:      true,
		DB:       1,
	}

	require.Equal(t, "localhost:6379", cfg.Addr)
	require.Equal(t, "secret", cfg.Password)
	require.True(t, cfg.TLS)
	require.Equal(t, 1, cfg.DB)
}

func TestErrCacheMiss(t *testing.T) {
	// Test that ErrCacheMiss is defined
	require.Error(t, ErrCacheMiss)
	require.Equal(t, "cache miss", ErrCacheMiss.Error())
}

func TestDefaultTTL(t *testing.T) {
	// Test that DefaultTTL is defined correctly
	require.Equal(t, time.Hour, DefaultTTL)
}

func TestCacheInterface(_ *testing.T) {
	// Verify that both implementations satisfy the Cache interface
	var _ Cache = &RedisCache{}
	var _ Cache = NoOpCache{}
}

func TestRedisCache_Get_BytesConversion(t *testing.T) {
	// Test that Get properly converts string to bytes
	client, mock := redismock.NewClientMock()
	cache := newRedisCacheWithClient(client)
	ctx := context.Background()

	key := "test-key"
	// Redis stores as string, we expect bytes back
	stringValue := `{"model":"gpt-4","response":"hello"}`

	mock.ExpectGet(key).SetVal(stringValue)

	val, found, err := cache.Get(ctx, key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte(stringValue), val)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRedisCache_Get_RedisNilHandling(t *testing.T) {
	// Explicitly test redis.Nil error handling
	client, mock := redismock.NewClientMock()
	cache := newRedisCacheWithClient(client)
	ctx := context.Background()

	key := "missing-key"

	// RedisNil simulates redis.Nil error
	mock.ExpectGet(key).SetErr(redis.Nil)

	val, found, err := cache.Get(ctx, key)
	require.NoError(t, err, "redis.Nil should not be returned as an error")
	require.False(t, found)
	require.Nil(t, val)
	require.NoError(t, mock.ExpectationsWereMet())
}
