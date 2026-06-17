// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage"
)

// Store implements storage.Store using Redis.
type Store struct {
	client *redis.Client
}

// Config holds Redis connection parameters.
type Config struct {
	URL      string // host:port
	TLS      bool
	Password string
	DB       int
}

// New creates a new Redis-backed rate limit store.
func New(ctx context.Context, cfg Config) (*Store, error) {
	opts := &redis.Options{
		Addr:     cfg.URL,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	if cfg.TLS {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return &Store{client: client}, nil
}

// incrScript atomically increments a counter with window-based TTL.
// KEYS[1]: counter key
// ARGV[1]: window duration in seconds
// ARGV[2]: current unix timestamp
// ARGV[3]: delta (hits_addend)
//
// Note: the window starts from the first increment, not a fixed
// clock-aligned boundary. This differs from the file and postgres
// backends which truncate to window boundaries.
const incrScript = `
local key = KEYS[1]
local window = tonumber(ARGV[1])
local now = tonumber(ARGV[2])
local delta = tonumber(ARGV[3])

local current = redis.call('GET', key)
if current == false then
    redis.call('SET', key, delta, 'EX', window)
    return {delta, now + window}
end

local ttl = redis.call('TTL', key)
if ttl <= 0 then
    redis.call('SET', key, delta, 'EX', window)
    return {delta, now + window}
end

local newCount = redis.call('INCRBY', key, delta)
return {newCount, now + ttl}
`

// Increment atomically increments the counter by delta, resetting if the window has expired.
func (s *Store) Increment(ctx context.Context, counter storage.Counter, limit storage.Limit, delta uint32) (uint32, time.Time, error) {
	key := counter.Key()
	window := int(limit.Unit.UnitDuration().Seconds())
	now := time.Now().UTC().Unix()

	result, err := s.client.Eval(ctx, incrScript, []string{key},
		window, now, delta).Result()
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("redis: increment: %w", err)
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 2 {
		return 0, time.Time{}, fmt.Errorf("redis: unexpected result format: %v", result)
	}
	count := uint32(values[0].(int64)) //nolint:gosec // Redis value fits in uint32
	resetAt := time.Unix(values[1].(int64), 0).UTC()
	return count, resetAt, nil
}

// Reset deletes the counter key.
func (s *Store) Reset(ctx context.Context, counter storage.Counter) error {
	return s.client.Del(ctx, counter.Key()).Err()
}

// Ping verifies connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

// Close releases the Redis connection.
func (s *Store) Close() error {
	return s.client.Close()
}
