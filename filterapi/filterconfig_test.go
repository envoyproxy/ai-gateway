// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package filterapi_test

import (
	"log/slog"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/extproc"
)

func TestDefaultConfig(t *testing.T) {
	server, err := extproc.NewServer(slog.Default())
	require.NoError(t, err)
	require.NotNil(t, server)

	cfg := filterapi.MustLoadDefaultConfig()
	require.Equal(t, &filterapi.Config{
		Schema:                 filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		SelectedRouteHeaderKey: "x-ai-eg-selected-route",
		ModelNameHeaderKey:     "x-ai-eg-model",
	}, cfg)

	err = server.LoadConfig(t.Context(), cfg)
	require.NoError(t, err)
}

func TestUnmarshalConfigYaml(t *testing.T) {
	configPath := path.Join(t.TempDir(), "config.yaml")
	const config = `
schema:
  name: OpenAI
selectedRouteHeaderKey: x-ai-eg-selected-route
modelNameHeaderKey: x-ai-eg-model
metadataNamespace: ai_gateway_llm_ns
llmRequestCosts:
- metadataKey: token_usage_key
  type: OutputToken
rules:
- headers:
  - name: x-ai-eg-model
    value: llama3.3333
  name: llama3-route
  backends:
  - name: openai
    schema:
      name: OpenAI
  - name: azureopenai
    schema:
      name: AzureOpenAI
      version: 2024-10-21
    auth:
      azure:
        accessToken: "azureazureazureazureazureazure"
  - name: awsbedrock
    schema:
      name: AWSBedrock
    auth:
      aws:
        CredentialFileLiteral: "awsawsawsawsawsawsaws"
        region: us-east-1
  - name: kserve
    schema:
      name: OpenAI
    auth:
      apiKey:
        key: kservekservekservekservekservekservekservekservekserve
- headers:
  - name: x-ai-eg-model
    value: gpt4.4444
  name: gpt4-route
`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	cfg, err := filterapi.UnmarshalConfigYaml(configPath)
	require.NoError(t, err)

	expectedCfg := &filterapi.Config{
		Schema:                 filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		SelectedRouteHeaderKey: "x-ai-eg-selected-route",
		ModelNameHeaderKey:     "x-ai-eg-model",
		MetadataNamespace:      "ai_gateway_llm_ns",
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{
				MetadataKey: "token_usage_key",
				Type:        filterapi.LLMRequestCostTypeOutputToken,
			},
		},
		Rules: []filterapi.RouteRule{
			{
				Name: "llama3-route", Headers: []filterapi.HeaderMatch{{Name: "x-ai-eg-model", Value: "llama3.3333"}},
				Backends: []filterapi.Backend{
					{
						Name:   "openai",
						Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
					},
					{
						Name:   "azureopenai",
						Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI, Version: "2024-10-21"},
						Auth: &filterapi.BackendAuth{
							AzureAuth: &filterapi.AzureAuth{
								AccessToken: "azureazureazureazureazureazure",
							},
						},
					},
					{
						Name:   "awsbedrock",
						Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock},
						Auth: &filterapi.BackendAuth{
							AWSAuth: &filterapi.AWSAuth{
								CredentialFileLiteral: "awsawsawsawsawsawsaws",
								Region:                "us-east-1",
							},
						},
					},
					{
						Name:   "kserve",
						Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
						Auth: &filterapi.BackendAuth{
							APIKey: &filterapi.APIKeyAuth{
								Key: "kservekservekservekservekservekservekservekservekserve",
							},
						},
					},
				},
			},
			{Name: "gpt4-route", Headers: []filterapi.HeaderMatch{{Name: "x-ai-eg-model", Value: "gpt4.4444"}}},
		},
	}

	require.Equal(t, expectedCfg, cfg)

	t.Run("not found", func(t *testing.T) {
		_, err := filterapi.UnmarshalConfigYaml("not-found.yaml")
		require.Error(t, err)
		require.True(t, os.IsNotExist(err))
	})
	t.Run("invalid", func(t *testing.T) {
		const invalidConfig = `{wefaf3q20,9u,f02`
		require.NoError(t, os.WriteFile(configPath, []byte(invalidConfig), 0o600))
		_, err := filterapi.UnmarshalConfigYaml(configPath)
		require.Error(t, err)
	})
}
