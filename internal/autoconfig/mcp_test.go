// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package autoconfig

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddMCPServersConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    *MCPServers
		expected ConfigData
	}{
		{
			name: "add GitHub MCP server with auth",
			input: &MCPServers{
				McpServers: map[string]MCPServer{
					"github": {
						Type: "http",
						URL:  "https://api.githubcopilot.com/mcp/x/issues/readonly",
						Headers: map[string]string{
							"Authorization": "Bearer ${GITHUB_MCP_TOKEN}",
						},
						IncludeTools: []string{"list_issues", "get_issue"},
					},
				},
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "github",
						Hostname:         "api.githubcopilot.com",
						OriginalHostname: "api.githubcopilot.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				MCPBackendRefs: []MCPBackendRef{
					{
						BackendName:  "github",
						Path:         "/mcp/x/issues/readonly",
						IncludeTools: []string{"list_issues", "get_issue"},
						APIKey:       "${GITHUB_MCP_TOKEN}",
						Headers:      map[string]string{},
					},
				},
			},
		},
		{
			name: "add MCP server with non-auth headers",
			input: &MCPServers{
				McpServers: map[string]MCPServer{
					"custom": {
						Type: "http",
						URL:  "https://mcp.example.com/path",
						Headers: map[string]string{
							"Authorization":   "Bearer ${TOKEN}",
							"X-Custom-Header": "custom-value",
							"X-Another":       "another-value",
						},
					},
				},
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "custom",
						Hostname:         "mcp.example.com",
						OriginalHostname: "mcp.example.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				MCPBackendRefs: []MCPBackendRef{
					{
						BackendName: "custom",
						Path:        "/path",
						APIKey:      "${TOKEN}",
						Headers: map[string]string{
							"X-Custom-Header": "custom-value",
							"X-Another":       "another-value",
						},
					},
				},
			},
		},
		{
			name: "add multiple MCP servers",
			input: &MCPServers{
				McpServers: map[string]MCPServer{
					"kiwi": {
						Type: "http",
						URL:  "https://mcp.kiwi.com",
					},
					"github": {
						Type: "http",
						URL:  "https://api.githubcopilot.com/mcp/x/issues/readonly",
						Headers: map[string]string{
							"Authorization": "Bearer ${GITHUB_MCP_TOKEN}",
						},
						IncludeTools: []string{"list_issues", "get_issue"},
					},
				},
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "github",
						Hostname:         "api.githubcopilot.com",
						OriginalHostname: "api.githubcopilot.com",
						Port:             443,
						NeedsTLS:         true,
					},
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
						BackendName:  "github",
						Path:         "/mcp/x/issues/readonly",
						IncludeTools: []string{"list_issues", "get_issue"},
						APIKey:       "${GITHUB_MCP_TOKEN}",
						Headers:      map[string]string{},
					},
					{
						BackendName: "kiwi",
						Path:        "/",
						Headers:     map[string]string{},
					},
				},
			},
		},
		{
			name:     "nil input does nothing",
			input:    nil,
			expected: ConfigData{},
		},
		{
			name:     "empty input does nothing",
			input:    &MCPServers{McpServers: map[string]MCPServer{}},
			expected: ConfigData{},
		},
		{
			name: "skip unsupported server types",
			input: &MCPServers{
				McpServers: map[string]MCPServer{
					"fetch": {
						Type: "stdio",
						URL:  "",
					},
					"github": {
						Type: "http",
						URL:  "https://api.githubcopilot.com/mcp/x/issues/readonly",
					},
				},
			},
			expected: ConfigData{
				Backends: []Backend{
					{
						Name:             "github",
						Hostname:         "api.githubcopilot.com",
						OriginalHostname: "api.githubcopilot.com",
						Port:             443,
						NeedsTLS:         true,
					},
				},
				MCPBackendRefs: []MCPBackendRef{
					{
						BackendName: "github",
						Path:        "/mcp/x/issues/readonly",
						Headers:     map[string]string{},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &ConfigData{}
			err := AddMCPServers(data, tt.input)

			require.NoError(t, err)
			require.Equal(t, tt.expected, *data)
		})
	}
}
