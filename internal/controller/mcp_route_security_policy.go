// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwapiv1a3 "sigs.k8s.io/gateway-api/apis/v1alpha3"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	oauthWellKnownProtectedResourceMetadataPath   = "/.well-known/oauth-protected-resource"
	oauthWellKnownAuthorizationServerMetadataPath = "/.well-known/oauth-authorization-server"

	oauthProtectedResourceMetadataSuffix = "-oauth-protected-resource-metadata"
	oauthAuthServerMetadataSuffix        = "-oauth-authorization-server-metadata"

	httpClientTimeout   = 5 * time.Second
	maxRetryElapsedTime = 10 * time.Second
)

// syncMCPRouteSecurityPolicy reconciles MCPRouteSecurityPolicy and creates/updates its associated envoy gateway resources.
func (c *MCPRouteController) syncMCPRouteSecurityPolicy(ctx context.Context, mcpRoute *aigv1a1.MCPRoute, httpRouteName string) error {
	if mcpRoute.Spec.SecurityPolicy != nil && mcpRoute.Spec.SecurityPolicy.OAuth != nil {
		// Create and manage SecurityPolicy to enforce JWT authentication for access token validation.
		// The SecurityPolicy will be associated with the HTTPRoute MCP proxy rule only.
		if secErr := c.ensureAccessTokenSecurityPolicy(ctx, mcpRoute, httpRouteName); secErr != nil {
			return fmt.Errorf("failed to ensure SecurityPolicy: %w", secErr)
		}

		// Create BackendTrafficPolicy for WWW-Authenticate header with OAuth resource metadata in 401 responses.
		if btpErr := c.ensureOAuthProtectedResourceMetadataBTP(ctx, mcpRoute, httpRouteName); btpErr != nil {
			return fmt.Errorf("failed to ensure BackendTrafficPolicy: %w", btpErr)
		}

		// Create HTTPRouteFilter for OAuth protected resource metadata endpoint.
		if hrfErr := c.ensureOAuthProtectedResourceMetadataHRF(ctx, mcpRoute); hrfErr != nil {
			return fmt.Errorf("failed to ensure HTTPRouteFilter: %w", hrfErr)
		}

		// Create HTTPRouteFilter for OAuth authorization server metadata endpoint.
		if hrfErr := c.ensureOAuthAuthServerMetadataHTTPRouteFilter(ctx, mcpRoute); hrfErr != nil {
			return fmt.Errorf("failed to ensure AuthServer HTTPRouteFilter: %w", hrfErr)
		}
	} else {
		// Clean up existing SecurityPolicy resources when SecurityPolicy is nil.
		if err := c.cleanupSecurityPolicyResources(ctx, mcpRoute); err != nil {
			return fmt.Errorf("failed to cleanup SecurityPolicy resources: %w", err)
		}
	}

	return nil
}

// ensureAccessTokenSecurityPolicy ensures that the SecurityPolicy resource exists with JWT authentication to validate access tokens.
func (c *MCPRouteController) ensureAccessTokenSecurityPolicy(ctx context.Context, mcpRoute *aigv1a1.MCPRoute, httpRouteName string) error {
	var securityPolicy egv1a1.SecurityPolicy
	securityPolicyName := internalapi.MCPGeneratedResourceCommonPrefix + mcpRoute.Name
	err := c.client.Get(ctx, client.ObjectKey{Name: securityPolicyName, Namespace: mcpRoute.Namespace}, &securityPolicy)
	existingPolicy := err == nil

	if apierrors.IsNotFound(err) {
		// SecurityPolicy doesn't exist, create it.
		securityPolicy = egv1a1.SecurityPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      securityPolicyName,
				Namespace: mcpRoute.Namespace,
			},
		}
		// Set owner reference to mcpRoute for garbage collection.
		if err = ctrlutil.SetControllerReference(mcpRoute, &securityPolicy, c.client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference for SecurityPolicy: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get SecurityPolicy: %w", err)
	}

	// Configure JWT authentication.
	auth := mcpRoute.Spec.SecurityPolicy.OAuth
	name := "mcp-jwt-provider"
	jwtProvider := egv1a1.JWTProvider{
		Name:      name,
		Audiences: auth.Audiences,
	}

	// Configure JWKS source: use explicit config if provided, otherwise auto-discover from authorization server metadata.
	if auth.JWKS != nil {
		jwtProvider.RemoteJWKS = auth.JWKS.RemoteJWKS
		jwtProvider.LocalJWKS = auth.JWKS.LocalJWKS
	} else {
		// Auto-discover JWKS URI from authorization server metadata.
		c.logger.Info("Auto-discovering JWKS URI from authorization server metadata", "issuer", auth.Issuer)
		jwksURI, discoveryErr := c.discoverJWKSURI(auth.Issuer)
		if discoveryErr != nil {
			return fmt.Errorf("failed to auto-discover JWKS URI: %w", discoveryErr)
		}
		c.logger.Info("Found JWKS URI from authorization server metadata", "issuer", auth.Issuer, "jwks_uri", jwksURI)

		jwtProvider.RemoteJWKS = &egv1a1.RemoteJWKS{
			URI: jwksURI,
		}

		// If the JWKS URI is an HTTPS backend, try to discover the backend service for Envoy to fetch JWKS.
		if strings.HasPrefix(jwksURI, "https://") {
			c.logger.Info("Auto-discovering backends with TLS config to fetch JWKS URI", "jwks_uri", jwksURI)
			jwtProvider.RemoteJWKS.BackendRefs, err = c.tryGetBackendsForJWKS(ctx, jwksURI)
			if err != nil {
				// log the error but continue, hoping the JWKS can be fetched without explicitly setting the backend cluster.
				c.logger.Error(err, "could not find a backend with TLS config to fetch the remote JWKS", "jwks_uri", jwksURI)
			}
		}
	}

	securityPolicy.Spec = egv1a1.SecurityPolicySpec{
		JWT: &egv1a1.JWT{
			Providers: []egv1a1.JWTProvider{jwtProvider},
		},
	}

	// The SecurityPolicy should only apply to the HTTPRoute MCP proxy rule.
	// However, since HTTPRouteRule name is experimental in Gateway API, and some vendors (e.g. GKE Gateway) do not
	// support it yet, we currently do not set the sectionName to avoid compatibility issues.
	// The jwt filter will be removed from backend routes in the extension server.
	// TODO: use sectionName to target the MCP proxy rule only when the HTTPRouteRule name is in stable channel.
	securityPolicy.Spec.TargetRefs = []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		{
			LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
				Group: "gateway.networking.k8s.io",
				Kind:  "HTTPRoute",
				Name:  gwapiv1.ObjectName(httpRouteName),
			},
		},
	}

	if existingPolicy {
		c.logger.Info("Updating SecurityPolicy", "namespace", securityPolicy.Namespace, "name", securityPolicy.Name)
		if err = c.client.Update(ctx, &securityPolicy); err != nil {
			return fmt.Errorf("failed to update SecurityPolicy: %w", err)
		}
	} else {
		c.logger.Info("Creating SecurityPolicy", "namespace", securityPolicy.Namespace, "name", securityPolicy.Name)
		if err = c.client.Create(ctx, &securityPolicy); err != nil {
			return fmt.Errorf("failed to create SecurityPolicy: %w", err)
		}
	}

	return nil
}

// ensureOAuthProtectedResourceMetadataBTP ensures that the BackendTrafficPolicy resource exists with response override for WWW-Authenticate header.
func (c *MCPRouteController) ensureOAuthProtectedResourceMetadataBTP(ctx context.Context, mcpRoute *aigv1a1.MCPRoute, httpRouteName string) error {
	var backendTrafficPolicy egv1a1.BackendTrafficPolicy
	backendTrafficPolicyName := oauthProtectedResourceMetadataName(mcpRoute.Name)
	err := c.client.Get(ctx, client.ObjectKey{Name: backendTrafficPolicyName, Namespace: mcpRoute.Namespace}, &backendTrafficPolicy)
	existingPolicy := err == nil

	if apierrors.IsNotFound(err) {
		// BackendTrafficPolicy doesn't exist, create it.
		backendTrafficPolicy = egv1a1.BackendTrafficPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      backendTrafficPolicyName,
				Namespace: mcpRoute.Namespace,
			},
		}
		// Set owner reference to mcpRoute for garbage collection.
		if err = ctrlutil.SetControllerReference(mcpRoute, &backendTrafficPolicy, c.client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference for BackendTrafficPolicy: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get BackendTrafficPolicy: %w", err)
	}

	// Build WWW-Authenticate header value based on RFC 9728 and MCP spec.
	auth := mcpRoute.Spec.SecurityPolicy.OAuth
	wwwAuthenticateValue := buildWWWAuthenticateHeaderValue(&auth.ProtectedResourceMetadata)

	// Configure response override for 401 responses.
	backendTrafficPolicy.Spec = egv1a1.BackendTrafficPolicySpec{
		ResponseOverride: []*egv1a1.ResponseOverride{
			{
				Match: egv1a1.CustomResponseMatch{
					StatusCodes: []egv1a1.StatusCodeMatch{
						{
							Type:  ptr.To(egv1a1.StatusCodeValueTypeValue),
							Value: ptr.To(http.StatusUnauthorized),
						},
					},
				},
				Response: &egv1a1.CustomResponse{
					StatusCode: ptr.To(http.StatusUnauthorized),
					Body: &egv1a1.CustomResponseBody{
						Type:   ptr.To(egv1a1.ResponseValueTypeInline),
						Inline: ptr.To(wwwAuthenticateValue),
					},
					// TODO: use Header when supported in Envoy Gateway. https://github.com/envoyproxy/gateway/pull/6308.
					// For now, use Body to set the WWW-Authenticate header value, and we move it to Header in the extension server.
					/*Header: &gwapiv1.HTTPHeaderFilter{
						Set: []gwapiv1.HTTPHeader{
							{
								Name:  "WWW-Authenticate",
								Value: wwwAuthenticateValue,
							},
						},
					},*/
				},
			},
		},
	}

	// Target the HTTPRoute MCP proxy rule only.
	backendTrafficPolicy.Spec.TargetRefs = []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		{
			LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
				Group: "gateway.networking.k8s.io",
				Kind:  "HTTPRoute",
				Name:  gwapiv1.ObjectName(httpRouteName),
			},
			// TODO: this filter should be applied to the MCP proxy rule only, enable sectionName when supported in Envoy Gateway.
			// SectionName: ptr.To(gwapiv1a2.SectionName(mcpProxyRuleName)).
		},
	}

	if existingPolicy {
		c.logger.Info("Updating BackendTrafficPolicy", "namespace", backendTrafficPolicy.Namespace, "name", backendTrafficPolicy.Name)
		if err = c.client.Update(ctx, &backendTrafficPolicy); err != nil {
			return fmt.Errorf("failed to update BackendTrafficPolicy: %w", err)
		}
	} else {
		c.logger.Info("Creating BackendTrafficPolicy", "namespace", backendTrafficPolicy.Namespace, "name", backendTrafficPolicy.Name)
		if err = c.client.Create(ctx, &backendTrafficPolicy); err != nil {
			return fmt.Errorf("failed to create BackendTrafficPolicy: %w", err)
		}
	}

	return nil
}

// buildWWWAuthenticateHeaderValue constructs the WWW-Authenticate header value according to RFC 9728.
// References:
// * https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization#authorization-server-location
// * https://datatracker.ietf.org/doc/html/rfc9728#name-www-authenticate-response
func buildWWWAuthenticateHeaderValue(metadata *aigv1a1.ProtectedResourceMetadata) string {
	// Build resource metadata URL using RFC 8414 compliant pattern.
	// Extract base URL and path from resource identifier.
	resourceURL := strings.TrimSuffix(metadata.Resource, "/")

	var baseURL string
	var prefixLen int
	switch {
	case strings.HasPrefix(resourceURL, "https://"):
		prefixLen = 8
	case strings.HasPrefix(resourceURL, "http://"):
		prefixLen = 7
	default: // should not happen as CEL validation should have caught it.
		prefixLen = 0
	}

	if idx := strings.Index(resourceURL[prefixLen:], "/"); idx != -1 {
		baseURL = resourceURL[:prefixLen+idx]
	} else {
		baseURL = resourceURL
	}

	// Some agents do not expect the path component to be included in the resource_metadata URL.
	// TODO: test with different agents and see if this would cause issues.
	// resourceMetadataURL := fmt.Sprintf("%s/.well-known/oauth-protected-resource%s", baseURL, pathComponent).
	resourceMetadataURL := fmt.Sprintf("%s%s", baseURL, oauthWellKnownProtectedResourceMetadataPath)
	// Build the basic Bearer challenge.
	headerValue := `Bearer error="invalid_request", error_description="No access token was provided in this request"`

	// Add resource_metadata as per RFC 9728 Section 5.1.
	headerValue = fmt.Sprintf(`%s, resource_metadata="%s"`, headerValue, resourceMetadataURL)

	return headerValue
}

// ensureOAuthProtectedResourceMetadataHRF ensures that the HTTPRouteFilter resource exists with direct response for OAuth metadata.
func (c *MCPRouteController) ensureOAuthProtectedResourceMetadataHRF(ctx context.Context, mcpRoute *aigv1a1.MCPRoute) error {
	if mcpRoute.Spec.SecurityPolicy == nil || mcpRoute.Spec.SecurityPolicy.OAuth == nil {
		return nil
	}

	var httpRouteFilter egv1a1.HTTPRouteFilter
	httpRouteFilterName := oauthProtectedResourceMetadataName(mcpRoute.Name)
	err := c.client.Get(ctx, client.ObjectKey{Name: httpRouteFilterName, Namespace: mcpRoute.Namespace}, &httpRouteFilter)
	existingFilter := err == nil

	if apierrors.IsNotFound(err) {
		// HTTPRouteFilter doesn't exist, create it.
		httpRouteFilter = egv1a1.HTTPRouteFilter{
			ObjectMeta: metav1.ObjectMeta{
				Name:      httpRouteFilterName,
				Namespace: mcpRoute.Namespace,
			},
		}
		// Set owner reference to mcpRoute for garbage collection.
		if err = ctrlutil.SetControllerReference(mcpRoute, &httpRouteFilter, c.client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference for HTTPRouteFilter: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get HTTPRouteFilter: %w", err)
	}

	// Build OAuth protected resource metadata JSON response.
	metadataJSON := buildOAuthProtectedResourceMetadataJSON(mcpRoute.Spec.SecurityPolicy.OAuth)

	// Configure direct response with OAuth metadata.
	httpRouteFilter.Spec = egv1a1.HTTPRouteFilterSpec{
		DirectResponse: &egv1a1.HTTPDirectResponseFilter{
			ContentType: ptr.To("application/json"),
			StatusCode:  ptr.To(http.StatusOK),
			Body: &egv1a1.CustomResponseBody{
				Type:   ptr.To(egv1a1.ResponseValueTypeInline),
				Inline: ptr.To(metadataJSON),
			},
		},
	}

	if existingFilter {
		c.logger.Info("Updating HTTPRouteFilter", "namespace", httpRouteFilter.Namespace, "name", httpRouteFilter.Name)
		if err = c.client.Update(ctx, &httpRouteFilter); err != nil {
			return fmt.Errorf("failed to update HTTPRouteFilter: %w", err)
		}
	} else {
		c.logger.Info("Creating HTTPRouteFilter", "namespace", httpRouteFilter.Namespace, "name", httpRouteFilter.Name)
		if err = c.client.Create(ctx, &httpRouteFilter); err != nil {
			return fmt.Errorf("failed to create HTTPRouteFilter: %w", err)
		}
	}

	return nil
}

// ensureOAuthAuthServerMetadataHTTPRouteFilter ensures that the HTTPRouteFilter resource exists with direct response for OAuth authorization server metadata.
func (c *MCPRouteController) ensureOAuthAuthServerMetadataHTTPRouteFilter(ctx context.Context, mcpRoute *aigv1a1.MCPRoute) error {
	var httpRouteFilter egv1a1.HTTPRouteFilter
	authServerFilterName := oauthAuthServerMetadataFilterName(mcpRoute.Name)
	err := c.client.Get(ctx, client.ObjectKey{Name: authServerFilterName, Namespace: mcpRoute.Namespace}, &httpRouteFilter)
	existingFilter := err == nil

	if apierrors.IsNotFound(err) {
		// HTTPRouteFilter doesn't exist, create it.
		httpRouteFilter = egv1a1.HTTPRouteFilter{
			ObjectMeta: metav1.ObjectMeta{
				Name:      authServerFilterName,
				Namespace: mcpRoute.Namespace,
			},
		}
		// Set owner reference to mcpRoute for garbage collection.
		if err = ctrlutil.SetControllerReference(mcpRoute, &httpRouteFilter, c.client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference for HTTPRouteFilter: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get HTTPRouteFilter: %w", err)
	}

	// Build OAuth authorization server metadata JSON response.
	metadataJSON := c.buildOAuthAuthServerMetadataJSON(mcpRoute.Spec.SecurityPolicy.OAuth)

	// Configure direct response with OAuth authorization server metadata.
	httpRouteFilter.Spec = egv1a1.HTTPRouteFilterSpec{
		DirectResponse: &egv1a1.HTTPDirectResponseFilter{
			ContentType: ptr.To("application/json"),
			StatusCode:  ptr.To(http.StatusOK),
			Body: &egv1a1.CustomResponseBody{
				Type:   ptr.To(egv1a1.ResponseValueTypeInline),
				Inline: ptr.To(metadataJSON),
			},
		},
	}

	if existingFilter {
		c.logger.Info("Updating AuthServer HTTPRouteFilter", "namespace", httpRouteFilter.Namespace, "name", httpRouteFilter.Name)
		if err = c.client.Update(ctx, &httpRouteFilter); err != nil {
			return fmt.Errorf("failed to update HTTPRouteFilter: %w", err)
		}
	} else {
		c.logger.Info("Creating AuthServer HTTPRouteFilter", "namespace", httpRouteFilter.Namespace, "name", httpRouteFilter.Name)
		if err = c.client.Create(ctx, &httpRouteFilter); err != nil {
			return fmt.Errorf("failed to create HTTPRouteFilter: %w", err)
		}
	}

	return nil
}

// buildOAuthProtectedResourceMetadataJSON constructs the OAuth protected resource metadata JSON response.
// References:
// * https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization#authorization-server-location
// * https://datatracker.ietf.org/doc/html/rfc9728#name-protected-resource-metadata
func buildOAuthProtectedResourceMetadataJSON(auth *aigv1a1.MCPRouteOAuth) string {
	response := map[string]interface{}{
		"resource":                 auth.ProtectedResourceMetadata.Resource,
		"authorization_servers":    []string{auth.Issuer},
		"bearer_methods_supported": []string{"header"},
	}
	if auth.ProtectedResourceMetadata.ResourceName != nil && *auth.ProtectedResourceMetadata.ResourceName != "" {
		response["resource_name"] = auth.ProtectedResourceMetadata.ResourceName
	}
	if len(auth.ProtectedResourceMetadata.ScopesSupported) != 0 {
		response["scopes_supported"] = auth.ProtectedResourceMetadata.ScopesSupported
	}
	if auth.ProtectedResourceMetadata.ResourceName != nil && *auth.ProtectedResourceMetadata.ResourceName != "" {
		response["resource_name"] = auth.ProtectedResourceMetadata.ResourceName
	}
	if len(auth.ProtectedResourceMetadata.ResourceSigningAlgValuesSupported) > 0 {
		response["resource_signing_alg_values_supported"] = auth.ProtectedResourceMetadata.ResourceSigningAlgValuesSupported
	}
	if auth.ProtectedResourceMetadata.ResourceDocumentation != nil && *auth.ProtectedResourceMetadata.ResourceDocumentation != "" {
		response["resource_documentation"] = auth.ProtectedResourceMetadata.ResourceDocumentation
	}
	if auth.ProtectedResourceMetadata.ResourcePolicyURI != nil && *auth.ProtectedResourceMetadata.ResourcePolicyURI != "" {
		response["resource_policy_uri"] = auth.ProtectedResourceMetadata.ResourcePolicyURI
	}

	// Convert to JSON string.
	jsonBytes, _ := json.Marshal(response)
	return string(jsonBytes)
}

// buildOAuthAuthServerMetadataJSON constructs the OAuth authorization server metadata JSON response.
// It first attempts to fetch metadata from the authorization server's well-known endpoint,
// and falls back to hardcoded values if the fetch fails.
// References:
// * https://modelcontextprotocol.io/specification/2025-03-26/basic/authorization#authorization-server-location
// * https://datatracker.ietf.org/doc/html/rfc8414#section-3.2
func (c *MCPRouteController) buildOAuthAuthServerMetadataJSON(oauth *aigv1a1.MCPRouteOAuth) string {
	// For 2025-03-26 compatibility, we return the authorization server metadata.

	// The authorization server's issuer identifier, which is a URL that uses the "https" scheme and has no query or
	// fragment components.  Authorization server metadata is published at a location that is ".well-known" according
	// to RFC 5785 derived from this issuer identifier, as described in Section 3.
	// https://datatracker.ietf.org/doc/html/rfc8414#section-2
	authServer := strings.TrimSuffix(oauth.Issuer, "/")

	// Try to fetch metadata from the well-known endpoint first.
	if authServer != "" {
		fetchedMetadata, err := fetchOAuthAuthServerMetadata(authServer)
		if err == nil && fetchedMetadata != nil {
			// Convert to JSON string and return.
			jsonBytes, _ := json.Marshal(fetchedMetadata)
			return string(jsonBytes)
		}
		// If there was an error fetching metadata, log it.
		if err != nil {
			c.logger.Error(err, "failed to fetch OAuth authorization server metadata from well-known endpoint", "authServer", authServer)
		}
	}

	c.logger.Info("Falling back to hardcoded OAuth authorization server metadata", "authServer", authServer)

	// Fallback to hardcoded values if fetching fails or authServer is empty
	// These fields are currently hardcoded for keycloak compatibility.
	// We don't fail the reconciliation if fetching metadata fails,
	// as the authorization server metadata is just for back-compatibility with the MCP spec 2025-03-26.
	response := map[string]interface{}{
		"issuer":                                authServer,
		"authorization_endpoint":                authServer + "/protocol/openid-connect/auth",
		"token_endpoint":                        authServer + "/protocol/openid-connect/token",
		"jwks_uri":                              authServer + "/protocol/openid-connect/certs",
		"scopes_supported":                      oauth.ProtectedResourceMetadata.ScopesSupported,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
	}

	// Convert to JSON string.
	jsonBytes, _ := json.Marshal(response)
	return string(jsonBytes)
}

// cleanupSecurityPolicyResources deletes existing SecurityPolicy-related resources when SecurityPolicy is nil.
func (c *MCPRouteController) cleanupSecurityPolicyResources(ctx context.Context, mcpRoute *aigv1a1.MCPRoute) error {
	// Delete SecurityPolicy.
	securityPolicyName := internalapi.MCPGeneratedResourceCommonPrefix + mcpRoute.Name
	var securityPolicy egv1a1.SecurityPolicy
	err := c.client.Get(ctx, client.ObjectKey{Name: securityPolicyName, Namespace: mcpRoute.Namespace}, &securityPolicy)
	if err == nil {
		c.logger.Info("Deleting SecurityPolicy", "namespace", securityPolicy.Namespace, "name", securityPolicy.Name)
		if err = c.client.Delete(ctx, &securityPolicy); err != nil {
			return fmt.Errorf("failed to delete SecurityPolicy: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get SecurityPolicy for deletion: %w", err)
	}

	// Delete BackendTrafficPolicy.
	backendTrafficPolicyName := oauthProtectedResourceMetadataName(mcpRoute.Name)
	var backendTrafficPolicy egv1a1.BackendTrafficPolicy
	err = c.client.Get(ctx, client.ObjectKey{Name: backendTrafficPolicyName, Namespace: mcpRoute.Namespace}, &backendTrafficPolicy)
	if err == nil {
		c.logger.Info("Deleting BackendTrafficPolicy", "namespace", backendTrafficPolicy.Namespace, "name", backendTrafficPolicy.Name)
		if err = c.client.Delete(ctx, &backendTrafficPolicy); err != nil {
			return fmt.Errorf("failed to delete BackendTrafficPolicy: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get BackendTrafficPolicy for deletion: %w", err)
	}

	// Delete OAuth protected resource HTTPRouteFilter.
	httpRouteFilterName := oauthProtectedResourceMetadataName(mcpRoute.Name)
	var httpRouteFilter egv1a1.HTTPRouteFilter
	err = c.client.Get(ctx, client.ObjectKey{Name: httpRouteFilterName, Namespace: mcpRoute.Namespace}, &httpRouteFilter)
	if err == nil {
		c.logger.Info("Deleting HTTPRouteFilter", "namespace", httpRouteFilter.Namespace, "name", httpRouteFilter.Name)
		if err = c.client.Delete(ctx, &httpRouteFilter); err != nil {
			return fmt.Errorf("failed to delete HTTPRouteFilter: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get HTTPRouteFilter for deletion: %w", err)
	}

	// Delete OAuth authorization server HTTPRouteFilter.
	authServerFilterName := oauthAuthServerMetadataFilterName(mcpRoute.Name)
	var authServerFilter egv1a1.HTTPRouteFilter
	err = c.client.Get(ctx, client.ObjectKey{Name: authServerFilterName, Namespace: mcpRoute.Namespace}, &authServerFilter)
	if err == nil {
		c.logger.Info("Deleting AuthServer HTTPRouteFilter", "namespace", authServerFilter.Namespace, "name", authServerFilter.Name)
		if err = c.client.Delete(ctx, &authServerFilter); err != nil {
			return fmt.Errorf("failed to delete AuthServer HTTPRouteFilter: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get AuthServer HTTPRouteFilter for deletion: %w", err)
	}

	return nil
}

// OAuthAuthServerMetadata represents the OAuth authorization server metadata response
// from the /.well-known/oauth-authorization-server endpoint as defined in RFC 8414.
// https://datatracker.ietf.org/doc/html/rfc8414#section-2
type OAuthAuthServerMetadata struct {
	Issuer                                             string   `json:"issuer"`
	AuthorizationEndpoint                              string   `json:"authorization_endpoint"`
	TokenEndpoint                                      string   `json:"token_endpoint"`
	JwksURI                                            string   `json:"jwks_uri"`
	RegistrationEndpoint                               string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                                    []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported                             []string `json:"response_types_supported,omitempty"`
	ResponseModesSupported                             []string `json:"response_modes_supported,omitempty"`
	GrantTypesSupported                                []string `json:"grant_types_supported,omitempty"`
	TokenEndpointAuthMethodsSupported                  []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	TokenEndpointAuthSigningAlgValuesSupported         []string `json:"token_endpoint_auth_signing_alg_values_supported,omitempty"`
	ServiceDocumentation                               string   `json:"service_documentation,omitempty"`
	UILocalesSupported                                 []string `json:"ui_locales_supported,omitempty"`
	OPPolicyURI                                        string   `json:"op_policy_uri,omitempty"`
	OPTosURI                                           string   `json:"op_tos_uri,omitempty"`
	RevocationEndpoint                                 string   `json:"revocation_endpoint,omitempty"`
	RevocationEndpointAuthMethodsSupported             []string `json:"revocation_endpoint_auth_methods_supported,omitempty"`
	RevocationEndpointAuthSigningAlgValuesSupported    []string `json:"revocation_endpoint_auth_signing_alg_values_supported,omitempty"`
	IntrospectionEndpoint                              string   `json:"introspection_endpoint,omitempty"`
	IntrospectionEndpointAuthMethodsSupported          []string `json:"introspection_endpoint_auth_methods_supported,omitempty"`
	IntrospectionEndpointAuthSigningAlgValuesSupported []string `json:"introspection_endpoint_auth_signing_alg_values_supported,omitempty"`
	CodeChallengeMethodsSupported                      []string `json:"code_challenge_methods_supported,omitempty"`
}

// fetchOAuthAuthServerMetadata fetches OAuth authorization server metadata from the well-known endpoint
// with exponential backoff retry logic. It returns the fetched metadata or an error if all attempts fail.
func fetchOAuthAuthServerMetadata(authServer string) (*OAuthAuthServerMetadata, error) {
	httpClient := &http.Client{Timeout: httpClientTimeout}
	wellKnownURL := strings.TrimSuffix(authServer, "/") + oauthWellKnownAuthorizationServerMetadataPath

	var metadata OAuthAuthServerMetadata

	// Configure exponential backoff.
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = maxRetryElapsedTime

	operation := func() error {
		resp, err := httpClient.Get(wellKnownURL)
		if err != nil {
			urlError, dnsError := &url.Error{}, &net.DNSError{}
			if errors.As(err, &urlError) || errors.As(err, &dnsError) {
				// These errors are highly likely configuration issues, don't retry.
				//
				// ***NOTE***: Do not delete this handling, otherwise all the test case that hitting this
				// will keep retrying until timeout, which slows the tests significantly.
				return backoff.Permanent(fmt.Errorf("failed to fetch OAuth authorization server metadata: %w", err))
			}
			return err
		}
		defer resp.Body.Close()

		// Check for successful response.
		if resp.StatusCode != http.StatusOK {
			// Retry on 5xx server errors, but not on 4xx client errors.
			if resp.StatusCode >= 500 && resp.StatusCode < 600 {
				return fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
			}
			// 4xx errors are permanent, don't retry.
			return backoff.Permanent(fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status))
		}

		// Read and parse the response body.
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			// I/O errors during reading might be transient.
			return err
		}

		if err := json.Unmarshal(body, &metadata); err != nil {
			// JSON parsing errors are permanent, don't retry.
			return backoff.Permanent(fmt.Errorf("failed to parse JSON: %w", err))
		}
		return nil
	}

	if err := backoff.Retry(operation, b); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func oauthProtectedResourceMetadataName(mcpRouteName string) string {
	return fmt.Sprintf("%s%s%s", internalapi.MCPGeneratedResourceCommonPrefix, mcpRouteName, oauthProtectedResourceMetadataSuffix)
}

func oauthAuthServerMetadataFilterName(mcpRouteName string) string {
	return fmt.Sprintf("%s%s%s", internalapi.MCPGeneratedResourceCommonPrefix, mcpRouteName, oauthAuthServerMetadataSuffix)
}

// discoverJWKSURI attempts to discover the JWKS URI from the OAuth authorization server metadata.
// It fetches the well-known metadata endpoint and extracts the jwks_uri field.
func (c *MCPRouteController) discoverJWKSURI(issuer string) (string, error) {
	// Fetch OAuth authorization server metadata.
	metadata, err := fetchOAuthAuthServerMetadata(issuer)
	switch {
	case err != nil:
		return "", fmt.Errorf("failed to fetch authorization server metadata: %w", err)
	case metadata.JwksURI == "":
		return "", fmt.Errorf("jwks_uri not found in authorization server metadata")
	default:
		return metadata.JwksURI, nil
	}
}

// tryGetBackendsForJWKS attempts to find a BackendCluster that can reach the given JWKS URL.
func (c *MCPRouteController) tryGetBackendsForJWKS(ctx context.Context, jwksURL string) ([]egv1a1.BackendRef, error) {
	var backendTLSPolicies gwapiv1a3.BackendTLSPolicyList
	if err := c.client.List(ctx, &backendTLSPolicies); err != nil {
		return nil, fmt.Errorf("failed to list BackendTLSPolicy: %w", err)
	}

	u, err := url.Parse(jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JWKS URL: %w", err)
	}
	hostname := u.Hostname()

	var backendRefs []egv1a1.BackendRef
	for i := range backendTLSPolicies.Items {
		btp := &backendTLSPolicies.Items[i]
		if string(btp.Spec.Validation.Hostname) == hostname {
			for j := range btp.Spec.TargetRefs {
				ref := btp.Spec.TargetRefs[j]
				backendRefs = append(backendRefs, egv1a1.BackendRef{
					BackendObjectReference: gwapiv1.BackendObjectReference{
						Group: &ref.Group,
						Kind:  &ref.Kind,
						Name:  ref.Name,
					},
				})
			}
		}
	}

	return backendRefs, nil
}
