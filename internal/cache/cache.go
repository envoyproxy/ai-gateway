// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package cache provides response caching functionality for the AI Gateway.
// It supports caching LLM responses in Redis to reduce latency and costs
// for repeated requests across all ext_proc instances.
package cache

import (
	"context"
	"crypto/tls"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultTTL is the default time-to-live for cached responses.
const DefaultTTL = time.Hour

// ErrCacheMiss is returned when a key is not found in the cache.
var ErrCacheMiss = errors.New("cache miss")

// Cache is the interface for response caching.
type Cache interface {
	// Get retrieves a cached response by key.
	// Returns the cached value, true if found, or ErrCacheMiss if not found.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Set stores a response in the cache with the given TTL.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Close closes the cache connection.
	Close() error
}

// RedisConfig holds the configuration for connecting to Redis.
type RedisConfig struct {
	// Addr is the Redis server address (e.g., "localhost:6379").
	Addr string
	// Password is the Redis password (optional).
	Password string
	// TLS enables TLS for the Redis connection.
	TLS bool
	// DB is the Redis database number (default 0).
	DB int
}

// RedisCache implements Cache using Redis.
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache creates a new Redis cache client.
func NewRedisCache(cfg RedisConfig) (*RedisCache, error) {
	opts := &redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	if cfg.TLS {
		opts.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	client := redis.NewClient(opts)

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &RedisCache{client: client}, nil
}

// Get retrieves a cached response by key.
func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

// Set stores a response in the cache with the given TTL.
func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

// Close closes the Redis connection.
func (c *RedisCache) Close() error {
	return c.client.Close()
}

// NoOpCache is a cache implementation that does nothing.
// Used when caching is disabled.
type NoOpCache struct{}

// Get always returns a cache miss.
func (NoOpCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}

// Set does nothing.
func (NoOpCache) Set(context.Context, string, []byte, time.Duration) error {
	return nil
}

// Close does nothing.
func (NoOpCache) Close() error {
	return nil
}
