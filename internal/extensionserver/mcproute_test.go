// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	egextension "github.com/envoyproxy/gateway/proto/extension"
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
			listener, err := s.createBackendListener(tt.mcpHTTPFilters, tt.accessLogConfig)
			require.NoError(t, err)

			require.Equal(t, tt.expectedListener.Name, listener.Name)
			require.Equal(t, tt.expectedListener.Address.GetSocketAddress().Address, listener.Address.GetSocketAddress().Address)
			require.Equal(t, tt.expectedListener.Address.GetSocketAddress().GetPortValue(), listener.Address.GetSocketAddress().GetPortValue())
			require.Equal(t, tt.expectedListener.Address.GetSocketAddress().Protocol, listener.Address.GetSocketAddress().Protocol)

			hcm, _, err := findHCM(listener.FilterChains[0])
			require.NoError(t, err)
			require.True(t, hcm.GetSchemeHeaderTransformation().GetMatchUpstream(), "SchemeHeaderTransformation.MatchUpstream should be true")
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

// mcpProxyLocalhostCluster is the STATIC localhost cluster the MCP proxy cluster is rewritten into.
func mcpProxyLocalhostCluster(clusterName string) *clusterv3.Cluster {
	return &clusterv3.Cluster{
		Name:                 clusterName,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
		ConnectTimeout:       &durationpb.Duration{Seconds: 10},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: clusterName,
			Endpoints: []*endpointv3.LocalityLbEndpoints{
				{
					LbEndpoints: []*endpointv3.LbEndpoint{
						{
							HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
								Endpoint: &endpointv3.Endpoint{
									Address: &corev3.Address{
										Address: &corev3.Address_SocketAddress{
											SocketAddress: &corev3.SocketAddress{
												Address:       "127.0.0.1",
												PortSpecifier: &corev3.SocketAddress_PortValue{PortValue: internalapi.MCPProxyPort},
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

// edsCluster mirrors how Envoy Gateway renders a static-IP Backend: an EDS cluster whose endpoints are
// delivered out-of-band, so it has no inline LoadAssignment at PostTranslateModify time. This is the
// shape the MCP proxy cluster actually has, so the rewrite must not depend on inline endpoints.
func edsCluster(clusterName string) *clusterv3.Cluster {
	return &clusterv3.Cluster{
		Name:                 clusterName,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_EDS},
		EdsClusterConfig:     &clusterv3.Cluster_EdsClusterConfig{ServiceName: clusterName},
	}
}

// mcpMainProxyRouteConfig builds a RouteConfiguration containing the MCP proxy front-end route (rule/0
// of the generated MCP main HTTPRoute) whose action targets clusterName.
func mcpMainProxyRouteConfig(clusterName string) *routev3.RouteConfiguration {
	return &routev3.RouteConfiguration{
		VirtualHosts: []*routev3.VirtualHost{
			{
				Routes: []*routev3.Route{
					{
						Name: "httproute/default/" + internalapi.MCPMainHTTPRoutePrefix + "myroute/rule/0/match/0",
						Action: &routev3.Route_Route{
							Route: &routev3.RouteAction{
								ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: clusterName},
							},
						},
					},
				},
			},
		},
	}
}

func TestServer_modifyMCPGatewayGeneratedCluster(t *testing.T) {
	const (
		legacyProxyCluster = "httproute/default/ai-eg-mcp-main-myroute/rule/0"
		mergedProxyCluster = "backend/Backend/default/default-myroute-mcp-proxy"
	)
	tests := []struct {
		name             string
		routes           []*routev3.RouteConfiguration
		clusters         []*clusterv3.Cluster
		expectedClusters []*clusterv3.Cluster
	}{
		{
			name:   "modifies legacy MCP proxy cluster",
			routes: []*routev3.RouteConfiguration{mcpMainProxyRouteConfig(legacyProxyCluster)},
			clusters: []*clusterv3.Cluster{
				{Name: "normal-cluster"},
				edsCluster(legacyProxyCluster),
			},
			expectedClusters: []*clusterv3.Cluster{
				{Name: "normal-cluster"},
				mcpProxyLocalhostCluster(legacyProxyCluster),
			},
		},
		{
			// A MergeBackends-collapsed MCP proxy cluster has a backend-keyed name and is an EDS cluster
			// with no inline endpoints. It is resolved via the rule/0 route's cluster reference.
			name:   "modifies merged MCP proxy cluster (MergeBackends renamed, EDS)",
			routes: []*routev3.RouteConfiguration{mcpMainProxyRouteConfig(mergedProxyCluster)},
			clusters: []*clusterv3.Cluster{
				{Name: "normal-cluster"},
				edsCluster(mergedProxyCluster),
			},
			expectedClusters: []*clusterv3.Cluster{
				{Name: "normal-cluster"},
				mcpProxyLocalhostCluster(mergedProxyCluster),
			},
		},
		{
			// No MCP proxy front-end route present → nothing is rewritten.
			name:   "leaves clusters untouched when no MCP proxy route present",
			routes: nil,
			clusters: []*clusterv3.Cluster{
				edsCluster(mergedProxyCluster),
			},
			expectedClusters: []*clusterv3.Cluster{
				edsCluster(mergedProxyCluster),
			},
		},
		{
			// A cluster not referenced by the MCP proxy route must be left untouched, even under merge.
			name:   "leaves clusters not referenced by the MCP proxy route untouched",
			routes: []*routev3.RouteConfiguration{mcpMainProxyRouteConfig(mergedProxyCluster)},
			clusters: []*clusterv3.Cluster{
				edsCluster("backend/Backend/default/some-other-backend"),
			},
			expectedClusters: []*clusterv3.Cluster{
				edsCluster("backend/Backend/default/some-other-backend"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			s.modifyMCPGatewayGeneratedCluster(tt.clusters, tt.routes)

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
										filterNameJWTAuthn:   emptyConfig,
										filterNameAPIKeyAuth: emptyConfig,
										filterNameExtAuth:    emptyConfig,
									},
								},
								{
									Name: internalapi.MCPMainHTTPRoutePrefix + "foo/rule/1",
									TypedPerFilterConfig: map[string]*anypb.Any{
										filterNameJWTAuthn:   emptyConfig,
										filterNameAPIKeyAuth: emptyConfig,
										filterNameExtAuth:    emptyConfig,
										"other-filter":       emptyConfig,
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
										filterNameJWTAuthn:   emptyConfig,
										filterNameAPIKeyAuth: emptyConfig,
										filterNameExtAuth:    emptyConfig,
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
										TypedConfig: mustToAny(t, &httpconnectionmanagerv3.HttpConnectionManager{
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
										TypedConfig: mustToAny(t, &httpconnectionmanagerv3.HttpConnectionManager{
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
										TypedConfig: mustToAny(t, &httpconnectionmanagerv3.HttpConnectionManager{
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
										TypedConfig: mustToAny(t, &httpconnectionmanagerv3.HttpConnectionManager{
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
			filters, accessLogConfigs, err := s.extractMCPBackendFiltersFromMCPProxyListener(tt.listeners)
			require.NoError(t, err)
			require.Empty(t, cmp.Diff(tt.expectedFilters, filters, protocmp.Transform()))
			require.Empty(t, cmp.Diff(tt.expectedAccessLogs, accessLogConfigs, protocmp.Transform()))
		})
	}
}

func TestServer_maybeGenerateResourcesForMCPGateway(t *testing.T) {
	tests := []struct {
		name          string
		req           *egextension.PostTranslateModifyRequest
		check         func(t *testing.T, req *egextension.PostTranslateModifyRequest)
		expectedError bool
	}{
		{
			name: "no listeners or routes",
			req: &egextension.PostTranslateModifyRequest{
				Listeners: []*listenerv3.Listener{},
				Routes:    []*routev3.RouteConfiguration{},
			},
			check: func(t *testing.T, req *egextension.PostTranslateModifyRequest) {
				require.Empty(t, req.Listeners)
				require.Empty(t, req.Routes)
			},
		},
		{
			name: "with MCP routes and listeners",
			req: &egextension.PostTranslateModifyRequest{
				Listeners: []*listenerv3.Listener{
					{
						Name: "test-listener",
						FilterChains: []*listenerv3.FilterChain{
							{
								Filters: []*listenerv3.Filter{
									{
										Name: wellknown.HTTPConnectionManager,
										ConfigType: &listenerv3.Filter_TypedConfig{
											TypedConfig: mustToAny(t, &httpconnectionmanagerv3.HttpConnectionManager{
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
				Routes: []*routev3.RouteConfiguration{
					{
						VirtualHosts: []*routev3.VirtualHost{
							{
								Name:    "mcp-vh",
								Domains: []string{"*"},
								Routes: []*routev3.Route{
									{
										// MCP proxy front-end route (rule/0 of the main HTTPRoute): its
										// cluster reference is what identifies the cluster to rewrite.
										Name: "httproute/default/" + internalapi.MCPMainHTTPRoutePrefix + "foo-bar/rule/0/match/0",
										Action: &routev3.Route_Route{
											Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{
												Cluster: internalapi.MCPMainHTTPRoutePrefix + "foo-bar/rule/0",
											}},
										},
									},
									{
										Name: internalapi.MCPPerBackendRefHTTPRoutePrefix + "foo/rule/0",
										Action: &routev3.Route_Route{
											Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{}},
										},
									},
								},
							},
						},
					},
				},
				Clusters: []*clusterv3.Cluster{
					{Name: internalapi.MCPMainHTTPRoutePrefix + "foo-bar/rule/0"},
				},
			},
			check: func(t *testing.T, req *egextension.PostTranslateModifyRequest) {
				require.Len(t, req.Listeners, 2)
				require.Equal(t, mcpBackendListenerName, req.Listeners[1].Name)

				require.Len(t, req.Routes, 2)
				require.Equal(t, "aigateway-mcp-backend-listener-route-config", req.Routes[1].Name)

				require.Len(t, req.Clusters, 1)
				require.Equal(t, internalapi.MCPMainHTTPRoutePrefix+"foo-bar/rule/0", req.Clusters[0].Name)
				require.Equal(t, clusterv3.Cluster_STATIC, req.Clusters[0].GetClusterDiscoveryType().(*clusterv3.Cluster_Type).Type)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: testr.New(t)}
			err := s.maybeGenerateResourcesForMCPGateway(tt.req)
			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				tt.check(t, tt.req)
			}
		})
	}
}

// TestPostTranslateModify_MCPProxyMergeBackends drives the full PostTranslateModify entry point with a
// MergeBackends-collapsed MCP proxy cluster: a backend-keyed EDS cluster
// ("backend/Backend/<ns>/<name>-mcp-proxy") with no inline endpoints. The proxy cluster, identified via
// the rule/0 route, is rewritten to localhost:MCPProxyPort so the MCP proxy path reaches the in-pod proxy.
func TestPostTranslateModify_MCPProxyMergeBackends(t *testing.T) {
	s, err := New(newFakeClient(), testr.New(t), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)

	const mergedProxyCluster = "backend/Backend/default/default-myroute-mcp-proxy"

	hcm := &httpconnectionmanagerv3.HttpConnectionManager{
		StatPrefix:  "http",
		HttpFilters: []*httpconnectionmanagerv3.HttpFilter{{Name: wellknown.Router}},
	}
	listener := &listenerv3.Listener{
		Name: "test-listener",
		FilterChains: []*listenerv3.FilterChain{
			{
				Filters: []*listenerv3.Filter{
					{Name: wellknown.HTTPConnectionManager, ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)}},
				},
			},
		},
	}
	// The MCP main HTTPRoute's rule/0 (proxy) route. Its cluster reference points at the collapsed
	// backend-keyed cluster; MergeBackends does not rename the route itself.
	routeConfig := &routev3.RouteConfiguration{
		Name: "test-route-config",
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:    "mcp-vh",
				Domains: []string{"*"},
				Routes: []*routev3.Route{
					{
						Name: "httproute/default/" + internalapi.MCPMainHTTPRoutePrefix + "myroute/rule/0/match/0",
						Action: &routev3.Route_Route{Route: &routev3.RouteAction{
							ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: mergedProxyCluster},
						}},
					},
				},
			},
		},
	}
	req := &egextension.PostTranslateModifyRequest{
		Listeners: []*listenerv3.Listener{listener},
		Routes:    []*routev3.RouteConfiguration{routeConfig},
		// As EG renders a static-IP Backend under MergeBackends: an EDS cluster with no inline endpoints.
		Clusters: []*clusterv3.Cluster{edsCluster(mergedProxyCluster)},
	}

	resp, err := s.PostTranslateModify(t.Context(), req)
	require.NoError(t, err)

	var proxy *clusterv3.Cluster
	for _, c := range resp.Clusters {
		if c.Name == mergedProxyCluster {
			proxy = c
		}
	}
	require.NotNil(t, proxy, "MCP proxy cluster must be present in the response")
	require.Equal(t, clusterv3.Cluster_STATIC, proxy.GetClusterDiscoveryType().(*clusterv3.Cluster_Type).Type,
		"MCP proxy cluster must be rewritten from EDS to a STATIC localhost cluster")
	require.Nil(t, proxy.EdsClusterConfig, "EDS config must be dropped after the rewrite")
	sa := proxy.GetLoadAssignment().GetEndpoints()[0].GetLbEndpoints()[0].GetEndpoint().GetAddress().GetSocketAddress()
	require.Equal(t, "127.0.0.1", sa.GetAddress(), "must dial localhost, not the 192.0.2.42 placeholder")
	require.Equal(t, uint32(internalapi.MCPProxyPort), sa.GetPortValue())
}
