// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

// mcpAuthTransport is an http.RoundTripper that adds Authorization header to requests
// for testing OAuth protected MCP endpoint.
type mcpAuthTransport struct {
	token string
	base  http.RoundTripper
}

// RoundTrip implements [http.RoundTripper.RoundTrip].
func (t *mcpAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

func TestMCPRouteOAuth(t *testing.T) {
	const manifest = "testdata/mcp_route_oauth.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=mcp-gateway-oauth"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
	defer fwd.Kill()

	// Use plain HTTP client to test WWW-Authenticate header.
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	t.Run("with valid token using MCP client", func(t *testing.T) {
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

		var sess *mcp.ClientSession
		require.Eventually(t, func() bool {
			var err error
			sess, err = client.Connect(
				ctx,
				&mcp.StreamableClientTransport{
					Endpoint: fmt.Sprintf("%s/mcp", fwd.Address()),
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

		// List tools to verify authenticated connection works.
		tools, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
		require.NoError(t, err)
		require.NotEmpty(t, tools.Tools, "Should have tools available with authenticated connection")

		t.Logf("Successfully connected with valid token and retrieved %d tools", len(tools.Tools))
	})

	t.Run("without token using MCP client", func(t *testing.T) {
		// Create an MCP client and connect to the server over Streamable HTTP.
		client := mcp.NewClient(&mcp.Implementation{Name: "demo-http-client", Version: "0.1.0"}, nil)
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
				if strings.Contains(err.Error(), "Unauthorized") {
					t.Logf("got expected Unauthorized error: %v", err)
					return true
				}
				t.Logf("failed to connect to MCP server: %v", err)
				return false
			}
			return false
		}, 30*time.Second, 100*time.Millisecond, "failed to connect to MCP server")
		t.Cleanup(func() {
			if sess != nil {
				_ = sess.Close()
			}
		})
	})

	t.Run("WWW-Authenticate header on unauthorized access", func(t *testing.T) {
		// Make request to MCP endpoint without authentication.
		mcpURL := fmt.Sprintf("%s/mcp", fwd.Address())

		req, err := http.NewRequestWithContext(t.Context(), "GET", mcpURL, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should get 401 Unauthorized.
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		t.Logf("Received expected 401 Unauthorized response: %v", *resp)

		// Validate WWW-Authenticate header is present.
		wwwAuthHeader := resp.Header.Get("WWW-Authenticate")
		require.NotEmpty(t, wwwAuthHeader, "WWW-Authenticate header should be present on 401 response")

		// Validate WWW-Authenticate header contains Bearer scheme.
		require.Contains(t, wwwAuthHeader, "Bearer", "WWW-Authenticate header should contain Bearer scheme")

		// Validate WWW-Authenticate header contains resource_metadata parameter.
		require.Contains(t, wwwAuthHeader, "resource_metadata", "WWW-Authenticate header should contain resource_metadata parameter")
		t.Logf("WWW-Authenticate header: %s", wwwAuthHeader)
	})

	t.Run("OAuth protected resource metadata endpoint", func(t *testing.T) {
		// Test the OAuth protected resource metadata endpoint (2025-06-18 spec).
		metadataURLWithSuffix := fmt.Sprintf("%s/.well-known/oauth-protected-resource/mcp", fwd.Address())

		req, err := http.NewRequestWithContext(t.Context(), "GET", metadataURLWithSuffix, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should get 200 OK (metadata endpoint should be publicly accessible).
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Should return JSON content.
		contentType := resp.Header.Get("Content-Type")
		require.Contains(t, contentType, "application/json", "Metadata endpoint should return JSON")

		// Parse and validate the JSON response structure.
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var metadata map[string]interface{}
		err = json.Unmarshal(body, &metadata)
		require.NoError(t, err)

		// Validate required fields are present.
		require.Contains(t, metadata, "resource", "Metadata should contain resource field")
		require.Contains(t, metadata, "authorization_servers", "Metadata should contain authorization_servers field")
		require.Contains(t, metadata, "bearer_methods_supported", "Metadata should contain bearer_methods_supported field")
		require.Contains(t, metadata, "scopes_supported", "Metadata should contain scopes_supported field")

		// Validate field values match expected configuration.
		require.Equal(t, "https://foo.bar.com/mcp", metadata["resource"], "Resource should match configured value")

		authServers, ok := metadata["authorization_servers"].([]interface{})
		require.True(t, ok, "authorization_servers should be an array")
		require.Len(t, authServers, 1, "Should have one authorization server")
		require.Equal(t, "https://auth-server.example.com", authServers[0], "Authorization server should match configured value")

		bearerMethods, ok := metadata["bearer_methods_supported"].([]interface{})
		require.True(t, ok, "bearer_methods_supported should be an array")
		require.Contains(t, bearerMethods, "header", "Should support header bearer method")

		scopes, ok := metadata["scopes_supported"].([]interface{})
		require.True(t, ok, "scopes_supported should be an array")
		expectedScopes := []string{"echo", "sum", "countdown"}
		for _, expectedScope := range expectedScopes {
			require.Contains(t, scopes, expectedScope, "Should contain expected scope: %s", expectedScope)
		}

		metadataURLWithoutSuffix := fmt.Sprintf("%s/.well-known/oauth-protected-resource", fwd.Address())
		req, err = http.NewRequestWithContext(t.Context(), "GET", metadataURLWithoutSuffix, nil)
		require.NoError(t, err)

		resp, err = httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// We can't expose these resource at the root, because there may be multiple MCP routes with different OAuth settings, so we need
		// to rely on clients properly implementing the spec and using this value returned in the header.
		// We validate that we are not exposing this route.
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("OAuth authorization server metadata endpoints", func(t *testing.T) {
		// Test the rest of endpoints
		// We can't expose these resource at the root, because there may be multiple MCP routes with different OAuth settings, so we need
		// to rely on clients properly implementing the spec and using this value returned in the header.
		// We validate that each endpoint is properly exposed
		for wellknownEndpoint, wantStatusCode := range map[string]int{
			fmt.Sprintf("%s/.well-known/oauth-authorization-server/mcp", fwd.Address()): http.StatusOK,
			fmt.Sprintf("%s/.well-known/oauth-authorization-server", fwd.Address()):     http.StatusNotFound,
			fmt.Sprintf("%s/.well-known/openid-configuration/mcp", fwd.Address()):       http.StatusOK,
			fmt.Sprintf("%s/.well-known/openid-configuration", fwd.Address()):           http.StatusNotFound,
		} {
			t.Run(wellknownEndpoint, func(t *testing.T) {
				req, err := http.NewRequestWithContext(t.Context(), "GET", wellknownEndpoint, nil)
				require.NoError(t, err)

				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				t.Cleanup(func() { _ = resp.Body.Close() })

				// Verify that the status code is the desired one
				require.Equal(t, wantStatusCode, resp.StatusCode)
				if wantStatusCode != http.StatusOK {
					return
				}

				// Should return JSON content.
				contentType := resp.Header.Get("Content-Type")
				require.Contains(t, contentType, "application/json", "Auth server metadata endpoint should return JSON")

				// Parse and validate the JSON response structure.
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)

				var authServerMetadata map[string]interface{}
				err = json.Unmarshal(body, &authServerMetadata)
				require.NoError(t, err)

				// Validate required fields are present according to RFC 8414.
				require.Contains(t, authServerMetadata, "issuer", "Metadata should contain issuer field")
				require.Contains(t, authServerMetadata, "authorization_endpoint", "Metadata should contain authorization_endpoint field")
				require.Contains(t, authServerMetadata, "token_endpoint", "Metadata should contain token_endpoint field")
				require.Contains(t, authServerMetadata, "jwks_uri", "Metadata should contain jwks_uri field")
				require.Contains(t, authServerMetadata, "scopes_supported", "Metadata should contain scopes_supported field")
				require.Contains(t, authServerMetadata, "response_types_supported", "Metadata should contain response_types_supported field")
				require.Contains(t, authServerMetadata, "grant_types_supported", "Metadata should contain grant_types_supported field")
				require.Contains(t, authServerMetadata, "code_challenge_methods_supported", "Metadata should contain code_challenge_methods_supported field")

				// Validate field values match expected configuration.
				require.Equal(t, "https://auth-server.example.com", authServerMetadata["issuer"], "Issuer should match configured authorization server")
				require.Contains(t, authServerMetadata["authorization_endpoint"], "https://auth-server.example.com", "Authorization endpoint should be correctly constructed")
				require.Contains(t, authServerMetadata["token_endpoint"], "https://auth-server.example.com", "Token endpoint should be correctly constructed")
				require.Contains(t, authServerMetadata["jwks_uri"], "https://auth-server.example.com", "JWKS URI should be correctly constructed")

				// Validate supported features for OAuth 2.1/PKCE.
				responseTypes, ok := authServerMetadata["response_types_supported"].([]interface{})
				require.True(t, ok, "response_types_supported should be an array")
				require.Contains(t, responseTypes, "code", "Should support authorization code flow")

				grantTypes, ok := authServerMetadata["grant_types_supported"].([]interface{})
				require.True(t, ok, "grant_types_supported should be an array")
				require.Contains(t, grantTypes, "authorization_code", "Should support authorization_code grant type")

				codeChallengeMethods, ok := authServerMetadata["code_challenge_methods_supported"].([]interface{})
				require.True(t, ok, "code_challenge_methods_supported should be an array")
				require.Contains(t, codeChallengeMethods, "S256", "Should support S256 PKCE method")

				scopes, ok := authServerMetadata["scopes_supported"].([]interface{})
				require.True(t, ok, "scopes_supported should be an array")
				expectedScopes := []string{"echo", "sum", "countdown"}
				for _, expectedScope := range expectedScopes {
					require.Contains(t, scopes, expectedScope, "Should contain expected scope: %s", expectedScope)
				}
			})
		}
	})

	t.Run("Invalid token should return WWW-Authenticate", func(t *testing.T) {
		// Test with invalid/malformed token.
		mcpURL := fmt.Sprintf("%s/mcp", fwd.Address())

		req, err := http.NewRequestWithContext(t.Context(), "GET", mcpURL, nil)
		require.NoError(t, err)

		// Add invalid bearer token.
		req.Header.Set("Authorization", "Bearer invalid-token")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should still get 401 Unauthorized.
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		// Validate WWW-Authenticate header is present.
		wwwAuthHeader := resp.Header.Get("WWW-Authenticate")
		require.NotEmpty(t, wwwAuthHeader, "WWW-Authenticate header should be present on 401 response")

		// Validate WWW-Authenticate header contains Bearer scheme.
		require.Contains(t, wwwAuthHeader, "Bearer", "WWW-Authenticate header should contain Bearer scheme")

		// Validate WWW-Authenticate header contains resource_metadata parameter.
		require.Contains(t, wwwAuthHeader, "resource_metadata", "WWW-Authenticate header should contain resource_metadata parameter")
		t.Logf("WWW-Authenticate header: %s", wwwAuthHeader)
	})
}
