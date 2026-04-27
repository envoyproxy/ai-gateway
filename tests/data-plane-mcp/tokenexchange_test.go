// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dataplanemcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
	"github.com/envoyproxy/ai-gateway/internal/version"
	"github.com/envoyproxy/ai-gateway/tests/internal/dataplaneenv"
	"github.com/envoyproxy/ai-gateway/tests/internal/testmcp"
	"github.com/envoyproxy/ai-gateway/tests/internal/testtokenexchangelib"
)

func TestMCPTokenExchange_Delegation(t *testing.T) {
	// Create a middleware to capture the Authorization header received by the upstream MCP server
	var receivedAuthHeader string
	authCaptureMiddleware := func(handler mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := handler(ctx, method, req)
			receivedAuthHeader = req.GetExtra().Header.Get("Authorization")
			return res, err
		}
	}

	env := requireTokenExchangeEnv(t, authCaptureMiddleware)

	// The Initialize request performed when the client connects should have triggered the token exchange.
	require.Eventually(t, func() bool {
		return env.stsServer.LastRequest != nil
	}, 10*time.Second, 100*time.Millisecond, "expected token exchange request to STS")

	// Verify the token exchange parameters
	req := env.stsServer.LastRequest
	require.NotNil(t, req)
	require.Equal(t, testtokenexchangelib.GrantTypeTokenExchange, req.GrantType)
	require.NotEmpty(t, req.SubjectToken)
	require.Equal(t, "aigw-client", req.ClientID)
	require.Equal(t, "aigw-client-secret", req.ClientSecret)
	require.Equal(t, "gateway-actor-token", req.ActorToken)
	require.Equal(t, "mcp-upstream", req.Audience)
	require.Equal(t, "mcp.read", req.Scope)

	// Verify that the authorization header received by the upstream MCP server is the exchanged token
	require.NotEmpty(t, receivedAuthHeader)
	token, err := testtokenexchangelib.ParseIssuedToken(strings.TrimPrefix(receivedAuthHeader, "Bearer "))
	require.NoError(t, err)
	require.Equal(t, "testtokenexchange", token.Issuer)
	require.Equal(t, "user-access-token-123", token.Subject) // Original subject in the original request
	require.NotNil(t, token.Act)
	require.Equal(t, "gateway-actor-token", token.Act.Sub) // Actor token configured in th token exchange (delegation chain)
	require.Len(t, token.Audience, 1)
	require.Equal(t, "mcp-upstream", token.Audience[0])
}

type tokenExchangeEnv struct {
	session   *mcp.ClientSession
	stsServer *testtokenexchangelib.Server
}

func requireTokenExchangeEnv(t *testing.T, middlewares ...func(handler mcp.MethodHandler) mcp.MethodHandler) *tokenExchangeEnv {
	t.Helper()

	internaltesting.ClearTestEnv(t)

	projectRoot := internaltesting.FindProjectRoot()
	modulePath := filepath.Join(projectRoot, "out", "libaigateway.so")
	_, err := os.Stat(modulePath)
	require.NoError(t, err, "libaigateway.so not found at %s; run: make build-dynamic-module", modulePath)

	// Configure the path where Envoy will look for dynamic module files
	t.Setenv("ENVOY_DYNAMIC_MODULES_SEARCH_PATH", filepath.Join(projectRoot, "out"))
	// Needed for Go-based dynamic modules to disable the cgo pointer checks as
	// Envoy may hold pointers to Go memory.
	t.Setenv("GODEBUG", "cgocheck=0")

	stsServer, stsHTTPServer := testtokenexchangelib.NewServer(1075)
	t.Cleanup(func() { _ = stsHTTPServer.Close() })

	mcpConfig := &filterapi.MCPConfig{
		BackendListenerAddr: "http://127.0.0.1:9999",
		Routes: []filterapi.MCPRoute{
			{
				Name: "test-route",
				Backends: []filterapi.MCPBackend{
					{
						Name:             "te-backend",
						UseTokenExchange: true,
					},
				},
			},
		},
	}

	config, err := json.Marshal(filterapi.Config{MCPConfig: mcpConfig, Version: version.Parse()})
	require.NoError(t, err)

	env := dataplaneenv.StartTestEnvironment(t,
		func(_ testing.TB, _ io.Writer, ports map[string]int) {
			srv, _ := testmcp.NewServer(&testmcp.Options{
				Port:                 ports["te_backend"],
				ForceJSONResponse:    false,
				DumbEchoServer:       false,
				WriteTimeout:         1200 * time.Second,
				ReceivingMiddlewares: middlewares,
			})
			t.Cleanup(func() {
				_ = srv.Close()
			})
		},
		map[string]int{"te_backend": 8082, "special_listener": 9999},
		string(config), nil, envoyConfig, true, true,
		1200*time.Second,
	)

	client := mcp.NewClient(&mcp.Implementation{Name: "token-exchange-test-client", Version: "0.1.0"}, &mcp.ClientOptions{})
	httpClient := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &authHeaderTransport{
			authHeader: "Bearer user-access-token-123",
		},
	}

	baseURL := fmt.Sprintf("http://localhost:%d%s", env.EnvoyListenerPort(), defaultMCPPath)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   baseURL,
		HTTPClient: httpClient,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return &tokenExchangeEnv{
		session:   session,
		stsServer: stsServer,
	}
}

type authHeaderTransport struct {
	base       http.RoundTripper
	authHeader string
}

func (t *authHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", t.authHeader)

	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}
