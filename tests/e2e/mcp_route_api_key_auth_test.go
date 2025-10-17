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
)

// mcpAPIKeyTransport injects an API key header into outgoing requests.
type mcpAPIKeyTransport struct {
	apiKey string
	base   http.RoundTripper
}

// RoundTrip implements [http.RoundTripper.RoundTrip].
func (t *mcpAPIKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("X-API-KEY", t.apiKey)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func TestMCPRouteAPIKeyAuth(t *testing.T) {
	const manifest = "testdata/mcp_route_api_key_auth.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(t.Context(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=mcp-gateway-api-key"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
	defer fwd.Kill()

	client := mcp.NewClient(&mcp.Implementation{Name: "demo-http-client", Version: "0.1.0"}, nil)
	validHTTPClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &mcpAPIKeyTransport{
			apiKey: "valid-mcp-api-key",
		},
	}

	t.Run("with valid api key", func(t *testing.T) {
		testMCPRouteTools(
			t.Context(),
			t,
			client,
			fwd.Address(),
			"/mcp",
			testMCPServerAllToolNames("mcp-backend-api-key__"),
			validHTTPClient,
			true,
			true,
		)
	})

	t.Run("without api key", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		t.Cleanup(cancel)

		var sess *mcp.ClientSession
		require.Eventually(t, func() bool {
			var err error
			sess, err = client.Connect(
				ctx,
				&mcp.StreamableClientTransport{
					Endpoint: fmt.Sprintf("%s/mcp", fwd.Address()),
				}, nil)
			if err != nil {
				if strings.Contains(err.Error(), "401 Unauthorized") {
					t.Logf("got expected 401 Unauthorized error: %v", err)
					return true
				}
				t.Logf("failed to connect to MCP server: %v", err)
				return false
			}
			return false
		}, 30*time.Second, 100*time.Millisecond, "expected unauthorized error when API key is missing")
		t.Cleanup(func() {
			if sess != nil {
				_ = sess.Close()
			}
		})
	})

	t.Run("with invalid api key", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		t.Cleanup(cancel)

		invalidClient := &http.Client{
			Timeout:   10 * time.Second,
			Transport: &mcpAPIKeyTransport{apiKey: "invalid-api-key"},
		}

		var sess *mcp.ClientSession
		require.Eventually(t, func() bool {
			var err error
			sess, err = client.Connect(
				ctx,
				&mcp.StreamableClientTransport{
					Endpoint:   fmt.Sprintf("%s/mcp", fwd.Address()),
					HTTPClient: invalidClient,
				}, nil)
			if err != nil {
				if strings.Contains(err.Error(), "401 Unauthorized") {
					t.Logf("got expected 401 Unauthorized error: %v", err)
					return true
				}
				t.Logf("failed to connect to MCP server: %v", err)
				return false
			}
			return false
		}, 30*time.Second, 100*time.Millisecond, "expected unauthorized error when API key is invalid")
		t.Cleanup(func() {
			if sess != nil {
				_ = sess.Close()
			}
		})
	})
}
