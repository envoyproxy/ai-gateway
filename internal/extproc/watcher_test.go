package extproc

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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

// newTestLoggerWithBuffer creates a new logger with a buffer for testing and asserting the output.
func newTestLoggerWithBuffer() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	return logger, buf
}

func TestStartConfigWatcher(t *testing.T) {
	tmpdir := t.TempDir()
	path := tmpdir + "/config.yaml"
	rcv := &mockReceiver{}

	require.NoError(t, os.WriteFile(path, []byte{}, 0o600))

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger, buf := newTestLoggerWithBuffer()
	err := StartConfigWatcher(ctx, path, rcv, logger, time.Millisecond*100)
	require.NoError(t, err)

	// Initial loading should have happened.
	require.Eventually(t, func() bool {
		return rcv.getConfig() != nil
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
