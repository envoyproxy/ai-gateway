// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package autoconfig

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPopulateOpenAIEnvConfig(t *testing.T) {
	tests := []struct {
		name          string
		envVars       map[string]string
		expected      ConfigData
		expectedError error
	}{
		{
			name: "default (OpenAI)",
			envVars: map[string]string{
				"OPENAI_API_KEY": "sk-test123",
				// OPENAI_BASE_URL not set, defaults to https://api.openai.com/v1
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "api.openai.com",
						OriginalHostname: "api.openai.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName: "openai",
					SchemaName:  "OpenAI",
					Version:     "",
				},
			},
		},
		{
			name: "Azure OpenAI",
			envVars: map[string]string{
				"AZURE_OPENAI_API_KEY":  "azure-key-123",
				"AZURE_OPENAI_ENDPOINT": "https://my-resource.openai.azure.com",
				"OPENAI_API_VERSION":    "2024-02-15-preview",
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "my-resource.openai.azure.com",
						OriginalHostname: "my-resource.openai.azure.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName: "openai",
					SchemaName:  "AzureOpenAI",
					Version:     "2024-02-15-preview",
				},
			},
		},
		{
			name: "Azure OpenAI prioritized over standard OpenAI",
			envVars: map[string]string{
				"AZURE_OPENAI_API_KEY":  "azure-key-123",
				"AZURE_OPENAI_ENDPOINT": "https://my-resource.openai.azure.com",
				"OPENAI_API_VERSION":    "2024-02-15-preview",
				"OPENAI_API_KEY":        "sk-test123",
				"OPENAI_BASE_URL":       "https://api.openai.com/v1",
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "my-resource.openai.azure.com",
						OriginalHostname: "my-resource.openai.azure.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName: "openai",
					SchemaName:  "AzureOpenAI",
					Version:     "2024-02-15-preview",
				},
			},
		},
		{
			name: "Ollama (localhost with port)",
			envVars: map[string]string{
				"OPENAI_API_KEY":  "ignored",
				"OPENAI_BASE_URL": "http://localhost:11434/v1",
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "127.0.0.1.nip.io",
						OriginalHostname: "localhost",
						Port:             11434,
						NeedsTLS:         false,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName: "openai",
					SchemaName:  "OpenAI",
					Version:     "",
				},
			},
		},
		{
			name: "OpenAI with organization ID",
			envVars: map[string]string{
				"OPENAI_API_KEY": "sk-test123",
				"OPENAI_ORG_ID":  "org-test123",
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "api.openai.com",
						OriginalHostname: "api.openai.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName:    "openai",
					SchemaName:     "OpenAI",
					Version:        "",
					OrganizationID: "org-test123",
				},
			},
		},
		{
			name: "OpenAI with project ID",
			envVars: map[string]string{
				"OPENAI_API_KEY":    "sk-test123",
				"OPENAI_PROJECT_ID": "proj_test456",
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "api.openai.com",
						OriginalHostname: "api.openai.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName: "openai",
					SchemaName:  "OpenAI",
					Version:     "",
					ProjectID:   "proj_test456",
				},
			},
		},
		{
			name: "OpenAI with both org and project ID",
			envVars: map[string]string{
				"OPENAI_API_KEY":    "sk-test123",
				"OPENAI_ORG_ID":     "org-test123",
				"OPENAI_PROJECT_ID": "proj_test456",
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "api.openai.com",
						OriginalHostname: "api.openai.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName:    "openai",
					SchemaName:     "OpenAI",
					Version:        "",
					OrganizationID: "org-test123",
					ProjectID:      "proj_test456",
				},
			},
		},
		{
			name:          "missing required API key",
			envVars:       map[string]string{},
			expectedError: fmt.Errorf("either OPENAI_API_KEY or AZURE_OPENAI_API_KEY environment variable is required"),
		},
		{
			name: "Azure missing AZURE_OPENAI_ENDPOINT",
			envVars: map[string]string{
				"AZURE_OPENAI_API_KEY": "azure-key-123",
				"OPENAI_API_VERSION":   "2024-02-15-preview",
			},
			expectedError: fmt.Errorf("AZURE_OPENAI_ENDPOINT environment variable is required when AZURE_OPENAI_API_KEY is set"),
		},
		{
			name: "Azure missing OPENAI_API_VERSION",
			envVars: map[string]string{
				"AZURE_OPENAI_API_KEY":  "azure-key-123",
				"AZURE_OPENAI_ENDPOINT": "https://my-resource.openai.azure.com",
			},
			expectedError: fmt.Errorf("OPENAI_API_VERSION environment variable is required when AZURE_OPENAI_API_KEY is set"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear any existing env vars first
			t.Setenv("OPENAI_API_KEY", "")
			t.Setenv("OPENAI_BASE_URL", "")
			t.Setenv("OPENAI_ORG_ID", "")
			t.Setenv("OPENAI_PROJECT_ID", "")
			t.Setenv("AZURE_OPENAI_API_KEY", "")
			t.Setenv("AZURE_OPENAI_ENDPOINT", "")
			t.Setenv("OPENAI_API_VERSION", "")

			// Set test environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			// Test PopulateOpenAIEnvConfig
			data := &ConfigData{}
			err := PopulateOpenAIEnvConfig(data)

			// Check result
			if tt.expectedError != nil {
				require.Error(t, err)
				require.Equal(t, tt.expectedError.Error(), err.Error())
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, *data)
			}
		})
	}
}
