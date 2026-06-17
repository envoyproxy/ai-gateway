// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package file

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	json "github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage"
)

func TestNew(t *testing.T) {
	t.Run("creates directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "ratelimit")
		store, err := New(t.Context(), dir)
		require.NoError(t, err)
		require.NotNil(t, store)
		require.DirExists(t, dir)
	})
	t.Run("existing directory", func(t *testing.T) {
		dir := t.TempDir()
		store, err := New(t.Context(), dir)
		require.NoError(t, err)
		require.NotNil(t, store)
	})
}

func TestIncrement(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := New(ctx, dir)
	require.NoError(t, err)

	counter := storage.Counter{Domain: "test", DescriptorKey: "key1"}
	limit := storage.Limit{RequestsPerUnit: 10, Unit: storage.RateLimitUnitMinute}

	// First increment.
	count, resetAt, err := store.Increment(ctx, counter, limit, 1)
	require.NoError(t, err)
	require.Equal(t, uint32(1), count)
	require.True(t, resetAt.After(time.Now()))

	// Second increment.
	count, _, err = store.Increment(ctx, counter, limit, 1)
	require.NoError(t, err)
	require.Equal(t, uint32(2), count)

	// Verify file exists.
	path := store.filePath(counter.Key())
	require.FileExists(t, path)
}

func TestIncrement_WindowReset(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := New(ctx, dir)
	require.NoError(t, err)

	counter := storage.Counter{Domain: "test", DescriptorKey: "key2"}
	// Use a SECOND window so we can trigger reset by manipulating the file.
	limit := storage.Limit{RequestsPerUnit: 5, Unit: storage.RateLimitUnitSecond}

	count, _, err := store.Increment(ctx, counter, limit, 1)
	require.NoError(t, err)
	require.Equal(t, uint32(1), count)

	// Manually set the file's resetAt to the past.
	path := store.filePath(counter.Key())
	data := counterData{Count: 5, ResetAt: time.Now().Add(-time.Second)}
	writeCounterData(t, path, data)

	// Next increment should reset.
	count, _, err = store.Increment(ctx, counter, limit, 1)
	require.NoError(t, err)
	require.Equal(t, uint32(1), count)
}

func TestIncrement_EmptyFile(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := New(ctx, dir)
	require.NoError(t, err)

	counter := storage.Counter{Domain: "test", DescriptorKey: "key3"}
	limit := storage.Limit{RequestsPerUnit: 10, Unit: storage.RateLimitUnitMinute}

	// Write an empty file.
	path := store.filePath(counter.Key())
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte{}, 0o600))

	count, _, err := store.Increment(ctx, counter, limit, 1)
	require.NoError(t, err)
	require.Equal(t, uint32(1), count)
}

func TestIncrement_CorruptedJSON(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := New(ctx, dir)
	require.NoError(t, err)

	counter := storage.Counter{Domain: "test", DescriptorKey: "corrupted-json"}
	limit := storage.Limit{RequestsPerUnit: 10, Unit: storage.RateLimitUnitMinute}

	// Write malformed JSON to disk.
	path := store.filePath(counter.Key())
	require.NoError(t, os.WriteFile(path, []byte(`{bad json!!!`), 0o600))

	// Increment should recover by treating it as a fresh counter.
	count, resetAt, err := store.Increment(ctx, counter, limit, 1)
	require.NoError(t, err)
	require.Equal(t, uint32(1), count)
	require.True(t, resetAt.After(time.Now()))
}

func TestIncrement_OverLimit(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := New(ctx, dir)
	require.NoError(t, err)

	counter := storage.Counter{Domain: "test", DescriptorKey: "key4"}
	limit := storage.Limit{RequestsPerUnit: 3, Unit: storage.RateLimitUnitHour}

	for i := uint32(1); i <= 5; i++ {
		count, _, err := store.Increment(ctx, counter, limit, 1)
		require.NoError(t, err)
		require.Equal(t, i, count)
	}

	// Verify file content.
	path := store.filePath(counter.Key())
	data := readCounterData(t, path)
	require.Equal(t, uint32(5), data.Count)
}

func TestReset(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := New(ctx, dir)
	require.NoError(t, err)

	counter := storage.Counter{Domain: "test", DescriptorKey: "key5"}
	limit := storage.Limit{RequestsPerUnit: 10, Unit: storage.RateLimitUnitMinute}

	_, _, err = store.Increment(ctx, counter, limit, 1)
	require.NoError(t, err)
	require.FileExists(t, store.filePath(counter.Key()))

	err = store.Reset(ctx, counter)
	require.NoError(t, err)
	require.NoFileExists(t, store.filePath(counter.Key()))
}

func TestReset_MissingFile(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := New(ctx, dir)
	require.NoError(t, err)

	counter := storage.Counter{Domain: "test", DescriptorKey: "nonexistent"}
	err = store.Reset(ctx, counter)
	require.NoError(t, err)
}

func TestPing(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		store, err := New(t.Context(), dir)
		require.NoError(t, err)
		require.NoError(t, store.Ping(t.Context()))
	})
	t.Run("removed directory", func(t *testing.T) {
		dir := t.TempDir()
		store, err := New(t.Context(), dir)
		require.NoError(t, err)
		require.NoError(t, os.RemoveAll(dir))
		err = store.Ping(t.Context())
		require.Error(t, err)
	})
}

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"a/b:c", "a_b_c"},
		{"a\\b", "a_b"},
		{"a*b?c", "a_b_c"},
		{"a\"b<c>d|e", "a_b_c_d_e"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.expected, sanitizeKey(tt.input))
	}
}

func TestConcurrentIncrement(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := New(ctx, dir)
	require.NoError(t, err)

	counter := storage.Counter{Domain: "test", DescriptorKey: "concurrent"}
	limit := storage.Limit{RequestsPerUnit: 100, Unit: storage.RateLimitUnitHour}

	var wg sync.WaitGroup
	n := 50
	results := make([]uint32, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			count, _, err := store.Increment(ctx, counter, limit, 1)
			require.NoError(t, err)
			results[idx] = count
		}(i)
	}
	wg.Wait()

	// All results should be unique (1..50 in some order).
	seen := make(map[uint32]bool)
	for _, c := range results {
		require.False(t, seen[c], "duplicate count: %d", c)
		seen[c] = true
	}
	require.Len(t, seen, 50)

	// Final file should have count = 50.
	data := readCounterData(t, store.filePath(counter.Key()))
	require.Equal(t, uint32(50), data.Count)
}

func TestClose(t *testing.T) {
	dir := t.TempDir()
	store, err := New(t.Context(), dir)
	require.NoError(t, err)
	require.NoError(t, store.Close())
}

func TestStore_ImplementsInterface(_ *testing.T) {
	var s storage.Store = (*Store)(nil)
	_ = s
}

func writeCounterData(t *testing.T, path string, data counterData) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, json.NewEncoder(f).Encode(data))
}

func readCounterData(t *testing.T, path string) counterData {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	var data counterData
	require.NoError(t, json.NewDecoder(f).Decode(&data))
	return data
}

// compile-time interface compliance check.
var _ storage.Store = (*Store)(nil)
