// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

func TestMCPRouteTokenExchange(t *testing.T) {
	// Skip the build if we're running against an old version of EG
	e2elib.RequireMinEnvoyGatewayVersion(t, "1.7.2")

	const manifest = "testdata/mcp_route_token_exchange.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		if !e2elib.KeepCluster {
			_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
		}
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=mcp-gateway-token-exchange"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
	defer fwd.Kill()

	// https://raw.githubusercontent.com/envoyproxy/gateway/main/examples/kubernetes/jwt/test.jwt
	validToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiYWRtaW4iOnRydWUsImlhdCI6MTUxNjIzOTAyMn0.NHVaYe26MbtOYhSKkoKYdFVomg4i8ZJd8_-RU8VNbftc4TSMb4bXP3l3YlNWACwyXPGffz5aXHc6lty1Y2t4SWRqGteragsVdZufDn5BlnJl9pdR_kdVFUsra2rWKEofkZeIC4yWytE58sMIihvo9H1ScmmVwBcQP6XETqYd0aSHp1gOa9RdUPDvoXQ5oqygTqVtxaDr6wUFKrKItgBMzWIdNZ6y7O9E0DhEPTbE9rfBo6KTFsHAZnMg4k68CDp2woYIaXbmYTWcvbzIuHO7_37GT79XdIwkm95QJ7hYC9RiwrV7mesbY4PAahERJawntho0my942XheVLmGwLMBkQ" //nolint:gosec // Test JWT token

	// Create HTTP client with Authorization header.
	authHTTPClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &mcpAuthTransport{
			token: validToken,
			base:  http.DefaultTransport,
		},
	}

	// Create an MCP client and connect to the server over Streamable HTTP.
	client := mcp.NewClient(&mcp.Implementation{Name: "demo-http-client", Version: "0.1.0"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)

	// The MCP Server has a middleware that verifies that MCP requests receive a Bearer token that contains the
	// "act" claim with the right delegation value. If the connection succeeds, we know token exchange worked
	// and that the claims are properly propagated.

	var sess *mcp.ClientSession
	require.Eventually(t, func() bool {
		var err error
		sess, err = client.Connect(
			ctx,
			&mcp.StreamableClientTransport{
				Endpoint: fmt.Sprintf("%s/token-exchange", fwd.Address()),
				// Use HTTP client that adds Authorization header.
				HTTPClient: authHTTPClient,
			}, nil)
		if err != nil {
			t.Logf("failed to connect to MCP server: %v", err)
			return false
		}
		return true
	}, 30*time.Second, 100*time.Millisecond, "failed to connect to MCP server")
	t.Cleanup(func() { _ = sess.Close() })
}
