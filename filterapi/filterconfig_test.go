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

	cfg, raw := filterapi.MustLoadDefaultConfig()
	require.Equal(t, []byte(filterapi.DefaultConfig), raw)
	require.Equal(t, &filterapi.Config{
		Schema:                   filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		SelectedBackendHeaderKey: "x-ai-eg-selected-backend",
		ModelNameHeaderKey:       "x-ai-eg-model",
	}, cfg)

	err = server.LoadConfig(t.Context(), cfg)
	require.NoError(t, err)
}

func TestUnmarshalConfigYaml(t *testing.T) {
	configPath := path.Join(t.TempDir(), "config.yaml")
	const config = `
schema:
  name: OpenAI
selectedBackendHeaderKey: x-ai-eg-selected-backend
modelNameHeaderKey: x-ai-eg-model
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
    auth:
      apiKey:
        filename: apikey.txt
  - name: awsbedrock
    weight: 10
    schema:
      name: AWSBedrock
    auth:
      aws:
        credentialFileName: aws.txt
        region: us-east-1
  headers:
  - name: x-ai-eg-model
    value: llama3.3333
- backends:
  - name: openai
    weight: 1
    schema:
      name: OpenAI
  - name: azureopenai
    weight: 5
    schema:
      name: AzureOpenAI
      version: 2024-10-21
    auth:
      azure:
        filename: azure.txt
  headers:
  - name: x-ai-eg-model
    value: gpt4.4444
`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	cfg, raw, err := filterapi.UnmarshalConfigYaml(configPath)
	require.NoError(t, err)
	require.Equal(t, []byte(config), raw)

	expectedCfg := &filterapi.Config{
		Schema:                   filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
		SelectedBackendHeaderKey: "x-ai-eg-selected-backend",
		ModelNameHeaderKey:       "x-ai-eg-model",
		MetadataNamespace:        "ai_gateway_llm_ns",
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{
				MetadataKey: "token_usage_key",
				Type:        filterapi.LLMRequestCostTypeOutputToken,
			},
		},
		Rules: []filterapi.RouteRule{
			{
				Headers: []filterapi.HeaderMatch{
					{Name: "x-ai-eg-model", Value: "llama3.3333"},
				},
				Backends: []filterapi.Backend{
					{
						Name:   "kserve",
						Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
						Weight: 1,
						Auth: &filterapi.BackendAuth{
							APIKey: &filterapi.APIKeyAuth{
								Filename: "apikey.txt",
							},
						},
					},
					{
						Name:   "awsbedrock",
						Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock},
						Weight: 10,
						Auth: &filterapi.BackendAuth{
							AWSAuth: &filterapi.AWSAuth{
								CredentialFileName: "aws.txt",
								Region:             "us-east-1",
							},
						},
					},
				},
			},
			{
				Headers: []filterapi.HeaderMatch{
					{Name: "x-ai-eg-model", Value: "gpt4.4444"},
				},
				Backends: []filterapi.Backend{
					{
						Name:   "openai",
						Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI},
						Weight: 1,
					},
					{
						Name:   "azureopenai",
						Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAzureOpenAI, Version: "2024-10-21"},
						Weight: 5,
						Auth: &filterapi.BackendAuth{
							AzureAuth: &filterapi.AzureAuth{
								Filename: "azure.txt",
							},
						},
					},
				},
			},
		},
	}

	require.Equal(t, expectedCfg, cfg)

	t.Run("not found", func(t *testing.T) {
		_, _, err := filterapi.UnmarshalConfigYaml("not-found.yaml")
		require.Error(t, err)
		require.True(t, os.IsNotExist(err))
	})
	t.Run("invalid", func(t *testing.T) {
		const invalidConfig = `{wefaf3q20,9u,f02`
		require.NoError(t, os.WriteFile(configPath, []byte(invalidConfig), 0o600))
		_, _, err := filterapi.UnmarshalConfigYaml(configPath)
		require.Error(t, err)
	})
}
