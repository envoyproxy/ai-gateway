// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	dynmodulesextv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/dynamic_modules/v3"
	dynmodulesv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/dynamic_modules/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

const (
	// tokenExchangeFilterName is the name used for both the HCM filter and per-route config of the
	// BOE token-exchange Dynamic Module filter.
	tokenExchangeFilterName = "token-exchange"
	// tokenExchangeModuleName is the name of the Dynamic Module.
	// Envoy resolves this to lib${name}.so in ENVOY_DYNAMIC_MODULES_SEARCH_PATH.
	tokenExchangeModuleName = "aigateway"
	// stsClusterPrefix is the prefix for Envoy clusters created for STS endpoints.
	stsClusterPrefix = "mcp-sts-"
	// defaultActorTokenType is the default token type URI for actor tokens used in delegation mode.
	defaultActorTokenType = "urn:ietf:params:oauth:token-type:access_token" // #nosec G101
)

// tokenExchangePerRouteConfig is the per-route JSON configuration consumed by the BOE
// token-exchange Dynamic Module filter. Fields map to the RFC-8693 token exchange request
// parameters sent by the filter to the STS endpoint.
type tokenExchangePerRouteConfig struct {
	// Cluster is the Envoy cluster name used to reach the STS endpoint.
	Cluster string `json:"cluster"`
	// TokenExchangeURL is the URL of the STS token exchange endpoint.
	TokenExchangeURL string `json:"token_exchange_url"`
	// ClientID is the OAuth 2.0 client_id for authenticating the gateway to the STS.
	ClientID string `json:"client_id,omitempty"`
	// ClientSecret is the OAuth 2.0 client_secret for authenticating the gateway to the STS.
	ClientSecret string `json:"client_secret,omitempty"` // #nosec G101
	// SubjectTokenType is the token type URI for the subject_token parameter (RFC-8693 §3).
	SubjectTokenType string `json:"subject_token_type,omitempty"`
	// Audience identifies the intended recipient of the issued upstream token.
	Audience string `json:"audience,omitempty"`
	// Resource is the URI of the upstream resource (RFC-8693 §2.1, RFC-8707).
	Resource string `json:"resource,omitempty"`
	// Scope lists the requested OAuth 2.0 scopes (space-separated) for the issued token.
	Scope string `json:"scope,omitempty"`
	// RequestedTokenType specifies the desired type URI for the issued token.
	RequestedTokenType string `json:"requested_token_type,omitempty"`
	// ActorToken is the static actor token value used for delegation mode.
	ActorToken string `json:"actor_token,omitempty"` // #nosec G101
	// ActorTokenType is the token type URI for the actor_token parameter.
	ActorTokenType string `json:"actor_token_type,omitempty"`
	// TimeoutMs is the request timeout in milliseconds for the STS callout.
	TimeoutMs int `json:"timeout_ms,omitempty"`
}

// buildSTSClusterName returns a stable, deterministic Envoy cluster name derived from the first
// 4 bytes (8 hex chars) of the SHA-256 hash of the STS endpoint URL.
func buildSTSClusterName(stsEndpoint string) string {
	h := sha256.Sum256([]byte(stsEndpoint))
	return fmt.Sprintf("%s%x", stsClusterPrefix, h[:4])
}

// buildSTSCluster creates an Envoy STRICT_DNS cluster for connecting to an STS token endpoint.
// HTTPS endpoints are configured with a TLS transport socket; plain HTTP endpoints are not.
func buildSTSCluster(clusterName, stsEndpoint string) (*clusterv3.Cluster, error) {
	u, err := url.Parse(stsEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse STS endpoint %q: %w", stsEndpoint, err)
	}

	host := u.Hostname()
	port := uint32(443)
	if u.Port() != "" {
		p, parseErr := strconv.ParseUint(u.Port(), 10, 32)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse port in STS endpoint %q: %w", stsEndpoint, parseErr)
		}
		port = uint32(p)
	} else if u.Scheme == "http" {
		port = 80
	}

	c := &clusterv3.Cluster{
		Name:           clusterName,
		ConnectTimeout: durationpb.New(10 * time.Second),
		ClusterDiscoveryType: &clusterv3.Cluster_Type{
			Type: clusterv3.Cluster_STRICT_DNS,
		},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: clusterName,
			Endpoints: []*endpointv3.LocalityLbEndpoints{{
				LbEndpoints: []*endpointv3.LbEndpoint{{
					HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
						Endpoint: &endpointv3.Endpoint{
							Address: &corev3.Address{
								Address: &corev3.Address_SocketAddress{
									SocketAddress: &corev3.SocketAddress{
										Address:  host,
										Protocol: corev3.SocketAddress_TCP,
										PortSpecifier: &corev3.SocketAddress_PortValue{
											PortValue: port,
										},
									},
								},
							},
						},
					},
				}},
			}},
		},
	}

	if u.Scheme == "https" {
		// TODO(nacx): Make the CA configurable for privately hosted STS endpoints
		anyTLS, tlsErr := toAny(&tlsv3.UpstreamTlsContext{
			CommonTlsContext: &tlsv3.CommonTlsContext{
				ValidationContextType: &tlsv3.CommonTlsContext_ValidationContext{
					ValidationContext: &tlsv3.CertificateValidationContext{
						TrustedCa: &corev3.DataSource{
							Specifier: &corev3.DataSource_Filename{Filename: systemCertPath},
						},
					},
				},
			},
			Sni: host,
		})
		if tlsErr != nil {
			return nil, fmt.Errorf("failed to marshal TLS context for STS cluster %s: %w", clusterName, tlsErr)
		}
		c.TransportSocket = &corev3.TransportSocket{
			Name: "envoy.transport_sockets.tls",
			ConfigType: &corev3.TransportSocket_TypedConfig{
				TypedConfig: anyTLS,
			},
		}
	}

	return c, nil
}

// tokenExchangeHTTPFilter builds the DynamicModuleFilter HTTP filter configuration for the
// Dynamic Module filter. This is inserted into the backend listener's HTTP filter chain
// and operates in per-route mode; routes without per-route config are passed through unchanged.
// It is only inserted if there is at least one MCPRoute with a token-exchange security policy.
func tokenExchangeHTTPFilter() (*httpconnectionmanagerv3.HttpFilter, error) {
	dynModFilter := &dynmodulesv3.DynamicModuleFilter{
		DynamicModuleConfig: &dynmodulesextv3.DynamicModuleConfig{
			Name: tokenExchangeModuleName,
		},
		FilterName: tokenExchangeFilterName,
	}
	a, err := toAny(dynModFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal token-exchange dynamic module filter: %w", err)
	}
	return &httpconnectionmanagerv3.HttpFilter{
		Name:       tokenExchangeFilterName,
		Disabled:   true,
		ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{TypedConfig: a},
	}, nil
}

// maybeCreateSTSClusters lists all MCPRoute objects and appends a STRICT_DNS Envoy cluster to req
// for each unique STS endpoint referenced by a token-exchange backend security policy.
// It is a no-op when no k8sClient is configured (e.g. standalone mode or unit tests).
func (s *Server) maybeCreateSTSClusters(ctx context.Context, req *egextension.PostTranslateModifyRequest) (map[types.NamespacedName]aigv1b1.MCPRoute, error) {
	var mcpRoutes aigv1b1.MCPRouteList
	if err := s.k8sClient.List(ctx, &mcpRoutes); err != nil {
		return nil, fmt.Errorf("failed to list MCPRoutes: %w", err)
	}

	seen := make(map[string]bool)
	tokenExchangeRoutes := make(map[types.NamespacedName]aigv1b1.MCPRoute)
	for i := range mcpRoutes.Items {
		hasTokenExchange := false
		mcpRoute := mcpRoutes.Items[i]
		for _, backend := range mcpRoute.Spec.BackendRefs {
			if backend.SecurityPolicy == nil || backend.SecurityPolicy.TokenExchange == nil {
				continue
			}
			hasTokenExchange = true
			stsEndpoint := backend.SecurityPolicy.TokenExchange.STSEndpoint
			clusterName := buildSTSClusterName(stsEndpoint)
			if seen[clusterName] {
				continue
			}
			seen[clusterName] = true
			cluster, err := buildSTSCluster(clusterName, stsEndpoint)
			if err != nil {
				return nil, fmt.Errorf("failed to build STS cluster for %s: %w", stsEndpoint, err)
			}
			req.Clusters = append(req.Clusters, cluster)
			s.log.Info("Created STS cluster for token exchange", "cluster", clusterName, "sts", stsEndpoint)
		}

		if hasTokenExchange {
			routeKey := types.NamespacedName{Namespace: mcpRoute.Namespace, Name: mcpRoute.Name}
			tokenExchangeRoutes[routeKey] = mcpRoute
		}
	}

	return tokenExchangeRoutes, nil
}

// extractMCPHeaderMatchValue scans a route's match headers and returns the exact-match value
// for the given header name. Both legacy ExactMatch and modern StringMatch.Exact are checked.
func extractMCPHeaderMatchValue(route *routev3.Route, headerName string) string {
	for _, hm := range route.GetMatch().GetHeaders() {
		if hm.GetName() != headerName {
			continue
		}
		// Legacy exact match.
		if em := hm.GetExactMatch(); em != "" {
			return em
		}
		// Modern StringMatcher exact variant.
		if sm := hm.GetStringMatch(); sm != nil {
			if exact := sm.GetExact(); exact != "" {
				return exact
			}
		}
	}
	return ""
}

// maybeSetTokenExchangePerRouteConfig inspects a backend-listener route's match headers to
// determine the corresponding MCPRouteBackendRef. If that backend ref is configured with
// token-exchange, this function reads the necessary credentials from Kubernetes and sets the
// DynamicModuleFilterPerRoute TypedPerFilterConfig on the route so the BOE filter performs
// the exchange on every request reaching that route.
//
// It is a no-op when:
//   - The route does not carry MCPBackendHeader / MCPRouteHeader match headers.
//   - The matched backend ref does not have a TokenExchange security policy.
func (s *Server) maybeSetTokenExchangePerRouteConfig(ctx context.Context, route *routev3.Route, mcpRoutes map[types.NamespacedName]aigv1b1.MCPRoute) error {
	backendName := extractMCPHeaderMatchValue(route, internalapi.MCPBackendHeader)
	mcpRouteRef := extractMCPHeaderMatchValue(route, internalapi.MCPRouteHeader)
	if backendName == "" || mcpRouteRef == "" {
		return nil
	}

	// mcpRouteRef is "namespace/name".
	slashIdx := strings.Index(mcpRouteRef, "/")
	if slashIdx < 0 {
		return nil
	}
	namespace := mcpRouteRef[:slashIdx]
	routeName := mcpRouteRef[slashIdx+1:]

	mcpRoute, ok := mcpRoutes[types.NamespacedName{Namespace: namespace, Name: routeName}]
	if !ok {
		return fmt.Errorf("MCPRoute %s/%s not found in cache", namespace, routeName)
	}

	// Find the backend ref matching the route's MCPBackendHeader value.
	var te *aigv1b1.MCPBackendTokenExchange
	for i := range mcpRoute.Spec.BackendRefs {
		ref := &mcpRoute.Spec.BackendRefs[i]
		if string(ref.Name) == backendName &&
			ref.SecurityPolicy != nil &&
			ref.SecurityPolicy.TokenExchange != nil {
			te = ref.SecurityPolicy.TokenExchange
			break
		}
	}
	if te == nil {
		return nil // Not a token-exchange backend.
	}

	// Read client credentials if configured.
	clientID, clientSecret := "", ""
	if te.ClientAuth != nil {
		clientID = te.ClientAuth.ClientID
		secretNS := namespace
		if te.ClientAuth.ClientSecretRef.Namespace != nil {
			secretNS = string(*te.ClientAuth.ClientSecretRef.Namespace)
		}
		var secretObj corev1.Secret
		if err := s.k8sClient.Get(ctx, types.NamespacedName{
			Namespace: secretNS,
			Name:      string(te.ClientAuth.ClientSecretRef.Name),
		}, &secretObj); err != nil {
			return fmt.Errorf("failed to get client secret for backend %s: %w", backendName, err)
		}
		val, ok := secretObj.Data["clientSecret"]
		if !ok {
			return fmt.Errorf("client secret key \"clientSecret\" not found in secret %s/%s", secretNS, te.ClientAuth.ClientSecretRef.Name)
		}
		clientSecret = string(val)
	}

	// Read actor token for Delegation mode when a SecretRef actor token is configured.
	// JWT-based actor tokens are defined in the API but not yet implemented.
	actorToken, actorTokenType, err := s.mcpUpstreamTokenProvider.GetToken(ctx, namespace, te)
	if err != nil {
		return fmt.Errorf("failed to load actor token for backend %s: %w", backendName, err)
	}

	// Build the BOE per-route JSON config.
	clusterName := buildSTSClusterName(te.STSEndpoint)
	cfg := tokenExchangePerRouteConfig{
		Cluster:          clusterName,
		TokenExchangeURL: te.STSEndpoint,
		ClientID:         clientID,
		ClientSecret:     clientSecret,
		ActorToken:       actorToken,
		ActorTokenType:   actorTokenType,
	}
	if te.SubjectTokenType != nil {
		cfg.SubjectTokenType = *te.SubjectTokenType
	}
	if te.Audience != nil {
		cfg.Audience = *te.Audience
	}
	if te.Resource != nil {
		cfg.Resource = *te.Resource
	}
	if len(te.Scopes) > 0 {
		cfg.Scope = strings.Join(te.Scopes, " ")
	}
	if te.RequestedTokenType != nil {
		cfg.RequestedTokenType = *te.RequestedTokenType
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal token-exchange per-route config for backend %s: %w", backendName, err)
	}

	// Wrap the JSON string in a StringValue Any so the BOE filter receives it as a plain string.
	svAny, err := toAny(wrapperspb.String(string(cfgJSON)))
	if err != nil {
		return fmt.Errorf("failed to marshal StringValue Any for token-exchange per-route config: %w", err)
	}
	perRouteProto := &dynmodulesv3.DynamicModuleFilterPerRoute{
		DynamicModuleConfig: &dynmodulesextv3.DynamicModuleConfig{
			Name: tokenExchangeModuleName,
		},
		PerRouteConfigName: tokenExchangeFilterName,
		FilterConfig:       svAny,
	}
	perRouteAny, err := toAny(perRouteProto)
	if err != nil {
		return fmt.Errorf("failed to marshal DynamicModuleFilterPerRoute for backend %s: %w", backendName, err)
	}

	if route.TypedPerFilterConfig == nil {
		route.TypedPerFilterConfig = make(map[string]*anypb.Any)
	}
	route.TypedPerFilterConfig[tokenExchangeFilterName] = perRouteAny
	s.log.Info("Set token-exchange per-route config", "route", route.Name, "backend", backendName)
	return nil
}

// mcpUpstreamTokenProvider is responsible for providing tokens for upstream calls to MCP servers.
// It supports both static actor tokens and dynamic JWT-based actor tokens used in delegation mode. For dynamic
// JWT-based actor tokens, it generates and signs a JWT on demand using the configured claims and private key,
// and caches the token until shortly before expiry to avoid unnecessary regeneration and signing on every request.
type mcpUpstreamTokenProvider struct {
	k8sClient client.Client
	token     string    // last issued token
	expiry    time.Time // last issued token expiration
}

func (m *mcpUpstreamTokenProvider) GetToken(ctx context.Context, namespace string, te *aigv1b1.MCPBackendTokenExchange) (string, string, error) {
	if te == nil || te.ActorToken == nil {
		return "", "", nil
	}

	// If the token is provided via SecretRef, load it from Kubernetes.
	if te.ActorToken.SecretRef != nil {
		secretObj, err := loadSecret(ctx, m.k8sClient, te.ActorToken.SecretRef, namespace)
		if err != nil {
			return "", "", fmt.Errorf("failed to load actor token secret: %w", err)
		}
		val, ok := secretObj.Data["token"]
		if !ok {
			return "", "", fmt.Errorf("actor token key \"token\" not found in secret %s/%s", namespace, te.ActorToken.SecretRef.Name)
		}
		return string(val), defaultActorTokenType, nil // #nosec G101
	}

	// Otherwise, generate the token with the provided data

	// If the last generated token is not expired, return it to avoid unnecessary regeneration and signing that
	// could potentially trigger an unnecessary config change and RDS update in Envoy.
	if m.token != "" && time.Until(m.expiry) > 30*time.Second {
		return m.token, defaultActorTokenType, nil
	}

	actorTokenConfig := te.ActorToken.ClientAssertionJWT
	privateKey, err := loadSecret(ctx, m.k8sClient, &actorTokenConfig.PrivateKeyRef, namespace)
	if err != nil {
		return "", "", fmt.Errorf("failed to load client assertion JWT private key secret: %w", err)
	}

	lifetime := 300 * time.Second
	if actorTokenConfig.Lifetime != nil {
		lifetime = time.Duration(*actorTokenConfig.Lifetime) * time.Second
	}

	now := time.Now()
	exp := now.Add(lifetime)
	claims := jwt.RegisteredClaims{
		Issuer:    actorTokenConfig.Issuer,
		Subject:   actorTokenConfig.Subject,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}

	var signingMethod jwt.SigningMethod = jwt.SigningMethodRS256
	if actorTokenConfig.SigningAlgorithm != nil {
		signingMethod = jwt.GetSigningMethod(*actorTokenConfig.SigningAlgorithm)
	}

	keyData, ok := privateKey.Data["privateKey"]
	if !ok {
		return "", "", fmt.Errorf("private key data key \"privateKey\" not found in secret %s/%s", namespace, actorTokenConfig.PrivateKeyRef.Name)
	}
	var key any
	switch signingMethod {
	case jwt.SigningMethodRS256, jwt.SigningMethodRS384, jwt.SigningMethodRS512, jwt.SigningMethodPS256, jwt.SigningMethodPS384, jwt.SigningMethodPS512:
		key, err = jwt.ParseRSAPrivateKeyFromPEM(keyData)
	case jwt.SigningMethodES256, jwt.SigningMethodES384, jwt.SigningMethodES512:
		key, err = jwt.ParseECPrivateKeyFromPEM(keyData)
	case jwt.SigningMethodHS256, jwt.SigningMethodHS384, jwt.SigningMethodHS512:
		key = keyData
	default:
		err = fmt.Errorf("unsupported signing algorithm for client assertion JWT: %s", *actorTokenConfig.SigningAlgorithm)
	}
	if err != nil {
		return "", "", fmt.Errorf("failed to parse client assertion JWT private key: %w", err)
	}

	token := jwt.NewWithClaims(signingMethod, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		return "", "", fmt.Errorf("failed to sign client assertion JWT: %w", err)
	}

	// Cache the generated token and its expiry time to avoid unnecessary regeneration on every request.
	m.token = signed
	m.expiry = exp
	return signed, defaultActorTokenType, nil
}

func loadSecret(ctx context.Context, k8sClient client.Client, ref *gwapiv1.SecretObjectReference, defaultNS string) (corev1.Secret, error) {
	ns := defaultNS
	if ref.Namespace != nil {
		ns = string(*ref.Namespace)
	}
	var secretObj corev1.Secret
	if err := k8sClient.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      string(ref.Name),
	}, &secretObj); err != nil {
		return corev1.Secret{}, fmt.Errorf("failed to get secret %s/%s: %w", ns, ref.Name, err)
	}
	return secretObj, nil
}

// Code borrowed from: https://github.com/envoyproxy/gateway/blob/main/internal/utils/cert/cert.go

// canonicalCertPath is the Debian/Ubuntu CA path used as a canonical value in golden files.
// The envoy-proxy image uses Debian, so this matches the runtime path.
const canonicalCertPath = "/etc/ssl/certs/ca-certificates.crt"

// systemCertPath is the default location of the system trust store, initialized at runtime once.
//
// This assumes the Envoy running in a very specific environment. For example, the default location of the system
// trust store on Debian derivatives like the envoy-proxy image being used by the infrastructure controller.
var systemCertPath = func() string {
	switch runtime.GOOS {
	case "darwin":
		// TODO: maybe automatically get the keychain cert? That might be macOS version dependent.
		// For now, we'll just use the root cert installed by Homebrew: brew install ca-certificates.
		// See:
		// * https://apple.stackexchange.com/questions/226375/where-are-the-root-cas-stored-on-os-x
		// * https://superuser.com/questions/992167/where-are-digital-certificates-physically-stored-on-a-mac-os-x-machine
		return "/opt/homebrew/etc/ca-certificates/cert.pem"
	default:
		return canonicalCertPath
	}
}()
