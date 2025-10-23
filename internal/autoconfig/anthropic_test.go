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

func TestPopulateAnthropicEnvConfig(t *testing.T) {
	tests := []struct {
		name          string
		envVars       map[string]string
		expected      ConfigData
		expectedError error
	}{
		{
			name: "default (Anthropic)",
			envVars: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-test123",
				// ANTHROPIC_BASE_URL not set, defaults to https://api.anthropic.com/v1
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "anthropic",
						Hostname:         "api.anthropic.com",
						OriginalHostname: "api.anthropic.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				Anthropic: &AnthropicConfig{
					BackendName: "anthropic",
					SchemaName:  "Anthropic",
					Version:     "",
				},
			},
		},
		{
			name: "Anthropic with custom base URL",
			envVars: map[string]string{
				"ANTHROPIC_API_KEY":  "sk-ant-test123",
				"ANTHROPIC_BASE_URL": "https://custom.anthropic.com/v2",
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "anthropic",
						Hostname:         "custom.anthropic.com",
						OriginalHostname: "custom.anthropic.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				Anthropic: &AnthropicConfig{
					BackendName: "anthropic",
					SchemaName:  "Anthropic",
					Version:     "v2",
				},
			},
		},
		{
			name: "Anthropic with localhost",
			envVars: map[string]string{
				"ANTHROPIC_API_KEY":  "sk-ant-test123",
				"ANTHROPIC_BASE_URL": "http://localhost:8080/v1",
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "anthropic",
						Hostname:         "127.0.0.1.nip.io",
						OriginalHostname: "localhost",
						Port:             8080,
						NeedsTLS:         false,
					},
				},
				Anthropic: &AnthropicConfig{
					BackendName: "anthropic",
					SchemaName:  "Anthropic",
					Version:     "",
				},
			},
		},
		{
			name:          "missing required API key",
			envVars:       map[string]string{},
			expectedError: fmt.Errorf("ANTHROPIC_API_KEY environment variable is required"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear any existing env vars first
			t.Setenv("ANTHROPIC_API_KEY", "")
			t.Setenv("ANTHROPIC_BASE_URL", "")

			// Set test environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			// Test PopulateAnthropicEnvConfig
			data := &ConfigData{}
			err := PopulateAnthropicEnvConfig(data)

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
