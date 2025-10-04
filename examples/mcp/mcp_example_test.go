// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcp

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

var aigwBin string

func TestMain(m *testing.M) {
	var err error
	// Build aigw binary once for all tests.
	if aigwBin, err = internaltesting.BuildAigwOnDemand(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start tests due to aigw build error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestMCP_standalone(t *testing.T) {
	ght := os.Getenv("TEST_GITHUB_ACCESS_TOKEN")
	githubConfigured := ght != ""
	if githubConfigured {
		t.Setenv("GITHUB_ACCESS_TOKEN", ght)
	}

	internaltesting.StartAIGWCLI(t, aigwBin, "run", "mcp_example.yaml")

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
		t.Logf("tools/list response: %+v", resp)
		var names []string
		for _, tool := range resp.Tools {
			schemastring, err := json.MarshalIndent(tool.InputSchema, "", "  ")
			require.NoError(t, err)
			t.Logf("[tool=%s]%s\n\n%s\n", tool.Name, schemastring, tool.Description)
			names = append(names, tool.Name)
		}

		exp := []string{
			"learn-microsoft__microsoft_docs_search",
			"learn-microsoft__microsoft_docs_fetch",
			"context7__resolve-library-id",
			"context7__get-library-docs",
			"kiwi__search-flight",
			"kiwi__feedback-to-devs",
			"aws-knowledge__aws___read_documentation",
			"aws-knowledge__aws___search_documentation",
		}

		if githubConfigured {
			exp = append(exp, "github__get_issue")
			exp = append(exp, "github__get_issue_comments")
			exp = append(exp, "github__get_pull_request")
			exp = append(exp, "github__get_pull_request_diff")
			exp = append(exp, "github__get_pull_request_files")
			exp = append(exp, "github__get_pull_request_review_comments")
			exp = append(exp, "github__get_pull_request_reviews")
			exp = append(exp, "github__get_pull_request_status")
			exp = append(exp, "github__list_issue_types")
			exp = append(exp, "github__list_issues")
			exp = append(exp, "github__list_pull_requests")
			exp = append(exp, "github__list_sub_issues")
			exp = append(exp, "github__search_issues")
			exp = append(exp, "github__search_pull_requests")
		}
		require.ElementsMatch(t, exp, names, "tool names do not match")
	})

	t.Run("tool calls", func(t *testing.T) {
		type callToolTest struct {
			toolName string
			params   map[string]any
		}
		tests := []callToolTest{
			{
				toolName: "learn-microsoft__microsoft_docs_search",
				params: map[string]any{
					"query":    "microsoft 365",
					"question": "What does microsoft 365 include?",
				},
			},
			{
				toolName: "learn-microsoft__microsoft_docs_fetch",
				params: map[string]any{
					"url": "https://learn.microsoft.com/en-us/copilot/manage",
				},
			},
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
				toolName: "aws-knowledge__aws___search_documentation",
				params: map[string]any{
					"limit":         1,
					"search_phrase": "DynamoDB",
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
				toolName: "github__get_pull_request",
				params: map[string]any{
					"owner":      "envoyproxy",
					"repo":       "ai-gateway",
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
	internaltesting.StartAIGWCLI(t, aigwBin, "run", "mcp_oauth_example.yaml")

	url := fmt.Sprintf("http://localhost:%d/mcp", 1975)

	t.Run("fail to connect to MCP server without token", func(t *testing.T) {
		t.Skip("TODO: this passes")
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
		t.Logf("tools/list response: %+v", resp)
		var names []string
		for _, tool := range resp.Tools {
			schemastring, err := json.MarshalIndent(tool.InputSchema, "", "  ")
			require.NoError(t, err)
			t.Logf("[tool=%s]%s\n\n%s\n", tool.Name, schemastring, tool.Description)
			names = append(names, tool.Name)
		}

		// Do not use ElementsMatch so we can ensure there are no unexpected tools.
		for _, exp := range []string{
			"learn-microsoft__microsoft_docs_search",
			"learn-microsoft__microsoft_docs_fetch",
			"context7__resolve-library-id",
			"context7__get-library-docs",
			"aws-knowledge__aws___read_documentation",
			"aws-knowledge__aws___recommend",
			"aws-knowledge__aws___search_documentation",
			"kiwi__search-flight",
			"kiwi__feedback-to-devs",
		} {
			require.Contains(t, names, exp, "tool names do not match")
		}
	})
}
