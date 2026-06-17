// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package storage

import (
	"context"
	"time"
)

// Store is the pluggable storage backend for rate limit counters.
// All operations must be atomic and safe for concurrent use.
//
// Window semantics: the resetTime returned by Increment defines when the
// current window ends. Backends may implement this differently:
//
//   - Clock-aligned: the file and postgres backends truncate the current
//     time to the window boundary (e.g., top of the minute for per-minute
//     limits). All increments within the same clock-window share the same
//     resetAt, producing consistent behavior across replicas.
//
//   - Sliding TTL: the redis backend sets a TTL from the first increment
//     within a new window. The window slides with each new key, so the
//     same request pattern may produce different resetAt values across
//     backends. This is a valid implementation but callers should be aware
//     that switching backends may change rate-limit boundary alignment.
type Store interface {
	// Increment atomically increments the counter by delta (typically the hits_addend
	// from the rate limit descriptor) and returns the new count.
	// If this is the first increment within the current window, it resets the window.
	Increment(ctx context.Context, counter Counter, limit Limit, delta uint32) (newCount uint32, resetTime time.Time, err error)

	// Reset clears the counter for the given key.
	Reset(ctx context.Context, counter Counter) error

	// Ping checks connectivity to the storage backend.
	Ping(ctx context.Context) error

	// Close releases resources held by the store.
	Close() error
}
