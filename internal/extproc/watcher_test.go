// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// mockReceiver is a mock implementation of Receiver.
type mockReceiver struct {
	cfg *filterapi.Config
	mux sync.Mutex
}

// LoadConfig implements ConfigReceiver.
func (m *mockReceiver) LoadConfig(_ context.Context, cfg *filterapi.Config) error {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.cfg = cfg
	return nil
}

func (m *mockReceiver) getConfig() *filterapi.Config {
	m.mux.Lock()
	defer m.mux.Unlock()
	return m.cfg
}

var _ io.Writer = (*syncBuffer)(nil)

// syncBuffer is a bytes.Buffer that is safe for concurrent read/write access.
// used just in the tests to safely read the logs in assertions without data races.
type syncBuffer struct {
	mu sync.RWMutex
	b  *bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.b.String()
}

// newTestLoggerWithBuffer creates a new logger with a buffer for testing and asserting the output.
func newTestLoggerWithBuffer() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{b: &bytes.Buffer{}}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	return logger, buf
}

func TestStartConfigWatcher(t *testing.T) {
	tmpdir := t.TempDir()
	path := tmpdir + "/config.yaml"
	rcv := &mockReceiver{}

	const tickInterval = time.Millisecond
	logger, buf := newTestLoggerWithBuffer()
	err := StartConfigWatcher(t.Context(), path, rcv, logger, tickInterval)
	require.NoError(t, err)

	defaultCfg := filterapi.MustLoadDefaultConfig()
	require.NoError(t, err)

	// Verify the default config has been loaded.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, defaultCfg, rcv.getConfig())
	}, 1*time.Second, tickInterval)

	// Verify the buffer contains the default config loading.
	require.Eventually(t, func() bool {
		return strings.Contains(buf.String(), "config file does not exist; loading default config")
	}, 1*time.Second, tickInterval, buf.String())

	// Create the initial config file.
	cfg := `
schema:
  name: OpenAI
modelNameHeaderKey: x-model-name
backends:
- name: kserve
  weight: 1
  schema:
    name: OpenAI
- name: awsbedrock
  weight: 10
  schema:
    name: AWSBedrock
- name: openai
  schema:
    name: OpenAI
`
	requireAtomicWriteFile(t, tickInterval, path, []byte(cfg), 0o600)

	// Initial loading should have happened.
	require.Eventually(t, func() bool {
		return !cmp.Equal(rcv.getConfig(), defaultCfg)
	}, 1*time.Second, tickInterval)
	firstCfg := rcv.getConfig()
	require.NotNil(t, firstCfg)
	require.Len(t, firstCfg.Backends, 3, buf.String())
	require.Equal(t, "kserve", firstCfg.Backends[0].Name)
	require.Equal(t, "awsbedrock", firstCfg.Backends[1].Name)
	require.Equal(t, "openai", firstCfg.Backends[2].Name)

	// Update the config file.
	cfg = `
schema:
  name: OpenAI
modelNameHeaderKey: x-model-name
backends:
- name: openai
  schema:
    name: OpenAI
`

	requireAtomicWriteFile(t, tickInterval, path, []byte(cfg), 0o600)

	// Verify the config has been updated.
	require.Eventually(t, func() bool {
		return !cmp.Equal(rcv.getConfig(), firstCfg)
	}, 1*time.Second, tickInterval)
	secondCfg := rcv.getConfig()
	require.NotNil(t, secondCfg)
	require.Len(t, secondCfg.Backends, 1, buf.String())
	require.Equal(t, "openai", secondCfg.Backends[0].Name)
}

// requireAtomicWriteFile creates a temporary file, writes the data to it, and then renames it to the final filename.
// This is an alternative to os.WriteFile but in a way that ensures the write is atomic.
func requireAtomicWriteFile(t *testing.T, tickInterval time.Duration, filename string, data []byte, perm os.FileMode) {
	// Sleep enough to ensure that the new file has a different modification time.
	// In practice, when the extproc is deployed, it will read from the k8s secret,
	// hence the file will have a different modification time (due to the delay caused by Kubernetes secret updates).
	time.Sleep(2 * tickInterval)

	tempFile, err := os.CreateTemp(t.TempDir(), filepath.Base(filename)+".tmp.*")
	require.NoError(t, err, "failed to create temporary file for atomic write")
	tempName := tempFile.Name()
	_, err = tempFile.Write(data)
	require.NoError(t, err, "failed to write data to temporary file")
	err = tempFile.Chmod(perm)
	require.NoError(t, err, "failed to set permissions on temporary file")
	err = tempFile.Close()
	require.NoError(t, err, "failed to close temporary file")
	err = os.Rename(tempName, filename)
	require.NoError(t, err, "failed to rename temporary file to final destination")
}
