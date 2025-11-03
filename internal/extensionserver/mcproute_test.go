// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	accesslogv3 "github.com/envoyproxy/go-control-plane/envoy/config/accesslog/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/go-logr/logr/testr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestServer_createBackendListener(t *testing.T) {
	tests := []struct {
		name             string
		mcpHTTPFilters   []*httpconnectionmanagerv3.HttpFilter
		accessLogConfig  []*accesslogv3.AccessLog
		expectedListener *listenerv3.Listener
	}{
		{
			name:           "no filters",
			mcpHTTPFilters: nil,
			expectedListener: &listenerv3.Listener{
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
			},
		},
		{
			name:           "no filters with access logs",
			mcpHTTPFilters: nil,
			accessLogConfig: []*accesslogv3.AccessLog{
				{Name: "accesslog1"},
				{Name: "accesslog2"},
			},
			expectedListener: &listenerv3.Listener{
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
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			listener := s.createBackendListener(tt.mcpHTTPFilters, tt.accessLogConfig)

			require.Equal(t, tt.expectedListener.Name, listener.Name)
			require.Equal(t, tt.expectedListener.Address.GetSocketAddress().Address, listener.Address.GetSocketAddress().Address)
			require.Equal(t, tt.expectedListener.Address.GetSocketAddress().GetPortValue(), listener.Address.GetSocketAddress().GetPortValue())
			require.Equal(t, tt.expectedListener.Address.GetSocketAddress().Protocol, listener.Address.GetSocketAddress().Protocol)

			hcm, _, err := findHCM(listener.FilterChains[0])
			require.NoError(t, err)
			require.Len(t, hcm.AccessLog, len(tt.accessLogConfig))
			for i := range tt.accessLogConfig {
				require.Equal(t, tt.accessLogConfig[i].Name, hcm.AccessLog[i].Name)
			}
		})
	}
}

func TestServer_createRoutesForBackendListener(t *testing.T) {
	tests := []struct {
		name          string
		routes        []*routev3.RouteConfiguration
		expectedRoute *routev3.RouteConfiguration
	}{
		{
			name:          "empty",
			routes:        []*routev3.RouteConfiguration{},
			expectedRoute: nil,
		},
		{
			name: "no MCP routes",
			routes: []*routev3.RouteConfiguration{
				{
					VirtualHosts: []*routev3.VirtualHost{
						{
							Name:   "test-vh",
							Routes: []*routev3.Route{{Name: "normal"}},
						},
					},
				},
			},
			expectedRoute: nil,
		},
		{
			name: "with MCP routes",
			routes: []*routev3.RouteConfiguration{
				{
					VirtualHosts: []*routev3.VirtualHost{
						{
							Name:   "test-vh",
							Routes: []*routev3.Route{{Name: "normal"}},
						},
					},
				},
				{
					VirtualHosts: []*routev3.VirtualHost{
						{
							Name:    "mcp-vh",
							Domains: []string{"*"},
							Routes: []*routev3.Route{
								{
									Name: internalapi.MCPPerBackendRefHTTPRoutePrefix + "foo/rule/0",
									Action: &routev3.Route_Route{
										Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{}},
									},
								},
								{
									Name: internalapi.MCPPerBackendRefHTTPRoutePrefix + "bar/rule/1",
									Action: &routev3.Route_Route{
										Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{}},
									},
								},
							},
						},
					},
				},
			},
			expectedRoute: &routev3.RouteConfiguration{
				Name: "aigateway-mcp-backend-listener-route-config",
				VirtualHosts: []*routev3.VirtualHost{
					{
						Domains: []string{"*"},
						Name:    "aigateway-mcp-backend-listener-wildcard",
						Routes: []*routev3.Route{
							{Name: internalapi.MCPPerBackendRefHTTPRoutePrefix + "foo/rule/0", Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{}},
							}},
							{Name: internalapi.MCPPerBackendRefHTTPRoutePrefix + "bar/rule/1", Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{}},
							}},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			route := s.createRoutesForBackendListener(tt.routes)
			if tt.expectedRoute == nil {
				require.Nil(t, route)
			} else {
				require.Empty(t, cmp.Diff(tt.expectedRoute, route, protocmp.Transform()))
			}
		})
	}
}

func TestServer_modifyMCPGatewayGeneratedCluster(t *testing.T) {
	tests := []struct {
		name             string
		clusters         []*clusterv3.Cluster
		expectedClusters []*clusterv3.Cluster
	}{
		{
			name: "modifies MCP cluster",
			clusters: []*clusterv3.Cluster{
				{Name: "normal-cluster"},
				{Name: internalapi.MCPMainHTTPRoutePrefix + "foo-bar/rule/0"},
			},
			expectedClusters: []*clusterv3.Cluster{
				{Name: "normal-cluster"},
				{
					Name:                 internalapi.MCPMainHTTPRoutePrefix + "foo-bar/rule/0",
					ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
					ConnectTimeout:       &durationpb.Duration{Seconds: 10},
					LoadAssignment: &endpointv3.ClusterLoadAssignment{
						ClusterName: internalapi.MCPMainHTTPRoutePrefix + "foo-bar/rule/0",
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
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			s.modifyMCPGatewayGeneratedCluster(tt.clusters)

			for i, expectedCluster := range tt.expectedClusters {
				require.Empty(t, cmp.Diff(expectedCluster, tt.clusters[i], protocmp.Transform()))
			}
		})
	}
}

func TestServer_isMCPBackendHTTPFilter(t *testing.T) {
	tests := []struct {
		name     string
		filter   *httpconnectionmanagerv3.HttpFilter
		expected bool
	}{
		{
			name:     "MCP backend filter",
			filter:   &httpconnectionmanagerv3.HttpFilter{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "test"},
			expected: true,
		},
		{
			name:     "regular filter",
			filter:   &httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.router"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			result := s.isMCPBackendHTTPFilter(tt.filter)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestServer_isMCPOAuthCustomResponseFilter(t *testing.T) {
	tests := []struct {
		name     string
		filter   *httpconnectionmanagerv3.HttpFilter
		expected bool
	}{
		{
			name:     "MCP OAuth custom response filter",
			filter:   &httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.custom_response/" + internalapi.MCPGeneratedResourceCommonPrefix + "test-oauth-protected-resource-metadata"},
			expected: true,
		},
		{
			name:     "regular custom response filter",
			filter:   &httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.custom_response/regular"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			result := s.isMCPOAuthCustomResponseFilter(tt.filter)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestServer_maybeUpdateMCPRoutes(t *testing.T) {
	emptyConfig := &anypb.Any{TypeUrl: "type.googleapis.com/google.protobuf.Empty"}

	tests := []struct {
		name           string
		routes         []*routev3.RouteConfiguration
		expectedRoutes []*routev3.RouteConfiguration
	}{
		{
			name: "removes JWT from backend routes",
			routes: []*routev3.RouteConfiguration{
				{
					VirtualHosts: []*routev3.VirtualHost{
						{
							Name: "vh",
							Routes: []*routev3.Route{
								{
									Name: internalapi.MCPMainHTTPRoutePrefix + "foo/rule/0",
									TypedPerFilterConfig: map[string]*anypb.Any{
										jwtAuthnFilterName: emptyConfig,
									},
								},
								{
									Name: internalapi.MCPMainHTTPRoutePrefix + "foo/rule/1",
									TypedPerFilterConfig: map[string]*anypb.Any{
										jwtAuthnFilterName: emptyConfig,
										"other-filter":     emptyConfig,
									},
								},
							},
						},
					},
				},
			},
			expectedRoutes: []*routev3.RouteConfiguration{
				{
					VirtualHosts: []*routev3.VirtualHost{
						{
							Name: "vh",
							Routes: []*routev3.Route{
								{
									Name: internalapi.MCPMainHTTPRoutePrefix + "foo/rule/0",
									TypedPerFilterConfig: map[string]*anypb.Any{
										jwtAuthnFilterName: emptyConfig,
									},
								},
								{
									Name: internalapi.MCPMainHTTPRoutePrefix + "foo/rule/1",
									TypedPerFilterConfig: map[string]*anypb.Any{
										"other-filter": emptyConfig,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			s.maybeUpdateMCPRoutes(tt.routes)
			require.Empty(t, cmp.Diff(tt.expectedRoutes, tt.routes, protocmp.Transform()))
		})
	}
}

func TestServer_extractMCPBackendFiltersFromMCPProxyListener(t *testing.T) {
	tests := []struct {
		name               string
		listeners          []*listenerv3.Listener
		expectedFilters    []*httpconnectionmanagerv3.HttpFilter
		expectedAccessLogs []*accesslogv3.AccessLog
	}{
		{
			name:               "no listeners",
			listeners:          []*listenerv3.Listener{},
			expectedFilters:    nil,
			expectedAccessLogs: nil,
		},
		{
			name: "listener with MCP backend filter without access logs",
			listeners: []*listenerv3.Listener{
				{
					Name: "test-listener",
					FilterChains: []*listenerv3.FilterChain{
						{
							Filters: []*listenerv3.Filter{
								{
									Name: wellknown.HTTPConnectionManager,
									ConfigType: &listenerv3.Filter_TypedConfig{
										TypedConfig: mustToAny(&httpconnectionmanagerv3.HttpConnectionManager{
											StatPrefix: "http",
											HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
												{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "test-filter"},
												{Name: "envoy.filters.http.router"},
											},
										}),
									},
								},
							},
						},
					},
				},
			},
			expectedFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "test-filter"},
			},
		},
		{
			name: "listener with MCP backend filter with access logs",
			listeners: []*listenerv3.Listener{
				{
					Name: "test-listener1",
					FilterChains: []*listenerv3.FilterChain{
						{
							Filters: []*listenerv3.Filter{
								{
									Name: wellknown.HTTPConnectionManager,
									ConfigType: &listenerv3.Filter_TypedConfig{
										TypedConfig: mustToAny(&httpconnectionmanagerv3.HttpConnectionManager{
											StatPrefix: "http",
											HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
												{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "test-filter"},
												{Name: "envoy.filters.http.router"},
											},
										}),
									},
								},
							},
						},
					},
				},
				{
					Name: "test-listener2",
					FilterChains: []*listenerv3.FilterChain{
						{
							Filters: []*listenerv3.Filter{
								{
									Name: wellknown.HTTPConnectionManager,
									ConfigType: &listenerv3.Filter_TypedConfig{
										TypedConfig: mustToAny(&httpconnectionmanagerv3.HttpConnectionManager{
											StatPrefix: "http",
											HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
												{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "test-filter2"},
												{Name: "envoy.filters.http.router"},
											},
											AccessLog: []*accesslogv3.AccessLog{
												{Name: "listener2-accesslog1"},
												{Name: "listener2-accesslog2"},
											},
										}),
									},
								},
							},
						},
					},
				},
				{
					Name: "test-listener3",
					FilterChains: []*listenerv3.FilterChain{
						{
							Filters: []*listenerv3.Filter{
								{
									Name: wellknown.HTTPConnectionManager,
									ConfigType: &listenerv3.Filter_TypedConfig{
										TypedConfig: mustToAny(&httpconnectionmanagerv3.HttpConnectionManager{
											StatPrefix: "http",
											HttpFilters: []*httpconnectionmanagerv3.HttpFilter{
												{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "test-filter3"},
												{Name: "envoy.filters.http.router"},
											},
											AccessLog: []*accesslogv3.AccessLog{
												{Name: "listener3-accesslog1"},
												{Name: "listener3-accesslog2"},
											},
										}),
									},
								},
							},
						},
					},
				},
			},
			expectedFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "test-filter"},
				{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "test-filter2"},
				{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "test-filter3"},
			},
			expectedAccessLogs: []*accesslogv3.AccessLog{
				{Name: "listener3-accesslog1"},
				{Name: "listener3-accesslog2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			filters, accessLogConfigs := s.extractMCPBackendFiltersFromMCPProxyListener(tt.listeners)
			require.Empty(t, cmp.Diff(tt.expectedFilters, filters, protocmp.Transform()))
			require.Empty(t, cmp.Diff(tt.expectedAccessLogs, accessLogConfigs, protocmp.Transform()))
		})
	}
}

func TestServer_modifyMCPOAuthCustomResponseRoute(t *testing.T) {
	tests := []struct {
		name           string
		routes         []*routev3.RouteConfiguration
		expectedRoutes []*routev3.RouteConfiguration
	}{
		{
			name: "adds CORS headers to OAuth route",
			routes: []*routev3.RouteConfiguration{
				{
					VirtualHosts: []*routev3.VirtualHost{
						{
							Routes: []*routev3.Route{
								{
									Name: "aigateway-mcp-oauth-custom-response-route",
									Action: &routev3.Route_DirectResponse{
										DirectResponse: &routev3.DirectResponseAction{Status: 200},
									},
									Match: &routev3.RouteMatch{
										PathSpecifier: &routev3.RouteMatch_Path{
											Path: "/.well-known/oauth-protected-resource/mcp",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedRoutes: []*routev3.RouteConfiguration{
				{
					VirtualHosts: []*routev3.VirtualHost{
						{
							Routes: []*routev3.Route{
								{
									Name: "aigateway-mcp-oauth-custom-response-route",
									Action: &routev3.Route_DirectResponse{
										DirectResponse: &routev3.DirectResponseAction{Status: 200},
									},
									Match: &routev3.RouteMatch{
										PathSpecifier: &routev3.RouteMatch_Path{
											Path: "/.well-known/oauth-protected-resource/mcp",
										},
									},
									ResponseHeadersToAdd: []*corev3.HeaderValueOption{
										{
											Header: &corev3.HeaderValue{
												Key:   "Access-Control-Allow-Origin",
												Value: "*",
											},
										},
										{
											Header: &corev3.HeaderValue{
												Key:   "Access-Control-Allow-Methods",
												Value: "GET",
											},
										},
										{
											Header: &corev3.HeaderValue{
												Key:   "Access-Control-Allow-Headers",
												Value: "mcp-protocol-version",
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			s.modifyMCPOAuthCustomResponseRoute(tt.routes)
			require.Empty(t, cmp.Diff(tt.expectedRoutes, tt.routes, protocmp.Transform()))
		})
	}
}

func TestIsWellKnownOAuthPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "oauth protected resource",
			path:     "/.well-known/oauth-protected-resource",
			expected: true,
		},
		{
			name:     "oauth authorization server",
			path:     "/.well-known/oauth-authorization-server",
			expected: true,
		},
		{
			name:     "oauth protected resource with suffix",
			path:     "/.well-known/oauth-protected-resource/mcp",
			expected: true,
		},
		{
			name:     "normal path",
			path:     "/api/v1/resource",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isWellKnownOAuthPath(tt.path)
			require.Equal(t, tt.expected, result)
		})
	}
}
