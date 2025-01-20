package filterconfig_test

import (
	"log/slog"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/envoyproxy/ai-gateway/filterconfig"
	"github.com/envoyproxy/ai-gateway/internal/extproc"
)

func TestDefaultConfig(t *testing.T) {
	server, err := extproc.NewServer(slog.Default(), extproc.NewProcessor)
	require.NoError(t, err)
	require.NotNil(t, server)

	var cfg filterconfig.Config
	err = yaml.Unmarshal([]byte(filterconfig.DefaultConfig), &cfg)
	require.NoError(t, err)

	err = server.LoadConfig(&cfg)
	require.NoError(t, err)
}

func TestUnmarshalConfigYaml(t *testing.T) {
	configPath := path.Join(t.TempDir(), "config.yaml")
	const config = `
schema:
  name: OpenAI
selectedBackendHeaderKey: x-envoy-ai-gateway-selected-backend
modelNameHeaderKey: x-envoy-ai-gateway-model
metadataNamespace: ai_gateway_llm_ns
llmRequestCosts:
- metadataKey: token_usage_key
  type: OutputToken
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
  - name: x-envoy-ai-gateway-model
    value: llama3.3333
- backends:
  - name: openai
    schema:
      name: OpenAI
  headers:
  - name: x-envoy-ai-gateway-model
    value: gpt4.4444
`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	cfg, err := filterconfig.UnmarshalConfigYaml(configPath)
	require.NoError(t, err)
	require.Equal(t, "ai_gateway_llm_ns", cfg.MetadataNamespace)
	require.Equal(t, "token_usage_key", cfg.LLMRequestCosts[0].MetadataKey)
	require.Equal(t, "OutputToken", string(cfg.LLMRequestCosts[0].Type))
	require.Equal(t, "OpenAI", string(cfg.Schema.Name))
	require.Equal(t, "x-envoy-ai-gateway-selected-backend", cfg.SelectedBackendHeaderKey)
	require.Equal(t, "x-envoy-ai-gateway-model", cfg.ModelNameHeaderKey)
	require.Len(t, cfg.Rules, 2)
	require.Equal(t, "llama3.3333", cfg.Rules[0].Headers[0].Value)
	require.Equal(t, "gpt4.4444", cfg.Rules[1].Headers[0].Value)
	require.Equal(t, "kserve", cfg.Rules[0].Backends[0].Name)
	require.Equal(t, 10, cfg.Rules[0].Backends[1].Weight)
	require.Equal(t, "AWSBedrock", string(cfg.Rules[0].Backends[1].Schema.Name))
	require.Equal(t, "openai", cfg.Rules[1].Backends[0].Name)
	require.Equal(t, "OpenAI", string(cfg.Rules[1].Backends[0].Schema.Name))
}
