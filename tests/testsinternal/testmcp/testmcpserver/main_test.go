// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testmcp"
)

// TestMainServer_CallToolEcho starts the test MCP HTTP server and verifies a simple tool call works.
func TestMainServer_CallToolEcho(t *testing.T) {
	// Find a free TCP port.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())

	// Set the port env for main() and start the server.
	t.Setenv("LISTENER_PORT", fmt.Sprint(port))
	srv := doMain()
	t.Cleanup(func() { _ = srv.Close() })

	// Create an MCP client and connect to the server over Streamable HTTP.
	client := mcp.NewClient(&mcp.Implementation{Name: "demo-http-client", Version: "0.1.0"}, nil)
	// Wait briefly for the server to come up and connect.
	var sess *mcp.ClientSession
	require.Eventually(t, func() bool {
		sess, err = client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: fmt.Sprintf("http://127.0.0.1:%d/mcp", port)}, nil)
		if err != nil {
			t.Logf("failed to connect to MCP server: %v", err)
			return false
		}
		return true
	}, 3*time.Second, 50*time.Millisecond, "failed to connect to MCP server")
	t.Cleanup(func() { _ = sess.Close() })

	// Call the echo tool and verify the response content.
	const hello = "hello MCP"
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      testmcp.ToolEcho.Tool.Name,
		Arguments: testmcp.ToolEchoArgs{Text: hello},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	txt, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, hello, txt.Text)
}
