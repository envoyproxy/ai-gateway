// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2emcp

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"sort"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

var (
	examplesDir = path.Join(internaltesting.FindProjectRoot(), "examples", "mcp")

	// Adjust these as services update, as they can be added, removed or renamed

	allNonGithubTools = []string{
		"context7__query-docs",
		"context7__resolve-library-id",
		"kiwi__feedback-to-devs",
		"kiwi__search-flight",
	}
)

func TestMCP_standalone(t *testing.T) {
	ght := os.Getenv("TEST_GITHUB_ACCESS_TOKEN")
	githubConfigured := ght != ""
	if githubConfigured {
		t.Setenv("GITHUB_ACCESS_TOKEN", ght)
	}

	exampleYaml := path.Join(examplesDir, "mcp_example.yaml")
	startAIGWCLI(t, aigwBin, nil, "run", "--debug", exampleYaml)

	url := fmt.Sprintf("http://localhost:%d/mcp", 1975)
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "public-mcp-client", Version: "0.1.0"}, &mcp.ClientOptions{})
	session, err := mcpClient.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: url,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	t.Run("tools/list", func(t *testing.T) {
		resp, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
		require.NoError(t, err)

		var actualNames []string
		for _, tool := range resp.Tools {
			actualNames = append(actualNames, tool.Name)
		}
		sort.Strings(actualNames)

		// GitHub tool listing is flaky, so only check non-GitHub tools here.
		for _, toolName := range allNonGithubTools {
			require.Contains(t, actualNames, toolName)
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
					"libraryName": "envoyproxy/ai-gateway",
					"query":       "how can I route to an LLM bakend",
				},
			},
			{
				toolName: "context7__query-docs",
				params: map[string]any{
					"libraryId": "/envoyproxy/ai-gateway",
					"query":     "how can I route to an LLM bakend",
				},
			},
			{
				toolName: "kiwi__search-flight",
				params: map[string]any{
					"flyFrom":                "LAX",
					"flyTo":                  "HND",
					"departureDate":          "01/12/2026",
					"departureDateFlexRange": 1,
					"returnDate":             "02/12/2026",
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
				require.False(t, resp.IsError, "[[response]]\n%s", string(encoded))
			})
		}
	})
}

// authTransport is an http.RoundTripper that adds Authorization header to requests.
type authTransport struct {
	token string
	base  http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

func TestMCP_standalone_oauth(t *testing.T) {
	startAIGWCLI(t, aigwBin, nil, "run", "--debug", path.Join(examplesDir, "mcp_oauth_example.yaml"))

	url := fmt.Sprintf("http://localhost:%d/mcp", 1975)

	t.Run("fail to connect to MCP server without token", func(t *testing.T) {
		mcpClient := mcp.NewClient(&mcp.Implementation{Name: "public-mcp-client", Version: "0.1.0"}, &mcp.ClientOptions{})
		session, err := mcpClient.Connect(t.Context(), &mcp.StreamableClientTransport{
			Endpoint: url,
		}, nil)
		t.Cleanup(func() {
			if session != nil {
				_ = session.Close()
			}
		})
		// Should fail to connect due to missing authentication.
		require.Error(t, err)
		t.Logf("got expected error when connecting without token: %v", err)
	})

	t.Run("connect to MCP server with token", func(t *testing.T) {
		// https://raw.githubusercontent.com/envoyproxy/gateway/main/examples/kubernetes/jwt/test.jwt
		validToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiYWRtaW4iOnRydWUsImlhdCI6MTUxNjIzOTAyMn0.NHVaYe26MbtOYhSKkoKYdFVomg4i8ZJd8_-RU8VNbftc4TSMb4bXP3l3YlNWACwyXPGffz5aXHc6lty1Y2t4SWRqGteragsVdZufDn5BlnJl9pdR_kdVFUsra2rWKEofkZeIC4yWytE58sMIihvo9H1ScmmVwBcQP6XETqYd0aSHp1gOa9RdUPDvoXQ5oqygTqVtxaDr6wUFKrKItgBMzWIdNZ6y7O9E0DhEPTbE9rfBo6KTFsHAZnMg4k68CDp2woYIaXbmYTWcvbzIuHO7_37GT79XdIwkm95QJ7hYC9RiwrV7mesbY4PAahERJawntho0my942XheVLmGwLMBkQ" //nolint:gosec // Test JWT token

		// Create HTTP client with Authorization header.
		authHTTPClient := &http.Client{
			Timeout: 10 * time.Second,
			Transport: &authTransport{
				token: validToken,
				base:  http.DefaultTransport,
			},
		}
		// Create an MCP client and connect to the server over Streamable HTTP.
		mcpClient := mcp.NewClient(&mcp.Implementation{Name: "public-mcp-client", Version: "0.1.0"}, &mcp.ClientOptions{})
		session, err := mcpClient.Connect(t.Context(), &mcp.StreamableClientTransport{
			Endpoint: url,
			// Use HTTP client that adds Authorization header.
			HTTPClient: authHTTPClient,
		}, nil)

		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Close() })

		// List tools to verify authenticated connection works.
		resp, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
		require.NoError(t, err)

		var actualNames []string
		for _, tool := range resp.Tools {
			actualNames = append(actualNames, tool.Name)
		}
		sort.Strings(actualNames)

		require.Equal(t, allNonGithubTools, actualNames)
	})
}
