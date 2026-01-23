// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testmcp"
)

// mcpTenantRequestHeaderInjector implements [http.RoundTripper] to inject a tenant header
// that is specified in the MCP route configuration.
type mcpTenantRequestHeaderInjector struct{}

// RoundTrip implements [http.RoundTripper.RoundTrip].
func (h mcpTenantRequestHeaderInjector) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("x-tenant-id", "tenant-a")
	return http.DefaultTransport.RoundTrip(req)
}

func TestMCP(t *testing.T) {
	const manifest = "testdata/mcp_route.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})
	manyBackendsRouteToolNames := requireCreateMCPManyBackends(t)

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=mcp-gateway"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
	defer fwd.Kill()
	// Create an MCP client and connect to the server over Streamable HTTP.
	client := mcp.NewClient(&mcp.Implementation{Name: "demo-http-client", Version: "0.1.0"}, nil)

	t.Run("default route", func(t *testing.T) {
		testMCPRouteTools(t.Context(), t, client, fwd.Address(), "/mcp", testMCPServerAllToolNames("mcp-backend__"),
			nil, true, true)
	})
	t.Run("tenant route with another path suffix", func(t *testing.T) {
		testMCPRouteTools(t.Context(), t, client, fwd.Address(), "/mcp/another", []string{
			"mcp-backend__sum",
		}, nil, false, true)
	})
	t.Run("tenant route with different path", func(t *testing.T) {
		testMCPRouteTools(t.Context(), t, client, fwd.Address(), "/mcp-top-level-different-path", []string{
			"mcp-backend__echo",
		}, nil, true, false)
	})
	t.Run("tenant route with header", func(t *testing.T) {
		testMCPRouteTools(t.Context(), t, client, fwd.Address(), "/mcp", testMCPServerAllToolNames("mcp-backend-tenant__"),
			&http.Client{Transport: mcpTenantRequestHeaderInjector{}}, true, true)
	})
	t.Run("invalid route", func(t *testing.T) {
		sess, err := client.Connect(
			t.Context(),
			&mcp.StreamableClientTransport{
				Endpoint: fmt.Sprintf("%s/mcp/invalid", fwd.Address()),
			}, nil)
		require.Error(t, err)
		require.Nil(t, sess)
	})
	t.Run("many backends route", func(t *testing.T) {
		testMCPRouteTools(t.Context(), t, client, fwd.Address(), "/mcp/many", manyBackendsRouteToolNames,
			nil, true, true)
	})
}

func testMCPRouteTools(ctx context.Context, t *testing.T, client *mcp.Client, fwdAddress, routePath string, expectedTools []string, mcpRouteTenantHeaderClient *http.Client, requireEcho, requireSum bool) {
	var sess *mcp.ClientSession
	require.Eventually(t, func() bool {
		var err error
		sess, err = client.Connect(
			ctx,
			&mcp.StreamableClientTransport{
				Endpoint:   fmt.Sprintf("%s%s", fwdAddress, routePath),
				HTTPClient: mcpRouteTenantHeaderClient,
			}, nil)
		if err != nil {
			t.Logf("failed to connect to MCP server: %v", err)
			return false
		}
		return true
	}, 30*time.Second, 100*time.Millisecond, "failed to connect to MCP server")
	t.Cleanup(func() { _ = sess.Close() })

	// List tools and verify the expected tool names are present.
	tools, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	var names []string
	var echoTool, sumTool string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if strings.Contains(tool.Name, "__"+testmcp.ToolEcho.Tool.Name) {
			echoTool = tool.Name
		}
		if strings.Contains(tool.Name, "__"+testmcp.ToolSum.Tool.Name) {
			sumTool = tool.Name
		}
	}

	require.ElementsMatch(t, expectedTools, names, "tool names do not match")

	// Call the echo tool and verify the response content.
	var res *mcp.CallToolResult
	if requireEcho {
		require.NotEmpty(t, echoTool, "echo tool not found")
		const hello = "hello MCP"
		res, err = sess.CallTool(ctx, &mcp.CallToolParams{
			Name:      echoTool,
			Arguments: testmcp.ToolEchoArgs{Text: hello},
		})
		require.NoError(t, err)
		require.False(t, res.IsError)
		require.Len(t, res.Content, 1)
		txt, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		require.Equal(t, hello, txt.Text)
	}

	// Call the sum tool and verify the result content is "42".
	if requireSum {
		require.NotEmpty(t, sumTool, "sum tool not found")
		res, err = sess.CallTool(ctx, &mcp.CallToolParams{
			Name:      sumTool,
			Arguments: testmcp.ToolSumArgs{A: 41, B: 1},
		})
		require.NoError(t, err)
		require.False(t, res.IsError)
		require.Len(t, res.Content, 1)
		txt2, ok2 := res.Content[0].(*mcp.TextContent)
		require.True(t, ok2)
		require.Equal(t, "42", txt2.Text)
	}
}

// testMCPServerAllToolNames returns all tool names with the given prefix.
func testMCPServerAllToolNames(toolPrefix string) []string {
	return []string{
		toolPrefix + testmcp.ToolEcho.Tool.Name,
		toolPrefix + testmcp.ToolSum.Tool.Name,
		toolPrefix + testmcp.ToolError.Tool.Name,
		toolPrefix + testmcp.ToolCountDown.Tool.Name,
		toolPrefix + testmcp.ToolContainsRootTool.Tool.Name,
		toolPrefix + testmcp.ToolDelay.Tool.Name,
		toolPrefix + testmcp.ToolAddPromptName,
		toolPrefix + testmcp.ToolResourceUpdateNotificationName,
		toolPrefix + testmcp.ToolAddOrDeleteDummyResourceName,
		toolPrefix + testmcp.ToolElicitEmail.Tool.Name,
		toolPrefix + testmcp.ToolCreateMessage.Tool.Name,
		toolPrefix + testmcp.ToolNotificationCountsName,
	}
}

func requireCreateMCPManyBackends(t *testing.T) []string {
	const serviceTemplate = `
apiVersion: v1
kind: Service
metadata:
  name: mcp-backend-%d
  namespace: default
spec:
  selector:
    app: mcp-backend
  ports:
    - protocol: TCP
      port: 1063
      targetPort: 1063
  type: ClusterIP
`
	const backendRefTemplate = `
    - name: mcp-backend-%d
      port: 1063
      securityPolicy:
        apiKey:
          inline: "test-api-key"
`
	var toolNames []string
	var backendRefs []string
	var services []string
	for i := range 32 {
		services = append(services, fmt.Sprintf(serviceTemplate, i))
		backendRefs = append(backendRefs, fmt.Sprintf(backendRefTemplate, i))
		toolNames = append(toolNames, testMCPServerAllToolNames(fmt.Sprintf("mcp-backend-%d__", i))...)
	}
	manifest := strings.Join(services, "---\n")
	require.NoError(t, e2elib.KubectlApplyManifestStdin(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const mcpRouteTemplate = `
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: MCPRoute
metadata:
  name: mcp-with-many-backends
  namespace: default
spec:
  path: "/mcp/many"
  parentRefs:
    - name: mcp-gateway
      kind: Gateway
      group: gateway.networking.k8s.io
      namespace: default
  backendRefs:
%s
`
	routeManifest := fmt.Sprintf(mcpRouteTemplate, strings.Join(backendRefs, "\n"))
	require.NoError(t, e2elib.KubectlApplyManifestStdin(t.Context(), routeManifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), routeManifest)
	})
	return toolNames
}
