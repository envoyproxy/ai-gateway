// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"fmt"
	"os"
	"strings"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	accesslogv3 "github.com/envoyproxy/go-control-plane/envoy/config/accesslog/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	custom_responsev3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/custom_response/v3"
	htomv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/header_to_metadata/v3"
	routerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	local_response_policyv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/http/custom_response/local_response_policy/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	mcpBackendListenerName = "aigateway-mcp-backend-listener"
	filterNameJWTAuthn     = "envoy.filters.http.jwt_authn"
	filterNameAPIKeyAuth   = "envoy.filters.http.api_key_auth" // #nosec G101
	filterNameExtAuth      = "envoy.filters.http.ext_authz"
)

// Generate the resources needed to support MCP Gateway functionality.
func (s *Server) maybeGenerateResourcesForMCPGateway(req *egextension.PostTranslateModifyRequest) {
	if len(req.Listeners) == 0 || len(req.Routes) == 0 {
		return // Nothing to do, mostly for unit tests.
	}
	// Update existing MCP routes to remove JWT authn filter from non-proxy rules.
	// Order matters: do this before moving rules to the backend listener.
	s.maybeUpdateMCPRoutes(req.Routes)

	// Create routes for the backend listener first to determine if MCP processing is needed
	mcpBackendRoutes := s.createRoutesForBackendListener(req.Routes)

	// Only create the backend listener if there are routes for it
	if mcpBackendRoutes != nil {
		// Extract MCP backend filters from existing listeners and create the backend listener with those filters.
		mcpBackendHTTPFilters, accessLogConfig := s.extractMCPBackendFiltersFromMCPProxyListener(req.Listeners)
		req.Listeners = append(req.Listeners, s.createBackendListener(mcpBackendHTTPFilters, accessLogConfig))
		req.Routes = append(req.Routes, mcpBackendRoutes)
	}

	// Modify routes with mcp-gateway-generated annotation to use mcpproxy-cluster.
	s.modifyMCPGatewayGeneratedCluster(req.Clusters)

	// Modify OAuth custom response filters to add WWW-Authenticate headers.
	// TODO: remove this step once Envoy Gateway supports this natively in the BackendTrafficPolicy ResponseOverride.
	// https://github.com/envoyproxy/gateway/pull/6308
	s.modifyMCPOAuthCustomResponseFilters(req.Listeners)

	// TODO: remove this step once Envoy Gateway supports this natively in the BackendTrafficPolicy ResponseOverride.
	// https://github.com/envoyproxy/gateway/pull/6308
	s.modifyMCPOAuthCustomResponseRoute(req.Routes)
}

// createBackendListener creates the backend listener for MCP Gateway.
func (s *Server) createBackendListener(mcpHTTPFilters []*httpconnectionmanagerv3.HttpFilter, accessLogConfig []*accesslogv3.AccessLog) *listenerv3.Listener {
	httpConManager := &httpconnectionmanagerv3.HttpConnectionManager{
		StatPrefix: fmt.Sprintf("%s-http", mcpBackendListenerName),
		AccessLog:  accessLogConfig,
		RouteSpecifier: &httpconnectionmanagerv3.HttpConnectionManager_Rds{
			Rds: &httpconnectionmanagerv3.Rds{
				RouteConfigName: fmt.Sprintf("%s-route-config", mcpBackendListenerName),
				ConfigSource: &corev3.ConfigSource{
					ConfigSourceSpecifier: &corev3.ConfigSource_Ads{
						Ads: &corev3.AggregatedConfigSource{},
					},
					ResourceApiVersion: corev3.ApiVersion_V3,
				},
			},
		},
	}

	// Add MCP HTTP filters (like credential injection filters) to the backend listener.
	for _, filter := range mcpHTTPFilters {
		s.log.Info("Adding MCP HTTP filter to backend listener", "filterName", filter.Name)
		httpConManager.HttpFilters = append(httpConManager.HttpFilters, filter)
	}

	// Add the header-to-metadata filter to populate MCP metadata so that it can be accessed in the access logs.
	// The MCP Proxy will add these headers to the request (because it does not have direct access to the filter metadata).
	// Here we configure the header-to-metadata filter to extract those headers, populate the filter metadata, and clean
	// the headers up from the request before sending it upstream.
	headersToMetadata := &htomv3.Config{}
	for h, m := range internalapi.MCPInternalHeadersToMetadata {
		headersToMetadata.RequestRules = append(headersToMetadata.RequestRules,
			&htomv3.Config_Rule{
				Header: h,
				OnHeaderPresent: &htomv3.Config_KeyValuePair{
					MetadataNamespace: aigv1a1.AIGatewayFilterMetadataNamespace,
					Key:               m,
					Type:              htomv3.Config_STRING,
				},
				// If the header was an internal MCP header, we remove it before sending the request upstream.
				Remove: strings.HasPrefix(h, internalapi.MCPMetadataHeaderPrefix),
			},
		)
	}
	httpConManager.HttpFilters = append(httpConManager.HttpFilters, &httpconnectionmanagerv3.HttpFilter{
		Name: "envoy.filters.http.header_to_metadata",
		ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{
			TypedConfig: mustToAny(headersToMetadata),
		},
	})

	// Add Router filter as the terminal HTTP filter.
	httpConManager.HttpFilters = append(httpConManager.HttpFilters, &httpconnectionmanagerv3.HttpFilter{
		Name:       wellknown.Router,
		ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{TypedConfig: mustToAny(&routerv3.Router{})},
	})

	return &listenerv3.Listener{
		Name: mcpBackendListenerName,
		Address: &corev3.Address{
			Address: &corev3.Address_SocketAddress{
				SocketAddress: &corev3.SocketAddress{
					Protocol: corev3.SocketAddress_TCP,
					Address:  "127.0.0.1",
					PortSpecifier: &corev3.SocketAddress_PortValue{
						PortValue: internalapi.MCPBackendListenerPort,
					},
				},
			},
		},
		FilterChains: []*listenerv3.FilterChain{
			{
				Filters: []*listenerv3.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{
							TypedConfig: mustToAny(httpConManager),
						},
					},
				},
			},
		},
	}
}

// maybeUpdateMCPRoutes updates the mcp routes with necessary changes for MCP Gateway.
func (s *Server) maybeUpdateMCPRoutes(routes []*routev3.RouteConfiguration) {
	for _, routeConfig := range routes {
		for _, vh := range routeConfig.VirtualHosts {
			for _, route := range vh.Routes {
				if strings.Contains(route.Name, internalapi.MCPMainHTTPRoutePrefix) {
					// Skip the frontend mcp proxy route(rule/0).
					if strings.Contains(route.Name, "rule/0") {
						continue
					}
					// Remove the authn filters from the well-known and backend routes.
					// TODO: remove this step once the SecurityPolicy can target the MCP proxy route rule only.
					for _, filterName := range []string{filterNameJWTAuthn, filterNameAPIKeyAuth, filterNameExtAuth} {
						if _, ok := route.TypedPerFilterConfig[filterName]; ok {
							s.log.Info("removing authn filter from well-known and backend routes", "route", route.Name, "filter", filterName)
							delete(route.TypedPerFilterConfig, filterName)
						}
					}
				}
			}
		}
	}
}

// createRoutesForBackendListener creates routes for the backend listener.
// The HCM of the backend listener will have all the per-backendRef HTTP routes.
//
// Returns nil if no MCP routes are found.
func (s *Server) createRoutesForBackendListener(routes []*routev3.RouteConfiguration) *routev3.RouteConfiguration {
	var backendListenerRoutes []*routev3.Route
	for _, routeConfig := range routes {
		for _, vh := range routeConfig.VirtualHosts {
			var originalRoutes []*routev3.Route
			for _, route := range vh.Routes {
				if strings.Contains(route.Name, internalapi.MCPPerBackendRefHTTPRoutePrefix) {
					s.log.Info("found MCP route, processing for backend listener", "route", route.Name)
					// Copy the route and modify it to use the backend header and mcpproxy-cluster.
					marshaled, err := proto.Marshal(route)
					if err != nil {
						s.log.Error(err, "failed to marshal route for backend MCP listener", "route", route)
						continue
					}
					copiedRoute := &routev3.Route{}
					if err := proto.Unmarshal(marshaled, copiedRoute); err != nil {
						s.log.Error(err, "failed to unmarshal route for backend MCP listener", "route", route)
						continue
					}
					if routeAction := route.GetRoute(); routeAction != nil {
						if _, ok := routeAction.ClusterSpecifier.(*routev3.RouteAction_Cluster); ok {
							backendListenerRoutes = append(backendListenerRoutes, copiedRoute)
							continue
						}
					}
				}
				originalRoutes = append(originalRoutes, route)
			}
			vh.Routes = originalRoutes
		}
	}
	if len(backendListenerRoutes) == 0 {
		return nil
	}

	s.log.Info("created routes for MCP backend listener", "numRoutes", len(backendListenerRoutes))
	mcpRouteConfig := &routev3.RouteConfiguration{
		Name: fmt.Sprintf("%s-route-config", mcpBackendListenerName),
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:    fmt.Sprintf("%s-wildcard", mcpBackendListenerName),
				Domains: []string{"*"},
				Routes:  backendListenerRoutes,
			},
		},
	}
	return mcpRouteConfig
}

// modifyMCPGatewayGeneratedRoutes finds the mcp proxy dummy IP in the clusters and
// swaps it to the localhost.
func (s *Server) modifyMCPGatewayGeneratedCluster(clusters []*clusterv3.Cluster) {
	for _, c := range clusters {
		if strings.Contains(c.Name, internalapi.MCPMainHTTPRoutePrefix) && strings.HasSuffix(c.Name, "/rule/0") {
			name := c.Name
			*c = clusterv3.Cluster{
				Name:                 name,
				ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
				ConnectTimeout:       &durationpb.Duration{Seconds: 10},
				LoadAssignment: &endpointv3.ClusterLoadAssignment{
					ClusterName: name,
					Endpoints: []*endpointv3.LocalityLbEndpoints{
						{
							LbEndpoints: []*endpointv3.LbEndpoint{
								{
									HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
										Endpoint: &endpointv3.Endpoint{
											Address: &corev3.Address{
												Address: &corev3.Address_SocketAddress{
													SocketAddress: &corev3.SocketAddress{
														Address: "127.0.0.1",
														PortSpecifier: &corev3.SocketAddress_PortValue{
															PortValue: internalapi.MCPProxyPort,
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}
		}
	}
}

// extractMCPBackendFiltersFromMCPProxyListener scans through MCP proxy listeners to find HTTP filters
// that correspond to MCP backend processing (those with MCPBackendFilterPrefix in their names)
// and extracts them from the proxy listeners so they can be moved to the backend listener.
//
// This method also returns the access log configuration to use in the MCP backend listener. We want to use the same
// access log configuration that has been configured in the Gateway.
// The challenge is that the MCP backend listener will have a single HCM, and here we have N listeners, each with its own
// HCM, so we need to decide how to properly configure the access logs in the backend listener based on multiple input
// access log configurations.
//
// The Envoy Gateway extension server works, it will call the main `PostTranslateModify` individually for each gateway. This means
// that this method will receive ONLY listeners for the same gateway.
// Since the access logs are configured in the EnvoyProxy resource, and the Gateway object targets the EnvoyProxy resource via the
// "infrastructure" setting, it is guaranteed that all listeners here will have the same access log configuration, so it is safe to
// just pick the first one.
//
// When using the envoy Gateway `mergeGateways` feature, this method will receive all the listeners attached to the GatewayClass instead.
// This is still safe because in the end all Gateway objects will be attached to the same "infrastructure", so it is still safe to assume
// that all received listeners will have the same access log configuration
func (s *Server) extractMCPBackendFiltersFromMCPProxyListener(listeners []*listenerv3.Listener) ([]*httpconnectionmanagerv3.HttpFilter, []*accesslogv3.AccessLog) {
	var (
		mcpHTTPFilters  []*httpconnectionmanagerv3.HttpFilter
		accessLogConfig []*accesslogv3.AccessLog
	)

	for _, listener := range listeners {
		// Skip the backend MCP listener if it already exists.
		if listener.Name == mcpBackendListenerName {
			continue
		}

		// Get filter chains from the listener.
		filterChains := listener.GetFilterChains()
		defaultFC := listener.DefaultFilterChain
		if defaultFC != nil {
			filterChains = append(filterChains, defaultFC)
		}

		// Go through all filter chains to find HTTP Connection Managers.
		for _, chain := range filterChains {
			httpConManager, hcmIndex, err := findHCM(chain)
			if err != nil {
				continue // Skip chains without HCM.
			}

			// All listeners will have the same access log configuration, as they all belong to the same gateway
			// and share the infrastructure. We can just return any not-empty access log config and use that
			// to configure the MCP backend listener with the same settings.
			accessLogConfig = httpConManager.AccessLog

			// Look for MCP HTTP filters in this HCM and extract them.
			var remainingFilters []*httpconnectionmanagerv3.HttpFilter
			for _, filter := range httpConManager.HttpFilters {
				if s.isMCPBackendHTTPFilter(filter) {
					s.log.Info("Found MCP HTTP filter, extracting from original listener", "filterName", filter.Name, "listener", listener.Name)
					mcpHTTPFilters = append(mcpHTTPFilters, filter)
				} else {
					remainingFilters = append(remainingFilters, filter)
				}
			}

			// Update the HCM with remaining filters (MCP filters removed).
			if len(remainingFilters) != len(httpConManager.HttpFilters) {
				httpConManager.HttpFilters = remainingFilters

				// Write the updated HCM back to the filter chain.
				chain.Filters[hcmIndex].ConfigType = &listenerv3.Filter_TypedConfig{
					TypedConfig: mustToAny(httpConManager),
				}
			}
		}
	}

	if len(mcpHTTPFilters) > 0 {
		s.log.Info("Extracted MCP HTTP filters", "count", len(mcpHTTPFilters))
	}
	return mcpHTTPFilters, accessLogConfig
}

// isMCPBackendHTTPFilter checks if an HTTP filter is used for MCP backend processing.
func (s *Server) isMCPBackendHTTPFilter(filter *httpconnectionmanagerv3.HttpFilter) bool {
	// Check if the filter name contains the MCP prefix
	// MCP HTTPRouteFilters are typically named with the MCPPerBackendHTTPRouteFilterPrefix.
	if strings.Contains(filter.Name, internalapi.MCPPerBackendHTTPRouteFilterPrefix) {
		return true
	}

	return false
}

// isMCPOAuthCustomResponseFilter checks if an HTTP filter is a CustomResponse filter
// that handles MCP OAuth resources (contains both MCPHTTPRoutePrefix and oauthProtectedResourceMetadataSuffix).
func (s *Server) isMCPOAuthCustomResponseFilter(filter *httpconnectionmanagerv3.HttpFilter) bool {
	return strings.HasPrefix(filter.Name, "envoy.filters.http.custom_response/") &&
		strings.Contains(filter.Name, internalapi.MCPGeneratedResourceCommonPrefix) &&
		strings.HasSuffix(filter.Name, "-oauth-protected-resource-metadata")
}

// modifyMCPOAuthCustomResponseFilter modifies a CustomResponse filter to add WWW-Authenticate header
// to the response_headers_to_add field in the LocalResponsePolicy.
func (s *Server) modifyMCPOAuthCustomResponseFilter(filter *httpconnectionmanagerv3.HttpFilter) error {
	// Unmarshal the CustomResponse configuration.
	if filter.ConfigType == nil {
		return fmt.Errorf("CustomResponse filter has no configuration")
	}

	typedConfig, ok := filter.ConfigType.(*httpconnectionmanagerv3.HttpFilter_TypedConfig)
	if !ok {
		return fmt.Errorf("CustomResponse filter configuration is not a TypedConfig")
	}

	var customResponse custom_responsev3.CustomResponse
	if err := typedConfig.TypedConfig.UnmarshalTo(&customResponse); err != nil {
		return fmt.Errorf("failed to unmarshal CustomResponse configuration: %w", err)
	}

	// Navigate to the LocalResponsePolicy within the matcher.
	if customResponse.CustomResponseMatcher == nil {
		return fmt.Errorf("CustomResponse filter has no matcher")
	}

	matcherList := customResponse.CustomResponseMatcher.GetMatcherList()
	if matcherList == nil || len(matcherList.Matchers) == 0 {
		return fmt.Errorf("CustomResponse filter has no matchers")
	}

	for _, matcher := range matcherList.Matchers {
		if matcher.OnMatch == nil {
			continue
		}

		action := matcher.OnMatch.GetAction()
		if action == nil {
			continue
		}

		// Check if this is a LocalResponsePolicy.
		var localResponsePolicy local_response_policyv3.LocalResponsePolicy
		if err := action.TypedConfig.UnmarshalTo(&localResponsePolicy); err != nil {
			s.log.Info("Skipping non-LocalResponsePolicy action", "error", err.Error())
			continue
		}

		// Extract WWW-Authenticate header value from the existing body.
		// The current implementation stores the header value in the body field.
		wwwAuthenticateValue := ""
		if localResponsePolicy.BodyFormat != nil {
			switch bodyFormat := localResponsePolicy.BodyFormat.Format.(type) {
			case *corev3.SubstitutionFormatString_TextFormat:
				wwwAuthenticateValue = bodyFormat.TextFormat
			case *corev3.SubstitutionFormatString_TextFormatSource:
				if source := bodyFormat.TextFormatSource; source != nil {
					switch {
					case source.GetFilename() != "":
						content, err := os.ReadFile(source.GetFilename())
						if err != nil {
							s.log.Error(err, "reading WWW-Authenticate header value from CustomResponse bod")
						}
						wwwAuthenticateValue = string(content)
					case source.GetEnvironmentVariable() != "":
						wwwAuthenticateValue = os.Getenv(source.GetEnvironmentVariable())
					case source.GetInlineBytes() != nil:
						wwwAuthenticateValue = string(source.GetInlineBytes())
					case source.GetInlineString() != "":
						wwwAuthenticateValue = source.GetInlineString()
					}
				}
			}
		}

		if wwwAuthenticateValue == "" {
			s.log.Info("No WWW-Authenticate header value found in CustomResponse body")
			continue
		}

		// Add the WWW-Authenticate header to response_headers_to_add.
		wwwAuthHeader := &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:   "WWW-Authenticate",
				Value: wwwAuthenticateValue,
			},
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		}

		// Check if header already exists to avoid duplicates.
		headerExists := false
		for _, existingHeader := range localResponsePolicy.ResponseHeadersToAdd {
			if existingHeader.Header != nil && existingHeader.Header.Key == "WWW-Authenticate" {
				headerExists = true
				break
			}
		}

		if !headerExists {
			localResponsePolicy.ResponseHeadersToAdd = append(localResponsePolicy.ResponseHeadersToAdd, wwwAuthHeader)
			localResponsePolicy.BodyFormat = nil // Clear body format as it's no longer needed.
			s.log.Info("Added WWW-Authenticate header to CustomResponse filter", "filterName", filter.Name)
		}

		// Marshal the modified LocalResponsePolicy back.
		action.TypedConfig = mustToAny(&localResponsePolicy)
	}

	// Marshal the modified CustomResponse configuration back.
	typedConfig.TypedConfig = mustToAny(&customResponse)

	return nil
}

// modifyMCPOAuthCustomResponseFilters finds and modifies OAuth custom response filters
// in the original listeners to add WWW-Authenticate headers.
func (s *Server) modifyMCPOAuthCustomResponseFilters(listeners []*listenerv3.Listener) {
	for _, listener := range listeners {
		// Skip the backend MCP listener if it already exists.
		if listener.Name == mcpBackendListenerName {
			continue
		}

		// Get filter chains from the listener.
		filterChains := listener.GetFilterChains()
		defaultFC := listener.DefaultFilterChain
		if defaultFC != nil {
			filterChains = append(filterChains, defaultFC)
		}

		// Go through all filter chains to find HTTP Connection Managers.
		for _, chain := range filterChains {
			httpConManager, hcmIndex, err := findHCM(chain)
			if err != nil {
				continue // Skip chains without HCM.
			}

			// Look for OAuth custom response filters and modify them in place.
			modified := false
			for _, filter := range httpConManager.HttpFilters {
				if s.isMCPOAuthCustomResponseFilter(filter) {
					s.log.Info("Found MCP OAuth CustomResponse filter, modifying in place", "filterName", filter.Name, "listener", listener.Name)
					if err := s.modifyMCPOAuthCustomResponseFilter(filter); err != nil {
						s.log.Error(err, "failed to modify MCP OAuth CustomResponse filter", "filterName", filter.Name)
					} else {
						modified = true
					}
				}
			}

			// If we modified any filters, update the HCM in the filter chain.
			if modified {
				chain.Filters[hcmIndex].ConfigType = &listenerv3.Filter_TypedConfig{
					TypedConfig: mustToAny(httpConManager),
				}
			}
		}
	}
}

func (s *Server) modifyMCPOAuthCustomResponseRoute(routes []*routev3.RouteConfiguration) {
	for _, r := range routes {
		if r == nil {
			continue
		}

		for _, vh := range r.VirtualHosts {
			if vh == nil {
				continue
			}

			for _, route := range vh.Routes {
				if route == nil {
					continue
				}

				if route.GetDirectResponse() == nil || route.GetMatch() == nil {
					continue
				}

				path := route.GetMatch().GetPath()
				if isWellKnownOAuthPath(path) {
					s.log.V(6).Info("Adding CORS headers to MCP OAuth route", "routeName", route.Name, "path", path)
					// add CORS headers.
					// CORS filter won't work with direct response, so we add the headers directly to the route.
					// TODO: remove this step once Envoy Gateway supports this natively in the BackendTrafficPolicy ResponseOverride.
					route.ResponseHeadersToAdd = append(route.ResponseHeadersToAdd, &corev3.HeaderValueOption{
						Header: &corev3.HeaderValue{
							Key:   "Access-Control-Allow-Origin",
							Value: "*",
						},
					})
					route.ResponseHeadersToAdd = append(route.ResponseHeadersToAdd, &corev3.HeaderValueOption{
						Header: &corev3.HeaderValue{
							Key:   "Access-Control-Allow-Methods",
							Value: "GET",
						},
					})
					route.ResponseHeadersToAdd = append(route.ResponseHeadersToAdd, &corev3.HeaderValueOption{
						Header: &corev3.HeaderValue{
							Key:   "Access-Control-Allow-Headers",
							Value: "mcp-protocol-version",
						},
					})
				}
			}
		}
	}
}

const (
	oauthProtectedResourcePath   = "/.well-known/oauth-protected-resource"
	oauthAuthorizationServerPath = "/.well-known/oauth-authorization-server"
	oidcAuthorizationServerPath  = "/.well-known/openid-configuration"
)

func isWellKnownOAuthPath(path string) bool {
	return strings.Contains(path, oauthProtectedResourcePath) ||
		strings.Contains(path, oauthAuthorizationServerPath) ||
		strings.Contains(path, oidcAuthorizationServerPath)
}
