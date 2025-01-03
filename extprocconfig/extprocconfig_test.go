package extprocconfig

import (
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnmarshalConfigYaml(t *testing.T) {
	configPath := path.Join(t.TempDir(), "config.yaml")
	const config = `
inputSchema:
  schema: OpenAI
backendRoutingHeaderKey: x-backend-name
modelNameHeaderKey: x-model-name
tokenUsageMetadata:
  namespace: ai_gateway_llm_ns
  key: token_usage_key
rules:
- backends:
  - name: kserve
    weight: 1
    outputSchema:
      schema: OpenAI
  - name: awsbedrock
    weight: 10
    outputSchema:
      schema: AWSBedrock
  headers:
  - name: x-model-name
    value: llama3.3333
- backends:
  - name: openai
    outputSchema:
      schema: OpenAI
  headers:
  - name: x-model-name
    value: gpt4.4444
`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	cfg, err := UnmarshalConfigYaml(configPath)
	require.NoError(t, err)
	require.Equal(t, "ai_gateway_llm_ns", cfg.TokenUsageMetadata.Namespace)
	require.Equal(t, "token_usage_key", cfg.TokenUsageMetadata.Key)
	require.Equal(t, "OpenAI", string(cfg.InputSchema.Schema))
	require.Equal(t, "x-backend-name", cfg.BackendRoutingHeaderKey)
	require.Equal(t, "x-model-name", cfg.ModelNameHeaderKey)
	require.Len(t, cfg.Rules, 2)
	require.Equal(t, "llama3.3333", cfg.Rules[0].Headers[0].Value)
	require.Equal(t, "gpt4.4444", cfg.Rules[1].Headers[0].Value)
	require.Equal(t, "kserve", cfg.Rules[0].Backends[0].Name)
	require.Equal(t, 10, cfg.Rules[0].Backends[1].Weight)
	require.Equal(t, "AWSBedrock", string(cfg.Rules[0].Backends[1].OutputSchema.Schema))
	require.Equal(t, "openai", cfg.Rules[1].Backends[0].Name)
	require.Equal(t, "OpenAI", string(cfg.Rules[1].Backends[0].OutputSchema.Schema))
}
