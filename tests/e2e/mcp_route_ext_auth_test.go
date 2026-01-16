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

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testextauth"
)

// extAuthzTransport injects a header used by the Ext Authz server to make access decisions
type extAuthzTransport struct {
	accessControlValue string
	base               http.RoundTripper
}

// RoundTrip implements [http.RoundTripper.RoundTrip].
func (t *extAuthzTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set(testextauth.ExtAuthAccessControlHeader, t.accessControlValue)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func TestMCPRouteExtAuthz(t *testing.T) {
	const manifest = "testdata/mcp_route_ext_auth.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		if !e2elib.KeepCluster {
			_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
		}
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=mcp-gateway-ext-auth"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
	defer fwd.Kill()

	client := mcp.NewClient(&mcp.Implementation{Name: "demo-http-client", Version: "0.1.0"}, nil)
	allowedHTTPClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &extAuthzTransport{
			// Matches the value configured in the deployment of the ext-authz
			accessControlValue: "allowed-value",
		},
	}

	t.Run("allowed", func(t *testing.T) {
		testMCPRouteTools(
			t.Context(),
			t,
			client,
			fwd.Address(),
			"/mcp",
			testMCPServerAllToolNames("mcp-backend-ext-auth__"),
			allowedHTTPClient,
			true,
			true,
		)
	})

	t.Run("denied", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		t.Cleanup(cancel)

		deniedClient := &http.Client{
			Timeout: 10 * time.Second,
			Transport: &extAuthzTransport{
				accessControlValue: "denied",
			},
		}

		var sess *mcp.ClientSession
		require.Eventually(t, func() bool {
			var err error
			sess, err = client.Connect(
				ctx,
				&mcp.StreamableClientTransport{
					Endpoint:   fmt.Sprintf("%s/mcp", fwd.Address()),
					HTTPClient: deniedClient,
				}, nil)
			if err != nil {
				if strings.Contains(err.Error(), "Forbidden") {
					t.Logf("got expected Forbidden error: %v", err)
					return true
				}
				t.Logf("failed to connect to MCP server: %v", err)
				return false
			}
			return false
		}, 30*time.Second, 100*time.Millisecond, "expected Forbidden error when Ext Auth denies the request")
		t.Cleanup(func() {
			if sess != nil {
				_ = sess.Close()
			}
		})
	})
}
