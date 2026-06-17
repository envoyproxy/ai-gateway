// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	json "github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage"
)

// counterData is the on-disk format for a rate limit counter.
type counterData struct {
	Count   uint32    `json:"count"`
	ResetAt time.Time `json:"reset_at"`
}

// Store implements storage.Store using files on a shared volume.
// It uses syscall.Flock for cross-process mutual exclusion, safe for
// ReadWriteMany volumes (NFSv4, EFS, etc.).
type Store struct {
	dir string
	// inFlight serializes concurrent increments for the same key within a process,
	// preventing two goroutines from racing on the same file handle.
	inFlight sync.Map // key → *sync.Mutex
}

// New creates a new file-backed rate limit store.
func New(_ context.Context, dir string) (*Store, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("file: resolve path: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("file: create dir: %w", err)
	}
	return &Store{dir: absDir}, nil
}

// filePath returns the absolute path for a counter's on-disk file.
func (s *Store) filePath(key string) string {
	safe := sanitizeKey(key)
	return filepath.Join(s.dir, safe+".json")
}

func sanitizeKey(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch c {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			b[i] = '_'
		}
	}
	return string(b)
}

// Increment atomically increments the counter by delta, resetting if the current window has expired.
func (s *Store) Increment(_ context.Context, counter storage.Counter, limit storage.Limit, delta uint32) (uint32, time.Time, error) {
	key := counter.Key()
	path := s.filePath(key)
	now := time.Now().UTC()
	duration := limit.Unit.UnitDuration()
	resetAt := now.Truncate(duration).Add(duration)

	muI, _ := s.inFlight.LoadOrStore(key, &sync.Mutex{})
	mu := muI.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("file: open: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return 0, time.Time{}, fmt.Errorf("file: flock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck

	var data counterData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		data = counterData{}
	}

	if data.ResetAt.IsZero() || now.After(data.ResetAt) {
		data.Count = delta
		data.ResetAt = resetAt
	} else {
		data.Count += delta
	}

	if _, err := f.Seek(0, 0); err != nil {
		return 0, time.Time{}, fmt.Errorf("file: seek: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return 0, time.Time{}, fmt.Errorf("file: truncate: %w", err)
	}
	if err := json.NewEncoder(f).Encode(data); err != nil {
		return 0, time.Time{}, fmt.Errorf("file: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		return 0, time.Time{}, fmt.Errorf("file: sync: %w", err)
	}

	return data.Count, data.ResetAt, nil
}

// Reset removes the counter file.
func (s *Store) Reset(_ context.Context, counter storage.Counter) error {
	path := s.filePath(counter.Key())
	// Best-effort: if the file is already gone, we're done.
	_ = os.Remove(path)
	return nil
}

// Ping verifies the directory is writable.
func (s *Store) Ping(_ context.Context) error {
	probe := filepath.Join(s.dir, ".ping-probe")
	f, err := os.Create(probe)
	if err != nil {
		return fmt.Errorf("file: ping: %w", err)
	}
	f.Close()
	_ = os.Remove(probe)
	return nil
}

// Close is a no-op for the file store.
func (s *Store) Close() error { return nil }
