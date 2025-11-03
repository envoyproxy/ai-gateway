// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const runMCPTestServer = "__RUN_MCP_TEST_SERVER__"

func TestMain(m *testing.M) {
	// If the runMCPTestServer variable is set, run the mcp test server
	// instead of running tests (aka the fork and exec trick).
	if os.Getenv(runMCPTestServer) == "true" {
		_ = os.Unsetenv(runMCPTestServer)
		runTestStdioServer()
		return
	}

	os.Exit(m.Run())
}

func TestStdio2HTTP(t *testing.T) {
	// create a command to fork and exec the test binary as an MCP server.
	t.Setenv(runMCPTestServer, "true")
	cmd, err := os.Executable()
	require.NoError(t, err)

	// Run the stdio2http proxy; this will run the test binary as a subprocess in a separate goroutine.
	// Since it is bound to the test context, when the test is completed the command will be aborted.
	logger := slog.New(slog.DiscardHandler)
	addr, err := runStdio2HTTPProxy(t.Context(), logger, "test-stdio", cmd)
	require.NoError(t, err)

	// run a streamable HTTP client against the proxy.
	client := mcp.NewClient(&mcp.Implementation{Name: t.Name()}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: addr}, nil)
	require.NoError(t, err)

	// Verify that the tool call is properly proxied.
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "test stdio proxy"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	require.IsType(t, &mcp.TextContent{}, res.Content[0])
	require.Equal(t, "test stdio proxy", res.Content[0].(*mcp.TextContent).Text)
}

// runTestStdioServer runs a simple MCP stdio server that implements an "echo" tool.
// This method will be run in a subprocess via TestMain, which will be executed by the
// stdio2http proxy.
func runTestStdioServer() {
	type echoArgs struct {
		Text string `json:"text"`
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "test-stdio"}, nil)
	mcp.AddTool(server,
		&mcp.Tool{Name: "echo", Description: "echo tool"},
		func(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: args.Text},
				},
			}, nil, nil
		})

	_ = server.Run(context.Background(), &mcp.StdioTransport{})
}
