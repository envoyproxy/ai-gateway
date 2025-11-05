// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package autoconfig

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

	//go:embed testdata/llamastack-ip-address.yaml
	llamastackIPAddressYAML string

	//go:embed testdata/azure-openai.yaml
	azureOpenAIYAML string

	//go:embed testdata/openai-with-org.yaml
	openaiWithOrgYAML string

	//go:embed testdata/openai-with-project.yaml
	openaiWithProjectYAML string

	//go:embed testdata/openai-with-org-and-project.yaml
	openaiWithOrgAndProjectYAML string

	//go:embed testdata/azure-openai-with-org-and-project.yaml
	azureOpenAIWithOrgAndProjectYAML string

	//go:embed testdata/debug.yaml
	debugYAML string

	//go:embed testdata/kiwi.yaml
	kiwiYAML string

	//go:embed testdata/openai-github.yaml
	openaiGithubYAML string

	//go:embed testdata/anthropic.yaml
	anthropicYAML string
)

func TestWriteConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    ConfigData
		expected string
	}{
		{
			name: "default (OpenAI)",
			input: ConfigData{
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
			expected: openaiDefaultYAML,
		},
		{
			name: "Azure OpenAI",
			input: ConfigData{
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
			expected: azureOpenAIYAML,
		},
		{
			name: "Ollama (localhost with port)",
			input: ConfigData{
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
			expected: ollamaLocalYAML,
		},
		{
			name: "Tetrate Agent Router Service (https host)",
			input: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "api.router.tetrate.ai",
						OriginalHostname: "api.router.tetrate.ai",
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
			expected: tarsYAML,
		},
		{
			name: "OpenRouter (https path prefix)",
			input: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "openrouter.ai",
						OriginalHostname: "openrouter.ai",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName: "openai",
					SchemaName:  "OpenAI",
					Version:     "api/v1",
				},
			},
			expected: openrouterYAML,
		},
		{
			name: "LlamaStack (localhost path prefix and port)",
			input: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "127.0.0.1.nip.io",
						OriginalHostname: "localhost",
						Port:             8321,
						NeedsTLS:         false,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName: "openai",
					SchemaName:  "OpenAI",
					Version:     "v1/openai/v1",
				},
			},
			expected: llamastackYAML,
		},
		{
			name: "LlamaStack (local IP address)",
			input: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						IPAddress:        "192.168.0.10",
						OriginalHostname: "192.168.0.10",
						Port:             8321,
						NeedsTLS:         false,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName: "openai",
					SchemaName:  "OpenAI",
					Version:     "v1/openai/v1",
				},
			},
			expected: llamastackIPAddressYAML,
		},
		{
			name: "OpenAI with organization ID",
			input: ConfigData{
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
			expected: openaiWithOrgYAML,
		},
		{
			name: "OpenAI with project ID",
			input: ConfigData{
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
			expected: openaiWithProjectYAML,
		},
		{
			name: "OpenAI with both org and project ID",
			input: ConfigData{
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
			expected: openaiWithOrgAndProjectYAML,
		},
		{
			name: "Azure OpenAI with org and project ID",
			input: ConfigData{
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
					BackendName:    "openai",
					SchemaName:     "AzureOpenAI",
					Version:        "2024-02-15-preview",
					OrganizationID: "org-test123",
					ProjectID:      "proj_test456",
				},
			},
			expected: azureOpenAIWithOrgAndProjectYAML,
		},
		{
			name: "OpenAI with debug logging and custom Envoy version",
			input: ConfigData{
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
				Debug:        true,
				EnvoyVersion: "1.35.0",
			},
			// TODO: raise issue in EG to allow doing effectively this:
			// "--component-log-level ext_proc:trace,http:debug,connection:debug"
			expected: debugYAML,
		},
		{
			name: "Kiwi MCP server (MCP-only, no OpenAI)",
			input: ConfigData{
				Backends: []Backend{
					{
						Name:             "kiwi",
						Hostname:         "mcp.kiwi.com",
						OriginalHostname: "mcp.kiwi.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				MCPBackendRefs: []MCPBackendRef{
					{
						BackendName: "kiwi",
						Path:        "/",
					},
				},
			},
			expected: kiwiYAML,
		},
		{
			name: "OpenAI with GitHub MCP server",
			input: ConfigData{
				Backends: []Backend{
					{
						Name:             "openai",
						Hostname:         "api.openai.com",
						OriginalHostname: "api.openai.com",
						Port:             443,
						NeedsTLS:         true,
					},
					{
						Name:             "github",
						Hostname:         "api.githubcopilot.com",
						OriginalHostname: "api.githubcopilot.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				OpenAI: &OpenAIConfig{
					BackendName: "openai",
					SchemaName:  "OpenAI",
					Version:     "",
				},
				MCPBackendRefs: []MCPBackendRef{
					{
						BackendName:  "github",
						Path:         "/mcp/x/issues/readonly",
						APIKey:       "${GITHUB_MCP_TOKEN}",
						IncludeTools: []string{"issue_read", "list_issues"},
					},
				},
			},
			expected: openaiGithubYAML,
		},
		{
			name: "default (Anthropic)",
			input: ConfigData{
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
			expected: anthropicYAML,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WriteConfig(&tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestParseURL(t *testing.T) {
	tests := []struct {
		name          string
		baseURL       string
		expected      parsedURL
		expectedError error
	}{
		{
			name:    "HTTPS with default port",
			baseURL: "https://api.openai.com/v1",
			expected: parsedURL{
				hostname:         "api.openai.com",
				originalHostname: "api.openai.com",
				port:             443,
				path:             "/v1",
				version:          "", // v1 is omitted for cleaner output
				needsTLS:         true,
			},
		},
		{
			name:    "localhost converted to nip.io",
			baseURL: "http://localhost:11434/v1",
			expected: parsedURL{
				hostname:         "127.0.0.1.nip.io",
				originalHostname: "localhost",
				port:             11434,
				path:             "/v1",
				version:          "", // v1 is omitted
				needsTLS:         false,
			},
		},
		{
			name:    "127.0.0.1 converted to nip.io",
			baseURL: "http://127.0.0.1:8080/v1",
			expected: parsedURL{
				hostname:         "127.0.0.1.nip.io",
				originalHostname: "127.0.0.1",
				port:             8080,
				path:             "/v1",
				version:          "", // v1 is omitted
				needsTLS:         false,
			},
		},
		{
			name:    "IP address URL",
			baseURL: "http://192.168.0.10:8080/v1",
			expected: parsedURL{
				ipAddress:        "192.168.0.10",
				originalHostname: "192.168.0.10",
				port:             8080,
				path:             "/v1",
				version:          "", // v1 is omitted
				needsTLS:         false,
			},
		},
		{
			name:    "custom path preserved",
			baseURL: "https://custom.ai/v1beta/openai",
			expected: parsedURL{
				hostname:         "custom.ai",
				originalHostname: "custom.ai",
				port:             443,
				path:             "/v1beta/openai",
				version:          "v1beta/openai",
				needsTLS:         true,
			},
		},
		{
			name:    "HTTP with default port 80",
			baseURL: "http://example.com/v1",
			expected: parsedURL{
				hostname:         "example.com",
				originalHostname: "example.com",
				port:             80,
				path:             "/v1",
				version:          "", // v1 is omitted
				needsTLS:         false,
			},
		},
		{
			name:    "empty path treated as no version",
			baseURL: "https://api.example.com",
			expected: parsedURL{
				hostname:         "api.example.com",
				originalHostname: "api.example.com",
				port:             443,
				path:             "/",
				version:          "",
				needsTLS:         true,
			},
		},
		{
			name:    "trailing slash ignored",
			baseURL: "https://api.example.com/",
			expected: parsedURL{
				hostname:         "api.example.com",
				originalHostname: "api.example.com",
				port:             443,
				path:             "/",
				version:          "",
				needsTLS:         true,
			},
		},
		{
			name:          "invalid URL",
			baseURL:       ":::invalid",
			expectedError: fmt.Errorf("invalid base URL: parse \":::invalid\": missing protocol scheme"),
		},
		{
			name:          "missing hostname",
			baseURL:       "http:///path",
			expectedError: fmt.Errorf("invalid base URL: missing hostname"),
		},
		{
			name:          "unsupported scheme",
			baseURL:       "ftp://example.com",
			expectedError: fmt.Errorf("invalid base URL: unsupported scheme \"ftp\""),
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
