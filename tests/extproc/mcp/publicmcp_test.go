// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/tests/internal/testenvironment"
)

func TestPublicMCPServers(t *testing.T) {
	mcpConfig := &filterapi.MCPConfig{
		BackendListenerAddr: "http://127.0.0.1:9999",
		Routes: []filterapi.MCPRoute{
			{
				Name: "test-route",
				Backends: []filterapi.MCPBackend{
					{Name: "context7", Path: "/mcp"},
					{Name: "kiwi", Path: "/"},
				},
			},
		},
	}

	githubConfigured := false
	if githubAccessToken := os.Getenv("TEST_GITHUB_ACCESS_TOKEN"); githubAccessToken != "" {
		envoyConfig = strings.ReplaceAll(envoyConfig, "GITHUB_ACCESS_TOKEN_PLACEHOLDER", githubAccessToken)
		mcpConfig.Routes[0].Backends = append(mcpConfig.Routes[0].Backends,
			filterapi.MCPBackend{
				Name: "github",
				Path: "/mcp/readonly",
				ToolSelector: &filterapi.MCPToolSelector{
					IncludeRegex: []string{".*pull_requests?.*", ".*issues?.*"},
				},
			},
		)
		githubConfigured = true
	}

	config, err := json.Marshal(filterapi.Config{MCPConfig: mcpConfig})
	require.NoError(t, err)

	env := testenvironment.StartTestEnvironment(t,
		func(_ testing.TB, _ io.Writer, _ map[string]int) {}, map[string]int{"backend_listener": 9999},
		"", string(config), nil, envoyConfig, true, true, 120*time.Second,
	)

	url := fmt.Sprintf("http://localhost:%d%s", env.EnvoyListenerPort(), defaultMCPPath)
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "public-mcp-client", Version: "0.1.0"}, &mcp.ClientOptions{})
	session, err := mcpClient.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: url,
	}, nil)
	require.NoError(t, err)
	// Intentionally not using t.Cleanup to close the session so that we can check to see if it closes cleanly.
	// If we do this in t.Cleanup, it will happen after the Envoy is terminating, and we won't see any valid "closure" error.
	defer func() { _ = session.Close() }()

	t.Run("tools/list", func(t *testing.T) {
		resp, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
		require.NoError(t, err)
		t.Logf("tools/list response: %+v", resp)
		var names []string
		for _, tool := range resp.Tools {
			schemastring, err := json.MarshalIndent(tool.InputSchema, "", "  ")
			require.NoError(t, err)
			t.Logf("[tool=%s]%s\n\n%s\n", tool.Name, schemastring, tool.Description)
			names = append(names, tool.Name)
		}

		exps := []string{
			"context7__resolve-library-id",
			"context7__get-library-docs",
			"kiwi__search-flight",
			"kiwi__feedback-to-devs",
		}

		if githubConfigured {
			exps = append(exps, "github__get_issue")
			exps = append(exps, "github__get_issue_comments")
			exps = append(exps, "github__pull_request_read")
			exps = append(exps, "github__list_issue_types")
			exps = append(exps, "github__list_issues")
			exps = append(exps, "github__list_pull_requests")
			exps = append(exps, "github__list_sub_issues")
			exps = append(exps, "github__search_issues")
			exps = append(exps, "github__search_pull_requests")
		}

		// Do not use ElementsMatch so we can ensure there are no unexpected tools.
		for _, exp := range exps {
			require.Contains(t, names, exp, "expected tool not found: %s", exp)
		}
	})

	t.Run("tool calls", func(t *testing.T) {
		type callToolTest struct {
			toolName string
			params   map[string]any
		}
		tests := []callToolTest{
			{
				toolName: "context7__resolve-library-id",
				params: map[string]any{
					"libraryName": "non-existent",
				},
			},
			{
				toolName: "context7__get-library-docs",
				params: map[string]any{
					"context7CompatibleLibraryID": "/mongodb/docs",
				},
			},
			{
				toolName: "kiwi__search-flight",
				params: map[string]any{
					"flyFrom":                "LAX",
					"flyTo":                  "HND",
					"departureDate":          "01/01/2026",
					"departureDateFlexRange": 1,
					"returnDate":             "02/01/2026",
					"returnDateFlexRange":    1,
					"passengers": map[string]any{
						"adults":   1,
						"children": 0,
						"infants":  0,
					},
					"cabinClass": "M",
					"sort":       "date",
					"curr":       "USD",
					"locale":     "en",
				},
			},
		}
		if githubConfigured {
			tests = append(tests, callToolTest{
				toolName: "github__pull_request_read",
				params: map[string]any{
					"owner":      "envoyproxy",
					"repo":       "ai-gateway",
					"method":     "get",
					"pullNumber": 1,
				},
			})
		}
		for _, tc := range tests {
			t.Run(tc.toolName, func(t *testing.T) {
				t.Parallel()
				resp, err := session.CallTool(t.Context(), &mcp.CallToolParams{
					Name:      tc.toolName,
					Arguments: tc.params,
				})
				require.NoError(t, err)
				encoded, err := json.MarshalIndent(resp, "", "  ")
				require.NoError(t, err)
				t.Logf("[[response]]\n%s", string(encoded))
				require.False(t, resp.IsError)
			})
		}
	})
}
