// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwapiv1a3 "sigs.k8s.io/gateway-api/apis/v1alpha3"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func setupOAuthTestServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		metadata := map[string]interface{}{
			"issuer":                 r.URL.Scheme + "://" + r.Host,
			"authorization_endpoint": r.URL.Scheme + "://" + r.Host + "/auth",
			"token_endpoint":         r.URL.Scheme + "://" + r.Host + "/token",
			// Use HTTP to force the backend TLS Policy discovery.
			"jwks_uri": "https://" + r.Host + "/.well-known/jwks.json",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metadata)
	})

	return httptest.NewServer(mux)
}

func TestMCPRouteController_syncMCPRouteSecurityPolicy(t *testing.T) {
	server := setupOAuthTestServer()
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	tests := []struct {
		name       string
		mcpRoute   *aigv1a1.MCPRoute
		extraObjs  []client.Object
		wantSecPol bool
		wantBTP    bool
		wantFilter bool
		wantJWKS   *egv1a1.RemoteJWKS
		wantErr    bool
	}{
		{
			name: "no authentication configured",
			mcpRoute: &aigv1a1.MCPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "default"},
				Spec: aigv1a1.MCPRouteSpec{
					SecurityPolicy: &aigv1a1.MCPRouteSecurityPolicy{},
				},
			},
			wantSecPol: false,
			wantBTP:    false,
			wantFilter: false,
			wantErr:    false,
		},
		{
			name: "authentication configured",
			mcpRoute: &aigv1a1.MCPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "default"},
				Spec: aigv1a1.MCPRouteSpec{
					SecurityPolicy: &aigv1a1.MCPRouteSecurityPolicy{
						OAuth: &aigv1a1.MCPRouteOAuth{
							Issuer:    server.URL,
							Audiences: []string{"test-audience"},
							JWKS: &aigv1a1.JWKS{
								RemoteJWKS: &egv1a1.RemoteJWKS{
									URI: server.URL + "/.well-known/jwks.json",
								},
							},
							ProtectedResourceMetadata: aigv1a1.ProtectedResourceMetadata{
								Resource:                          "https://api.example.com/mcp",
								ScopesSupported:                   []string{"read", "write"},
								ResourceName:                      ptr.To("my cool mcp tools"),
								ResourceSigningAlgValuesSupported: []string{"RS256", "ES256"},
								ResourceDocumentation:             ptr.To("https://api.example.com/docs"),
								ResourcePolicyURI:                 ptr.To("https://api.example.com/policy"),
							},
						},
					},
				},
			},
			wantSecPol: true,
			wantBTP:    true,
			wantFilter: true,
			// For HTTP JWKS we don't need a cluster with TLS config.
			wantJWKS: &egv1a1.RemoteJWKS{URI: server.URL + "/.well-known/jwks.json"},
			wantErr:  false,
		},
		{
			name: "authentication configured without jwks - auto discovery",
			mcpRoute: &aigv1a1.MCPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "default"},
				Spec: aigv1a1.MCPRouteSpec{
					SecurityPolicy: &aigv1a1.MCPRouteSecurityPolicy{
						OAuth: &aigv1a1.MCPRouteOAuth{
							Issuer:    server.URL,
							Audiences: []string{"test-audience"},
							ProtectedResourceMetadata: aigv1a1.ProtectedResourceMetadata{
								Resource:        "https://api.example.com/mcp",
								ScopesSupported: []string{"read", "write"},
							},
						},
					},
				},
			},
			extraObjs: []client.Object{
				&gwapiv1a3.BackendTLSPolicy{
					ObjectMeta: metav1.ObjectMeta{Name: "non-matching-backend-tls", Namespace: "default"},
					Spec: gwapiv1a3.BackendTLSPolicySpec{
						Validation: gwapiv1a3.BackendTLSPolicyValidation{Hostname: gwapiv1.PreciseHostname("example.com")},
						TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
							{
								LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
									Group: "gateway.envoyproxy.io/v1alpha1",
									Kind:  "Backend",
									Name:  "non-matching-backend",
								},
							},
						},
					},
				},
				&gwapiv1a3.BackendTLSPolicy{
					ObjectMeta: metav1.ObjectMeta{Name: "jwks-backend-tls", Namespace: "default"},
					Spec: gwapiv1a3.BackendTLSPolicySpec{
						Validation: gwapiv1a3.BackendTLSPolicyValidation{Hostname: gwapiv1.PreciseHostname(serverURL.Hostname())},
						TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
							{
								LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
									Group: "gateway.envoyproxy.io/v1alpha1",
									Kind:  "Backend",
									Name:  "jwks-backend",
								},
							},
						},
					},
				},
			},
			wantSecPol: true, // JWKS discovery should work with test server.
			wantBTP:    true,
			wantFilter: true,
			// For HTTPS JWKS we need a cluster with TLS config.
			wantJWKS: &egv1a1.RemoteJWKS{
				URI: fmt.Sprintf("https://%s/.well-known/jwks.json", serverURL.Host),
				BackendCluster: egv1a1.BackendCluster{
					BackendRefs: []egv1a1.BackendRef{
						{
							BackendObjectReference: gwapiv1.BackendObjectReference{
								Group: ptr.To(gwapiv1.Group("gateway.envoyproxy.io/v1alpha1")),
								Kind:  ptr.To(gwapiv1.Kind("Backend")),
								Name:  "jwks-backend",
							},
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := requireNewFakeClientWithIndexesForMCP(t)
			eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
			c := NewMCPRouteController(fakeClient, nil, logr.Discard(), eventCh.Ch)

			err := fakeClient.Create(t.Context(), tt.mcpRoute)
			require.NoError(t, err)
			for _, obj := range tt.extraObjs {
				err = fakeClient.Create(t.Context(), obj)
				require.NoError(t, err)
			}

			httpRouteName := "test-http-route"
			err = c.syncMCPRouteSecurityPolicy(t.Context(), tt.mcpRoute, httpRouteName)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			securityPolicyName := internalapi.MCPGeneratedResourceCommonPrefix + tt.mcpRoute.Name
			var securityPolicy egv1a1.SecurityPolicy
			secPolErr := fakeClient.Get(t.Context(), client.ObjectKey{Name: securityPolicyName, Namespace: tt.mcpRoute.Namespace}, &securityPolicy)

			if tt.wantSecPol {
				require.NoError(t, secPolErr, "SecurityPolicy should exist")
				require.NotNil(t, securityPolicy.Spec.JWT)
				require.NotEmpty(t, securityPolicy.Spec.JWT.Providers)
				require.Equal(t, tt.wantJWKS, securityPolicy.Spec.JWT.Providers[0].RemoteJWKS)
				// The SecurityPolicy should only apply to the HTTPRoute MCP proxy rule.
				// However, since HTTPRouteRule name is experimental in Gateway API, and some vendors (e.g. GKE Gateway) do not
				// support it yet, we currently do not set the sectionName to avoid compatibility issues.
				// The jwt filter will be removed from backend routes in the extension server.
				// TODO: use sectionName to target the MCP proxy rule only when the HTTPRouteRule name is in stable channel.
				require.Nil(t, securityPolicy.Spec.TargetRefs[0].SectionName)

			} else {
				require.Error(t, secPolErr, "SecurityPolicy should not exist")
			}

			backendTrafficPolicyName := internalapi.MCPGeneratedResourceCommonPrefix + tt.mcpRoute.Name + oauthProtectedResourceMetadataSuffix
			var backendTrafficPolicy egv1a1.BackendTrafficPolicy
			btpErr := fakeClient.Get(t.Context(), client.ObjectKey{Name: backendTrafficPolicyName, Namespace: tt.mcpRoute.Namespace}, &backendTrafficPolicy)

			if tt.wantBTP {
				require.NoError(t, btpErr, "BackendTrafficPolicy should exist")
			} else {
				require.Error(t, btpErr, "BackendTrafficPolicy should not exist")
			}

			httpRouteFilterName := internalapi.MCPGeneratedResourceCommonPrefix + tt.mcpRoute.Name + oauthProtectedResourceMetadataSuffix
			var httpRouteFilter egv1a1.HTTPRouteFilter
			filterErr := fakeClient.Get(t.Context(), client.ObjectKey{Name: httpRouteFilterName, Namespace: tt.mcpRoute.Namespace}, &httpRouteFilter)

			if tt.wantFilter {
				require.NoError(t, filterErr, "HTTPRouteFilter should exist")
			} else {
				require.Error(t, filterErr, "HTTPRouteFilter should not exist")
			}
		})
	}
}

func TestMCPRouteControllerCleanupSecurityPolicyResources(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexesForMCP(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewMCPRouteController(fakeClient, nil, logr.Discard(), eventCh.Ch)

	mcpRoute := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "default"},
		Spec: aigv1a1.MCPRouteSpec{
			SecurityPolicy: &aigv1a1.MCPRouteSecurityPolicy{
				OAuth: &aigv1a1.MCPRouteOAuth{
					Issuer:    "https://auth.example.com",
					Audiences: []string{"test-audience"},
					JWKS: &aigv1a1.JWKS{
						RemoteJWKS: &egv1a1.RemoteJWKS{
							URI: "https://auth.example.com/.well-known/jwks.json",
						},
					},
					ProtectedResourceMetadata: aigv1a1.ProtectedResourceMetadata{
						Resource:        "https://api.example.com/mcp",
						ScopesSupported: []string{"read", "write"},
					},
				},
			},
		},
	}

	err := fakeClient.Create(t.Context(), mcpRoute)
	require.NoError(t, err)

	httpRouteName := "test-http-route"

	err = c.syncMCPRouteSecurityPolicy(t.Context(), mcpRoute, httpRouteName)
	require.NoError(t, err)

	securityPolicyName := internalapi.MCPGeneratedResourceCommonPrefix + mcpRoute.Name
	backendTrafficPolicyName := internalapi.MCPGeneratedResourceCommonPrefix + mcpRoute.Name + oauthProtectedResourceMetadataSuffix
	protecedResourceMetadataFilterName := internalapi.MCPGeneratedResourceCommonPrefix + mcpRoute.Name + oauthProtectedResourceMetadataSuffix
	authServerMetadataFilterName := internalapi.MCPGeneratedResourceCommonPrefix + mcpRoute.Name + oauthAuthServerMetadataSuffix

	var securityPolicy egv1a1.SecurityPolicy
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: securityPolicyName, Namespace: mcpRoute.Namespace}, &securityPolicy)
	require.NoError(t, err, "SecurityPolicy should exist before cleanup")

	var backendTrafficPolicy egv1a1.BackendTrafficPolicy
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: backendTrafficPolicyName, Namespace: mcpRoute.Namespace}, &backendTrafficPolicy)
	require.NoError(t, err, "BackendTrafficPolicy should exist before cleanup")

	var protecedResourceMetadataFilter egv1a1.HTTPRouteFilter
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: protecedResourceMetadataFilterName, Namespace: mcpRoute.Namespace}, &protecedResourceMetadataFilter)
	require.NoError(t, err, "Protected Resource Metadata HTTPRouteFilter should exist before cleanup")

	var authServerMetadataFilter egv1a1.HTTPRouteFilter
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: authServerMetadataFilterName, Namespace: mcpRoute.Namespace}, &authServerMetadataFilter)
	require.NoError(t, err, "Authorization Server Metadata HTTPRouteFilter should exist before cleanup")

	mcpRouteWithoutSecurityPolicy := mcpRoute.DeepCopy()
	mcpRouteWithoutSecurityPolicy.Spec.SecurityPolicy = nil

	err = c.syncMCPRouteSecurityPolicy(t.Context(), mcpRouteWithoutSecurityPolicy, httpRouteName)
	require.NoError(t, err)

	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: securityPolicyName, Namespace: mcpRoute.Namespace}, &securityPolicy)
	require.Error(t, err, "SecurityPolicy should be deleted after cleanup")
	require.True(t, apierrors.IsNotFound(err), "SecurityPolicy should not be found after cleanup")

	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: backendTrafficPolicyName, Namespace: mcpRoute.Namespace}, &backendTrafficPolicy)
	require.Error(t, err, "BackendTrafficPolicy should be deleted after cleanup")
	require.True(t, apierrors.IsNotFound(err), "BackendTrafficPolicy should not be found after cleanup")

	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: protecedResourceMetadataFilterName, Namespace: mcpRoute.Namespace}, &protecedResourceMetadataFilter)
	require.Error(t, err, "Protected Resource Metadata HTTPRouteFilter should be deleted after cleanup")
	require.True(t, apierrors.IsNotFound(err), "Protected Resource Metadata HTTPRouteFilter should not be found after cleanup")

	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: authServerMetadataFilterName, Namespace: mcpRoute.Namespace}, &authServerMetadataFilter)
	require.Error(t, err, "Authorization Server Metadata HTTPRouteFilter should be deleted after cleanup")
	require.True(t, apierrors.IsNotFound(err), "Authorization Server Metadata HTTPRouteFilter should not be found after cleanup")
}

func Test_buildOAuthProtectedResourceMetadataJSON(t *testing.T) {
	auth := &aigv1a1.MCPRouteOAuth{
		Issuer: "https://auth.example.com",
		ProtectedResourceMetadata: aigv1a1.ProtectedResourceMetadata{
			Resource:        "https://api.example.com/mcp",
			ScopesSupported: []string{"read", "write", "admin"},
		},
	}

	result := buildOAuthProtectedResourceMetadataJSON(auth)

	var jsonResponse map[string]interface{}
	err := json.Unmarshal([]byte(result), &jsonResponse)
	require.NoError(t, err)

	require.Equal(t, "https://api.example.com/mcp", jsonResponse["resource"])
	require.Equal(t, []interface{}{"https://auth.example.com"}, jsonResponse["authorization_servers"])
	require.Equal(t, []interface{}{"header"}, jsonResponse["bearer_methods_supported"])
	require.Equal(t, []interface{}{"read", "write", "admin"}, jsonResponse["scopes_supported"])
}

func Test_buildWWWAuthenticateHeaderValue(t *testing.T) {
	tests := []struct {
		name     string
		metadata *aigv1a1.ProtectedResourceMetadata
		expected string
	}{
		{
			name: "https URL with path",
			metadata: &aigv1a1.ProtectedResourceMetadata{
				Resource: "https://api.example.com/mcp/v1",
			},
			expected: `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource"`,
		},
		{
			name: "https URL without path",
			metadata: &aigv1a1.ProtectedResourceMetadata{
				Resource: "https://api.example.com",
			},
			expected: `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource"`,
		},
		{
			name: "https URL with trailing slash",
			metadata: &aigv1a1.ProtectedResourceMetadata{
				Resource: "https://api.example.com/mcp/",
			},
			expected: `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource"`,
		},
		{
			name: "http URL with path",
			metadata: &aigv1a1.ProtectedResourceMetadata{
				Resource: "http://api.example.com/mcp/v1",
			},
			expected: `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="http://api.example.com/.well-known/oauth-protected-resource"`,
		},
		{
			name: "http URL without path",
			metadata: &aigv1a1.ProtectedResourceMetadata{
				Resource: "http://api.example.com",
			},
			expected: `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="http://api.example.com/.well-known/oauth-protected-resource"`,
		},
		{
			name: "http URL with trailing slash",
			metadata: &aigv1a1.ProtectedResourceMetadata{
				Resource: "http://api.example.com/mcp/",
			},
			expected: `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="http://api.example.com/.well-known/oauth-protected-resource"`,
		},
		{
			name: "URL with port number https",
			metadata: &aigv1a1.ProtectedResourceMetadata{
				Resource: "https://api.example.com:8080/mcp",
			},
			expected: `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="https://api.example.com:8080/.well-known/oauth-protected-resource"`,
		},
		{
			name: "URL with port number http",
			metadata: &aigv1a1.ProtectedResourceMetadata{
				Resource: "http://api.example.com:8080/mcp",
			},
			expected: `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="http://api.example.com:8080/.well-known/oauth-protected-resource"`,
		},
		{
			name: "complex path with multiple segments",
			metadata: &aigv1a1.ProtectedResourceMetadata{
				Resource: "https://api.example.com/v1/mcp/endpoint",
			},
			expected: `Bearer error="invalid_request", error_description="No access token was provided in this request", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildWWWAuthenticateHeaderValue(tt.metadata)
			require.Equal(t, tt.expected, result)
		})
	}
}
