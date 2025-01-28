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

	"github.com/envoyproxy/ai-gateway/filterconfig"
)

// mockReceiver is a mock implementation of Receiver.
type mockReceiver struct {
	cfg *filterconfig.Config
	mux sync.Mutex
}

// LoadConfig implements ConfigReceiver.
func (m *mockReceiver) LoadConfig(cfg *filterconfig.Config) error {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.cfg = cfg
	return nil
}

func (m *mockReceiver) getConfig() *filterconfig.Config {
	m.mux.Lock()
	defer m.mux.Unlock()
	return m.cfg
}

// NewTestLoggerWithBuffer creates a new logger with a buffer for testing and asserting the output.
func NewTestLoggerWithBuffer() (*slog.Logger, *bytes.Buffer) {
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
	logger, buf := NewTestLoggerWithBuffer()
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
