// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq" // registers "postgres" driver for database/sql

	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage"
)

// Store implements storage.Store using PostgreSQL.
type Store struct {
	db *sql.DB
}

// Config holds PostgreSQL connection parameters.
type Config struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// New creates a new PostgreSQL-backed rate limit store.
func New(ctx context.Context, cfg Config) (*Store, error) {
	db, err := sql.Open("postgres", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	applyDefaults(&cfg)
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		return nil, fmt.Errorf("postgres: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ratelimit_counters (
			key         TEXT PRIMARY KEY,
			count       INTEGER NOT NULL DEFAULT 0,
			reset_at    TIMESTAMPTZ NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_ratelimit_reset_at ON ratelimit_counters(reset_at);
	`)
	return err
}

// Increment atomically upserts the counter by delta, resetting if the current window has expired.
func (s *Store) Increment(ctx context.Context, counter storage.Counter, limit storage.Limit, delta uint32) (uint32, time.Time, error) {
	key := counter.Key()
	duration := limit.Unit.UnitDuration()
	now := time.Now().UTC()
	resetAt := now.Truncate(duration).Add(duration)

	var count int64
	var dbResetAt time.Time
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO ratelimit_counters (key, count, reset_at)
		VALUES ($1, $4, $2)
		ON CONFLICT (key) DO UPDATE
		SET count = CASE
				WHEN ratelimit_counters.reset_at <= $3 THEN $4
				ELSE ratelimit_counters.count + $4
			END,
			reset_at = CASE
				WHEN ratelimit_counters.reset_at <= $3 THEN $2
				ELSE ratelimit_counters.reset_at
			END
		RETURNING count, reset_at
	`, key, resetAt, now, delta).Scan(&count, &dbResetAt)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("postgres: increment: %w", err)
	}
	return uint32(count), dbResetAt, nil //nolint:gosec // count fits in uint32 per schema
}

// Reset deletes the counter row.
func (s *Store) Reset(ctx context.Context, counter storage.Counter) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ratelimit_counters WHERE key = $1`, counter.Key())
	return err
}

// Ping verifies connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close releases the database connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// applyDefaults backfills zero-value pool settings with sensible defaults.
func applyDefaults(cfg *Config) {
	if cfg.MaxOpenConns <= 0 {
		cfg.MaxOpenConns = 25
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = 5
	}
	if cfg.ConnMaxLifetime <= 0 {
		cfg.ConnMaxLifetime = 5 * time.Minute
	}
}
