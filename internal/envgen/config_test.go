// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package envgen

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

var ollamaLocalYAML string

func init() {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to get caller info")
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "../../examples/aigw/ollama.yaml"))
	if err != nil {
		panic(err)
	}
	ollamaLocalYAML = string(b)
}

var (
	//go:embed testdata/openai.yaml
	openaiDefaultYAML string

	//go:embed testdata/tars.yaml
	tarsYAML string

	//go:embed testdata/openrouter.yaml
	openrouterYAML string

	//go:embed testdata/llamastack.yaml
	llamastackYAML string
)

func TestGenerateOpenAIConfig(t *testing.T) {
	tests := []struct {
		name          string
		envVars       map[string]string
		expected      string
		expectedError error
	}{
		{
			name: "default (OpenAI)",
			envVars: map[string]string{
				"OPENAI_API_KEY": "sk-test123",
				// OPENAI_BASE_URL not set, defaults to https://api.openai.com/v1
			},
			expected: openaiDefaultYAML,
		},
		{
			name: "Ollama (localhost with port)",
			envVars: map[string]string{
				"OPENAI_API_KEY":  "ignored",
				"OPENAI_BASE_URL": "http://localhost:11434/v1",
			},
			expected: ollamaLocalYAML,
		},
		{
			name: "Tetrate Agent Router Service (https host)",
			envVars: map[string]string{
				"OPENAI_API_KEY":  "sk-test123",
				"OPENAI_BASE_URL": "https://api.router.tetrate.ai/v1",
			},
			expected: tarsYAML,
		},
		{
			name: "OpenRouter (https path prefix)",
			envVars: map[string]string{
				"OPENAI_API_KEY":  "sk-test123",
				"OPENAI_BASE_URL": "https://openrouter.ai/api/v1",
			},
			expected: openrouterYAML,
		},
		{
			name: "LlamaStack (localhost path prefix and port)",
			envVars: map[string]string{
				"OPENAI_API_KEY":  "sk-test123",
				"OPENAI_BASE_URL": "http://localhost:8321/v1/openai/v1",
			},
			expected: llamastackYAML,
		},
		{
			name: "missing required API key",
			envVars: map[string]string{
				"OPENAI_BASE_URL": "http://localhost:11434/v1",
			},
			expectedError: fmt.Errorf("OPENAI_API_KEY environment variable is required"),
		},
		{
			name: "invalid URL format",
			envVars: map[string]string{
				"OPENAI_API_KEY":  "sk-test123",
				"OPENAI_BASE_URL": ":::invalid",
			},
			expectedError: fmt.Errorf("invalid OPENAI_BASE_URL: parse \":::invalid\": missing protocol scheme"),
		},
		{
			name: "URL with no scheme",
			envVars: map[string]string{
				"OPENAI_API_KEY":  "sk-test123",
				"OPENAI_BASE_URL": "localhost:11434/v1",
			},
			expectedError: fmt.Errorf("invalid OPENAI_BASE_URL: missing hostname"),
		},
		{
			name: "URL with unsupported scheme",
			envVars: map[string]string{
				"OPENAI_API_KEY":  "sk-test123",
				"OPENAI_BASE_URL": "ftp://example.com/v1",
			},
			expectedError: fmt.Errorf("invalid OPENAI_BASE_URL: unsupported scheme \"ftp\""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear any existing env vars first
			t.Setenv("OPENAI_API_KEY", "")
			t.Setenv("OPENAI_BASE_URL", "")
			t.Setenv("OPENAI_ORG_ID", "")
			t.Setenv("OPENAI_PROJECT_ID", "")

			// Set test environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			// Generate config
			got, err := GenerateOpenAIConfig()

			// Check result
			if tt.expectedError != nil {
				require.Error(t, err)
				require.Equal(t, tt.expectedError.Error(), err.Error())
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, got)
			}
		})
	}
}

func TestParseURL(t *testing.T) {
	tests := []struct {
		name          string
		baseURL       string
		expected      ConfigData
		expectedError error
	}{
		{
			name:    "HTTPS with default port",
			baseURL: "https://api.openai.com/v1",
			expected: ConfigData{
				Hostname:         "api.openai.com",
				OriginalHostname: "api.openai.com",
				Port:             "443",
				Version:          "", // v1 is omitted for cleaner output
				NeedsTLS:         true,
			},
		},
		{
			name:    "localhost converted to nip.io",
			baseURL: "http://localhost:11434/v1",
			expected: ConfigData{
				Hostname:         "127.0.0.1.nip.io",
				OriginalHostname: "localhost",
				Port:             "11434",
				Version:          "", // v1 is omitted
				NeedsTLS:         false,
			},
		},
		{
			name:    "127.0.0.1 converted to nip.io",
			baseURL: "http://127.0.0.1:8080/v1",
			expected: ConfigData{
				Hostname:         "127.0.0.1.nip.io",
				OriginalHostname: "127.0.0.1",
				Port:             "8080",
				Version:          "", // v1 is omitted
				NeedsTLS:         false,
			},
		},
		{
			name:    "custom path preserved",
			baseURL: "https://custom.ai/v1beta/openai",
			expected: ConfigData{
				Hostname:         "custom.ai",
				OriginalHostname: "custom.ai",
				Port:             "443",
				Version:          "v1beta/openai",
				NeedsTLS:         true,
			},
		},
		{
			name:    "HTTP with default port 80",
			baseURL: "http://example.com/v1",
			expected: ConfigData{
				Hostname:         "example.com",
				OriginalHostname: "example.com",
				Port:             "80",
				Version:          "", // v1 is omitted
				NeedsTLS:         false,
			},
		},
		{
			name:    "empty path treated as no version",
			baseURL: "https://api.example.com",
			expected: ConfigData{
				Hostname:         "api.example.com",
				OriginalHostname: "api.example.com",
				Port:             "443",
				Version:          "",
				NeedsTLS:         true,
			},
		},
		{
			name:    "trailing slash ignored",
			baseURL: "https://api.example.com/",
			expected: ConfigData{
				Hostname:         "api.example.com",
				OriginalHostname: "api.example.com",
				Port:             "443",
				Version:          "",
				NeedsTLS:         true,
			},
		},
		{
			name:          "invalid URL",
			baseURL:       ":::invalid",
			expectedError: fmt.Errorf("invalid OPENAI_BASE_URL: parse \":::invalid\": missing protocol scheme"),
		},
		{
			name:          "missing hostname",
			baseURL:       "http:///path",
			expectedError: fmt.Errorf("invalid OPENAI_BASE_URL: missing hostname"),
		},
		{
			name:          "unsupported scheme",
			baseURL:       "ftp://example.com",
			expectedError: fmt.Errorf("invalid OPENAI_BASE_URL: unsupported scheme \"ftp\""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseURL(tt.baseURL)

			if tt.expectedError != nil {
				require.Error(t, err)
				require.Equal(t, tt.expectedError.Error(), err.Error())
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, *got)
			}
		})
	}
}
