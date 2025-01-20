package extproc

import (
	"context"
	"log/slog"
	"os"
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

func TestStartConfigWatcher(t *testing.T) {
	tmpdir := t.TempDir()
	path := tmpdir + "/config.yaml"
	rcv := &mockReceiver{}

	require.NoError(t, os.WriteFile(path, []byte{}, 0o600))

	// Create the initial config file.
	cfg := `
schema:
  name: OpenAI
selectedBackendHeaderKey: x-eaig-selected-backend
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
	err := StartConfigWatcher(ctx, path, rcv, slog.Default(), time.Millisecond*100)
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
selectedBackendHeaderKey: x-eaig-selected-backend
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

	// Log should contain the updated loading.
	require.Eventually(t, func() bool {
		return rcv.getConfig() != firstCfg
	}, 1*time.Second, 100*time.Millisecond)
	require.NotEqual(t, firstCfg, rcv.getConfig())
}
