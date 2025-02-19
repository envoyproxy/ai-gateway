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
	"strings"
	"sync"
	"testing"
	"time"

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

	logger, buf := newTestLoggerWithBuffer()
	err := StartConfigWatcher(t.Context(), path, rcv, logger, time.Millisecond*100)
	require.NoError(t, err)

	defaultCfg, _ := filterapi.MustLoadDefaultConfig()
	require.NoError(t, err)

	// Verify the default config has been loaded.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, defaultCfg, rcv.getConfig())
	}, 1*time.Second, 100*time.Millisecond)

	// Verify the buffer contains the default config loading.
	require.Eventually(t, func() bool {
		return strings.Contains(buf.String(), "config file does not exist; loading default config")
	}, 1*time.Second, 100*time.Millisecond, buf.String())

	// Create the initial config file.
	cfg := `
schema:
  name: OpenAI
selectedBackendHeaderKey: x-ai-eg-selected-backend
modelNameHeaderKey: x-model-name
rules:
- backends:
  - name: kserve
    weight: 1
    schema:
      name: OpenAI
  - name: awsbedrock
    weight: 10
    schema:
      name: AWSBedrock
  headers:
  - name: x-model-name
    value: llama3.3333
- backends:
  - name: openai
    schema:
      name: OpenAI
  headers:
  - name: x-model-name
    value: gpt4.4444
`
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))

	// Initial loading should have happened.
	require.Eventually(t, func() bool {
		return rcv.getConfig() != defaultCfg
	}, 1*time.Second, 100*time.Millisecond)
	firstCfg := rcv.getConfig()
	require.NotNil(t, firstCfg)

	// Update the config file.
	cfg = `
schema:
  name: OpenAI
selectedBackendHeaderKey: x-ai-eg-selected-backend
modelNameHeaderKey: x-model-name
rules:
- backends:
  - name: openai
    schema:
      name: OpenAI
  headers:
  - name: x-model-name
    value: gpt4.4444
`

	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))

	// Verify the config has been updated.
	require.Eventually(t, func() bool {
		return rcv.getConfig() != firstCfg
	}, 1*time.Second, 100*time.Millisecond)
	require.NotEqual(t, firstCfg, rcv.getConfig())

	// Verify the buffer contains the updated loading.
	require.Eventually(t, func() bool {
		return strings.Contains(buf.String(), "loading a new config")
	}, 1*time.Second, 100*time.Millisecond, buf.String())

	// Verify the buffer contains the config line changed
	require.Eventually(t, func() bool {
		return strings.Contains(buf.String(), "config line changed")
	}, 1*time.Second, 100*time.Millisecond, buf.String())
}

func TestDiff(t *testing.T) {
	logger, buf := newTestLoggerWithBuffer()
	cw := &configWatcher{
		l: logger,
	}

	oldConfig := `schema:
	name: Foo`
	newConfig := `schema:
	name: Bar`

	expectedLog := `msg="config line changed" line=2 path="" old="name: Foo" new="name: Bar"`
	cw.diff(oldConfig, newConfig)
	require.Eventually(t, func() bool {
		return strings.Contains(buf.String(), expectedLog)
	}, 1*time.Second, 100*time.Millisecond, buf.String())
}
