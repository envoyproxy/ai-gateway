// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	dynmodulesv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/dynamic_modules/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/go-logr/logr/testr"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

// TestBuildSTSClusterName verifies that cluster names are derived from a SHA-256 hash of the URL,
// are stable (same input → same output), and differ across different endpoints.
func TestBuildSTSClusterName(t *testing.T) {
	name1 := buildSTSClusterName("https://keycloak.example.com/realms/myrealm/protocol/openid-connect/token")
	name2 := buildSTSClusterName("https://keycloak.example.com/realms/myrealm/protocol/openid-connect/token")
	name3 := buildSTSClusterName("https://other-sts.example.com/token")

	require.Equal(t, name1, name2, "same input must produce same cluster name")
	require.NotEqual(t, name1, name3, "different URLs must produce different cluster names")
	require.Equal(t, "mcp-sts-", name1[:len(stsClusterPrefix)], "cluster name must start with the expected prefix")
}

// TestBuildSTSCluster_HTTP verifies that a plain-HTTP STS endpoint produces a cluster without
// a TLS transport socket and uses port 80 as default.
func TestBuildSTSCluster_HTTP(t *testing.T) {
	clusterName := "mcp-sts-test"
	cluster, err := buildSTSCluster(clusterName, "http://sts.example.com/token")
	require.NoError(t, err)
	require.Equal(t, clusterName, cluster.Name)
	require.Nil(t, cluster.TransportSocket, "HTTP cluster must not have a TLS transport socket")

	// Verify the socket address uses port 80.
	ep := cluster.GetLoadAssignment().GetEndpoints()[0].GetLbEndpoints()[0].GetEndpoint()
	sa := ep.GetAddress().GetSocketAddress()
	require.Equal(t, "sts.example.com", sa.GetAddress())
	require.Equal(t, uint32(80), sa.GetPortValue())
}

// TestBuildSTSCluster_HTTPSDefaultPort verifies that an HTTPS STS endpoint with no explicit port
// uses port 443 and includes a TLS transport socket with the correct SNI.
func TestBuildSTSCluster_HTTPSDefaultPort(t *testing.T) {
	clusterName := "mcp-sts-tls"
	cluster, err := buildSTSCluster(clusterName, "https://sts.example.com/token")
	require.NoError(t, err)
	require.NotNil(t, cluster.TransportSocket, "HTTPS cluster must have a TLS transport socket")
	require.Equal(t, "envoy.transport_sockets.tls", cluster.TransportSocket.Name)

	ep := cluster.GetLoadAssignment().GetEndpoints()[0].GetLbEndpoints()[0].GetEndpoint()
	sa := ep.GetAddress().GetSocketAddress()
	require.Equal(t, "sts.example.com", sa.GetAddress())
	require.Equal(t, uint32(443), sa.GetPortValue())
}

// TestBuildSTSCluster_HTTPSCustomPort verifies that an explicit port in the URL is parsed correctly.
func TestBuildSTSCluster_HTTPSCustomPort(t *testing.T) {
	cluster, err := buildSTSCluster("mcp-sts-custom", "https://sts.example.com:8443/token")
	require.NoError(t, err)

	ep := cluster.GetLoadAssignment().GetEndpoints()[0].GetLbEndpoints()[0].GetEndpoint()
	sa := ep.GetAddress().GetSocketAddress()
	require.Equal(t, uint32(8443), sa.GetPortValue())
}

// TestBuildSTSCluster_InvalidURL verifies that a malformed URL returns an error.
func TestBuildSTSCluster_InvalidURL(t *testing.T) {
	_, err := buildSTSCluster("mcp-sts-bad", "://bad-url")
	require.Error(t, err)
}

// TestTokenExchangeHTTPFilter verifies that the token-exchange HTTP filter is correctly configured:
// correct filter name, module name, Disabled=true, IsOptional=true.
func TestTokenExchangeHTTPFilter(t *testing.T) {
	filter, err := tokenExchangeHTTPFilter()
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Equal(t, tokenExchangeFilterName, filter.Name)
	require.True(t, filter.Disabled, "filter must be disabled globally (enabled per-route only)")
	require.True(t, filter.IsOptional, "filter must be optional so Envoy can start without the ai gateway dynamic module")
	require.NotNil(t, filter.GetTypedConfig(), "filter must have a typed config")
}

// TestExtractMCPHeaderMatchValue verifies extraction of exact-match header values from a route's
// match headers, supporting both the legacy ExactMatch variant and the modern StringMatch.Exact variant.
func TestExtractMCPHeaderMatchValue(t *testing.T) {
	tests := []struct {
		name       string
		route      *routev3.Route
		headerName string
		want       string
	}{
		{
			name: "legacy ExactMatch",
			route: &routev3.Route{
				Match: &routev3.RouteMatch{
					Headers: []*routev3.HeaderMatcher{
						{
							Name: internalapi.MCPBackendHeader,
							HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{
								ExactMatch: "my-backend",
							},
						},
					},
				},
			},
			headerName: internalapi.MCPBackendHeader,
			want:       "my-backend",
		},
		{
			name: "modern StringMatch.Exact",
			route: &routev3.Route{
				Match: &routev3.RouteMatch{
					Headers: []*routev3.HeaderMatcher{
						{
							Name: internalapi.MCPBackendHeader,
							HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
								StringMatch: &matcherv3.StringMatcher{
									MatchPattern: &matcherv3.StringMatcher_Exact{
										Exact: "my-backend-sm",
									},
								},
							},
						},
					},
				},
			},
			headerName: internalapi.MCPBackendHeader,
			want:       "my-backend-sm",
		},
		{
			name: "header not present returns empty string",
			route: &routev3.Route{
				Match: &routev3.RouteMatch{
					Headers: []*routev3.HeaderMatcher{
						{
							Name: "other-header",
							HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{
								ExactMatch: "some-value",
							},
						},
					},
				},
			},
			headerName: internalapi.MCPBackendHeader,
			want:       "",
		},
		{
			name:       "nil route match returns empty string",
			route:      &routev3.Route{},
			headerName: internalapi.MCPBackendHeader,
			want:       "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractMCPHeaderMatchValue(tc.route, tc.headerName)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestMaybeCreateSTSClusters verifies that STS clusters are created for each unique
// STS endpoint across all MCPRoutes, and that duplicates are deduplicated.
func TestMaybeCreateSTSClusters(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()

	stsEndpoint1 := "https://keycloak.example.com/token"
	stsEndpoint2 := "https://other-sts.example.com/token"

	// Two routes: route1 uses stsEndpoint1 twice (should be deduped), route2 uses stsEndpoint2.
	mcpRoute1 := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "default"},
		Spec: aigv1a1.MCPRouteSpec{
			BackendRefs: []aigv1a1.MCPRouteBackendRef{
				{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "backend1"},
					SecurityPolicy: &aigv1a1.MCPBackendSecurityPolicy{
						TokenExchange: &aigv1a1.MCPBackendTokenExchange{
							STSEndpoint: stsEndpoint1,
						},
					},
				},
				{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "backend2"},
					SecurityPolicy: &aigv1a1.MCPBackendSecurityPolicy{
						TokenExchange: &aigv1a1.MCPBackendTokenExchange{
							STSEndpoint: stsEndpoint1, // duplicate endpoint
						},
					},
				},
			},
		},
	}
	mcpRoute2 := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route2", Namespace: "default"},
		Spec: aigv1a1.MCPRouteSpec{
			BackendRefs: []aigv1a1.MCPRouteBackendRef{
				{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "backend3"},
					SecurityPolicy: &aigv1a1.MCPBackendSecurityPolicy{
						TokenExchange: &aigv1a1.MCPBackendTokenExchange{
							STSEndpoint: stsEndpoint2,
						},
					},
				},
			},
		},
	}

	require.NoError(t, fakeClient.Create(t.Context(), mcpRoute1))
	require.NoError(t, fakeClient.Create(t.Context(), mcpRoute2))

	s := &Server{log: testr.New(t), k8sClient: fakeClient}
	req := &egextension.PostTranslateModifyRequest{}
	err := s.maybeCreateSTSClusters(t.Context(), req)
	require.NoError(t, err)

	// Expect exactly 2 unique clusters (stsEndpoint1 deduplicated, stsEndpoint2 unique).
	require.Len(t, req.Clusters, 2)

	clusterNames := map[string]bool{}
	for _, c := range req.Clusters {
		clusterNames[c.Name] = true
	}
	require.True(t, clusterNames[buildSTSClusterName(stsEndpoint1)], "cluster for stsEndpoint1 must be present")
	require.True(t, clusterNames[buildSTSClusterName(stsEndpoint2)], "cluster for stsEndpoint2 must be present")
}

// TestMaybeSetTokenExchangePerRouteConfig verifies that the per-route token-exchange
// config is populated when a route's headers identify a backend with a TokenExchange policy.
func TestMaybeSetTokenExchangePerRouteConfig(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()

	const namespace = "mynamespace"
	const routeName = "my-mcp-route"
	const backendName = "my-backend"
	const stsEndpoint = "https://sts.example.com/token"
	const clientID = "test-client-id"

	// Create the client-secret Secret.
	clientSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-client-secret", Namespace: namespace},
		Data:       map[string][]byte{"clientSecret": []byte("s3cret")},
	}
	require.NoError(t, fakeClient.Create(t.Context(), clientSecret))

	// Create the MCPRoute with a TokenExchange policy.
	mcpRoute := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: namespace},
		Spec: aigv1a1.MCPRouteSpec{
			BackendRefs: []aigv1a1.MCPRouteBackendRef{
				{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: gwapiv1.ObjectName(backendName)},
					SecurityPolicy: &aigv1a1.MCPBackendSecurityPolicy{
						TokenExchange: &aigv1a1.MCPBackendTokenExchange{
							STSEndpoint: stsEndpoint,
							ClientAuth: &aigv1a1.MCPTokenExchangeClientAuth{
								ClientID: clientID,
								ClientSecretRef: gwapiv1.SecretObjectReference{
									Name:      "my-client-secret",
									Namespace: ptr.To(gwapiv1.Namespace(namespace)),
								},
							},
							Audience: ptr.To("https://my-backend.example.com"),
							Scopes:   []string{"openid", "profile"},
						},
					},
				},
			},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), mcpRoute))

	s := &Server{log: testr.New(t), k8sClient: fakeClient}
	route := &routev3.Route{
		Match: &routev3.RouteMatch{
			Headers: []*routev3.HeaderMatcher{
				{
					Name: internalapi.MCPBackendHeader,
					HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{
						ExactMatch: backendName,
					},
				},
				{
					Name: internalapi.MCPRouteHeader,
					HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{
						ExactMatch: namespace + "/" + routeName,
					},
				},
			},
		},
	}

	err := s.maybeSetTokenExchangePerRouteConfig(t.Context(), route)
	require.NoError(t, err)
	require.NotNil(t, route.TypedPerFilterConfig, "per-route config must be set for a token-exchange backend")
	require.Contains(t, route.TypedPerFilterConfig, tokenExchangeFilterName, "per-route config must be keyed by the filter name")

	// Unmarshal and verify the per-route JSON config contains the expected fields.
	// The config is wrapped in a DynamicModuleFilterPerRoute; we verify by checking the raw JSON
	// indirectly through the cluster name and STS endpoint values.
	require.Equal(t, buildSTSClusterName(stsEndpoint), getPerRouteConfigClusterName(t, route))
}

// TestMaybeSetTokenExchangePerRouteConfig_NoMatchingBackend verifies that a route whose backend
// name does not match any MCPRouteBackendRef with a TokenExchange policy is left unchanged.
func TestMaybeSetTokenExchangePerRouteConfig_NoMatchingBackend(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()

	const namespace = "default"
	mcpRoute := &aigv1a1.MCPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: namespace},
		Spec: aigv1a1.MCPRouteSpec{
			BackendRefs: []aigv1a1.MCPRouteBackendRef{
				{
					BackendObjectReference: gwapiv1.BackendObjectReference{Name: "other-backend"},
					SecurityPolicy: &aigv1a1.MCPBackendSecurityPolicy{
						TokenExchange: &aigv1a1.MCPBackendTokenExchange{
							STSEndpoint: "https://sts.example.com/token",
						},
					},
				},
			},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), mcpRoute))

	s := &Server{log: testr.New(t), k8sClient: fakeClient}
	route := &routev3.Route{
		Match: &routev3.RouteMatch{
			Headers: []*routev3.HeaderMatcher{
				{
					Name: internalapi.MCPBackendHeader,
					HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{
						ExactMatch: "non-existent-backend",
					},
				},
				{
					Name: internalapi.MCPRouteHeader,
					HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{
						ExactMatch: namespace + "/route1",
					},
				},
			},
		},
	}

	err := s.maybeSetTokenExchangePerRouteConfig(t.Context(), route)
	require.NoError(t, err)
	require.Nil(t, route.TypedPerFilterConfig, "no per-route config should be set when backend has no TokenExchange policy")
}

// TestMaybeSetTokenExchangePerRouteConfig_MissingMCPHeaders verifies that a route without the
// MCPBackendHeader / MCPRouteHeader match headers is left unchanged.
func TestMaybeSetTokenExchangePerRouteConfig_MissingMCPHeaders(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
	s := &Server{log: testr.New(t), k8sClient: fakeClient}
	route := &routev3.Route{
		Match: &routev3.RouteMatch{
			Headers: []*routev3.HeaderMatcher{
				{
					Name: "some-other-header",
					HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{
						ExactMatch: "value",
					},
				},
			},
		},
	}
	err := s.maybeSetTokenExchangePerRouteConfig(t.Context(), route)
	require.NoError(t, err)
	require.Nil(t, route.TypedPerFilterConfig)
}

// getPerRouteConfigClusterName extracts the cluster field from the JSON config embedded in a
// DynamicModuleFilterPerRoute TypedPerFilterConfig on the given route.
// This is used to assert that maybeSetTokenExchangePerRouteConfig set the correct cluster name.
func getPerRouteConfigClusterName(t *testing.T, route *routev3.Route) string {
	t.Helper()
	// The per-route config is wrapped in a DynamicModuleFilterPerRoute Any.
	// The FilterConfig inside is a StringValue Any containing the JSON string.
	// Rather than fully unwrapping, we parse the raw JSON from the FilterConfig value.

	dynModPerRouteAny := route.TypedPerFilterConfig[tokenExchangeFilterName]
	require.NotNil(t, dynModPerRouteAny)

	dynModPerRoute := &dynmodulesv3.DynamicModuleFilterPerRoute{}
	require.NoError(t, dynModPerRouteAny.UnmarshalTo(dynModPerRoute))

	svAny := dynModPerRoute.FilterConfig
	require.NotNil(t, svAny)

	sv := &wrapperspb.StringValue{}
	require.NoError(t, svAny.UnmarshalTo(sv))

	var cfg tokenExchangePerRouteConfig
	require.NoError(t, json.Unmarshal([]byte(sv.Value), &cfg))
	return cfg.Cluster
}

func TestLoadTokenExchangeActorToken_NoActorToken(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
	s := &Server{log: testr.New(t), k8sClient: fakeClient}

	t.Run("nil token exchange config", func(t *testing.T) {
		token, tokenType, err := s.loadTokenExchangeActorToken(t.Context(), "default", nil)
		require.NoError(t, err)
		require.Empty(t, token)
		require.Empty(t, tokenType)
	})

	t.Run("nil actor token config", func(t *testing.T) {
		te := &aigv1a1.MCPBackendTokenExchange{}
		token, tokenType, err := s.loadTokenExchangeActorToken(t.Context(), "default", te)
		require.NoError(t, err)
		require.Empty(t, token)
		require.Empty(t, tokenType)
	})
}

func TestLoadTokenExchangeActorToken_SecretRef(t *testing.T) {
	const namespace = "default"
	const actorSecretName = "actor-token-secret" // #nosec G101
	const actorToken = "static-actor-token"

	fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: actorSecretName, Namespace: namespace},
		Data:       map[string][]byte{"token": []byte(actorToken)},
	}))

	s := &Server{log: testr.New(t), k8sClient: fakeClient}
	te := &aigv1a1.MCPBackendTokenExchange{
		ActorToken: &aigv1a1.MCPBackendTokenExchangeActorToken{
			SecretRef: &gwapiv1.SecretObjectReference{Name: gwapiv1.ObjectName(actorSecretName)},
		},
	}

	token, tokenType, err := s.loadTokenExchangeActorToken(t.Context(), namespace, te)
	require.NoError(t, err)
	require.Equal(t, actorToken, token)
	require.Equal(t, defaultActorTokenType, tokenType)
}

func TestLoadTokenExchangeActorToken_ClientAssertionJWT_HS256(t *testing.T) {
	const namespace = "default"
	const keySecretName = "jwt-private-key" // #nosec G101
	const keyValue = "super-secret-signing-key"

	fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: keySecretName, Namespace: namespace},
		Data:       map[string][]byte{"privateKey": []byte(keyValue)},
	}))

	alg := "HS256"
	lifetime := int32(120)
	te := &aigv1a1.MCPBackendTokenExchange{
		ActorToken: &aigv1a1.MCPBackendTokenExchangeActorToken{
			ClientAssertionJWT: &aigv1a1.MCPTokenExchangeJWTActorConfig{
				Issuer:   "gateway-client",
				Subject:  "gateway-service-account",
				Lifetime: &lifetime,
				PrivateKeyRef: gwapiv1.SecretObjectReference{
					Name: gwapiv1.ObjectName(keySecretName),
				},
				SigningAlgorithm: &alg,
			},
		},
	}

	s := &Server{log: testr.New(t), k8sClient: fakeClient}
	signed, tokenType, err := s.loadTokenExchangeActorToken(t.Context(), namespace, te)
	require.NoError(t, err)
	require.NotEmpty(t, signed)
	require.Equal(t, defaultActorTokenType, tokenType)

	claims := &jwt.RegisteredClaims{}
	parsedToken, err := jwt.ParseWithClaims(signed, claims, func(token *jwt.Token) (any, error) {
		require.Equal(t, jwt.SigningMethodHS256, token.Method)
		return []byte(keyValue), nil
	})
	require.NoError(t, err)
	require.True(t, parsedToken.Valid)
	require.Equal(t, "gateway-client", claims.Issuer)
	require.Equal(t, "gateway-service-account", claims.Subject)
	require.NotNil(t, claims.IssuedAt)
	require.NotNil(t, claims.ExpiresAt)
	actualLifetime := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
	require.GreaterOrEqual(t, actualLifetime, 115*time.Second)
	require.LessOrEqual(t, actualLifetime, 125*time.Second)
}

func TestLoadTokenExchangeActorToken_ClientAssertionJWT_RS256(t *testing.T) {
	const namespace = "default"
	const keySecretName = "jwt-rsa-private-key" // #nosec G101

	rsaPrivateKey, rsaPrivateKeyPEM := mustGenerateRSAPrivateKeyPEM(t)

	fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: keySecretName, Namespace: namespace},
		Data:       map[string][]byte{"privateKey": rsaPrivateKeyPEM},
	}))

	alg := "RS256"
	te := &aigv1a1.MCPBackendTokenExchange{
		ActorToken: &aigv1a1.MCPBackendTokenExchangeActorToken{
			ClientAssertionJWT: &aigv1a1.MCPTokenExchangeJWTActorConfig{
				Issuer:  "gateway-client-rsa",
				Subject: "gateway-sa-rsa",
				PrivateKeyRef: gwapiv1.SecretObjectReference{
					Name: gwapiv1.ObjectName(keySecretName),
				},
				SigningAlgorithm: &alg,
			},
		},
	}

	s := &Server{log: testr.New(t), k8sClient: fakeClient}
	signed, tokenType, err := s.loadTokenExchangeActorToken(t.Context(), namespace, te)
	require.NoError(t, err)
	require.NotEmpty(t, signed)
	require.Equal(t, defaultActorTokenType, tokenType)

	claims := &jwt.RegisteredClaims{}
	parsedToken, err := jwt.ParseWithClaims(signed, claims, func(token *jwt.Token) (any, error) {
		require.Equal(t, jwt.SigningMethodRS256, token.Method)
		return &rsaPrivateKey.PublicKey, nil
	})
	require.NoError(t, err)
	require.True(t, parsedToken.Valid)
	require.Equal(t, "gateway-client-rsa", claims.Issuer)
	require.Equal(t, "gateway-sa-rsa", claims.Subject)
}

func TestLoadTokenExchangeActorToken_ClientAssertionJWT_ES256(t *testing.T) {
	const namespace = "default"
	const keySecretName = "jwt-ecdsa-private-key" // #nosec G101

	ecdsaPrivateKey, ecdsaPrivateKeyPEM := mustGenerateECDSAPrivateKeyPEM(t)

	fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
	require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: keySecretName, Namespace: namespace},
		Data:       map[string][]byte{"privateKey": ecdsaPrivateKeyPEM},
	}))

	alg := "ES256"
	te := &aigv1a1.MCPBackendTokenExchange{
		ActorToken: &aigv1a1.MCPBackendTokenExchangeActorToken{
			ClientAssertionJWT: &aigv1a1.MCPTokenExchangeJWTActorConfig{
				Issuer:  "gateway-client-ecdsa",
				Subject: "gateway-sa-ecdsa",
				PrivateKeyRef: gwapiv1.SecretObjectReference{
					Name: gwapiv1.ObjectName(keySecretName),
				},
				SigningAlgorithm: &alg,
			},
		},
	}

	s := &Server{log: testr.New(t), k8sClient: fakeClient}
	signed, tokenType, err := s.loadTokenExchangeActorToken(t.Context(), namespace, te)
	require.NoError(t, err)
	require.NotEmpty(t, signed)
	require.Equal(t, defaultActorTokenType, tokenType)

	claims := &jwt.RegisteredClaims{}
	parsedToken, err := jwt.ParseWithClaims(signed, claims, func(token *jwt.Token) (any, error) {
		require.Equal(t, jwt.SigningMethodES256, token.Method)
		return &ecdsaPrivateKey.PublicKey, nil
	})
	require.NoError(t, err)
	require.True(t, parsedToken.Valid)
	require.Equal(t, "gateway-client-ecdsa", claims.Issuer)
	require.Equal(t, "gateway-sa-ecdsa", claims.Subject)
}

func TestLoadTokenExchangeActorToken_Errors(t *testing.T) {
	t.Run("secret ref not found", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
		s := &Server{log: testr.New(t), k8sClient: fakeClient}
		te := &aigv1a1.MCPBackendTokenExchange{
			ActorToken: &aigv1a1.MCPBackendTokenExchangeActorToken{
				SecretRef: &gwapiv1.SecretObjectReference{Name: "missing-actor-token"},
			},
		}

		_, _, err := s.loadTokenExchangeActorToken(t.Context(), "default", te)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to load actor token secret")
	})

	t.Run("invalid rsa private key", func(t *testing.T) {
		const namespace = "default"
		fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
		require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "jwt-private-key", Namespace: namespace},
			Data:       map[string][]byte{"privateKey": []byte("not-a-valid-rsa-pem")},
		}))

		s := &Server{log: testr.New(t), k8sClient: fakeClient}
		te := &aigv1a1.MCPBackendTokenExchange{
			ActorToken: &aigv1a1.MCPBackendTokenExchangeActorToken{
				ClientAssertionJWT: &aigv1a1.MCPTokenExchangeJWTActorConfig{
					Issuer:  "issuer",
					Subject: "subject",
					PrivateKeyRef: gwapiv1.SecretObjectReference{
						Name: "jwt-private-key",
					},
				},
			},
		}

		_, _, err := s.loadTokenExchangeActorToken(t.Context(), namespace, te)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to parse client assertion JWT private key")
	})

	t.Run("unsupported signing algorithm", func(t *testing.T) {
		const namespace = "default"
		const customAlg = "UNITTEST_UNSUPPORTED_ALG"

		fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
		require.NoError(t, fakeClient.Create(t.Context(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "jwt-private-key", Namespace: namespace},
			Data:       map[string][]byte{"privateKey": []byte("ignored-for-unsupported-alg")},
		}))

		s := &Server{log: testr.New(t), k8sClient: fakeClient}
		te := &aigv1a1.MCPBackendTokenExchange{
			ActorToken: &aigv1a1.MCPBackendTokenExchangeActorToken{
				ClientAssertionJWT: &aigv1a1.MCPTokenExchangeJWTActorConfig{
					Issuer:  "issuer",
					Subject: "subject",
					PrivateKeyRef: gwapiv1.SecretObjectReference{
						Name: "jwt-private-key",
					},
					SigningAlgorithm: ptr.To(customAlg),
				},
			},
		}

		_, _, err := s.loadTokenExchangeActorToken(t.Context(), namespace, te)
		require.Error(t, err)
		require.ErrorContains(t, err, "unsupported signing algorithm for client assertion JWT")
	})
}

func mustGenerateRSAPrivateKeyPEM(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	require.NotNil(t, pemBytes)
	return key, pemBytes
}

func mustGenerateECDSAPrivateKeyPEM(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	require.NotNil(t, pemBytes)
	return key, pemBytes
}
