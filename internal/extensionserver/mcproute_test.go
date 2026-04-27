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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
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

			// The token-exchange dynamic module filter must always be present, just before the
			// terminal router filter, so that per-route config can activate it on demand.
			n := len(hcm.HttpFilters)
			require.GreaterOrEqual(t, n, 2, "HCM must have at least token-exchange and router filters")
			require.Equal(t, "token-exchange", hcm.HttpFilters[n-2].Name,
				"second-to-last HCM filter must be the token-exchange filter")
			require.Equal(t, wellknown.Router, hcm.HttpFilters[n-1].Name,
				"last HCM filter must be the router filter")
		})
	}
}

func TestServer_createRoutesForBackendListener(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(*testing.T, client.Client)
		routes        []*routev3.RouteConfiguration
		expectedRoute *routev3.RouteConfiguration
	}{
		{
			name:          "empty",
			routes:        []*routev3.RouteConfiguration{},
			expectedRoute: nil,
		},
		{
			name: "with MCP route and token-exchange backend sets per-route config",
			setup: func(t *testing.T, c client.Client) {
				mcpRoute := &aigv1a1.MCPRoute{
					ObjectMeta: metav1.ObjectMeta{Name: "my-route", Namespace: "default"},
					Spec: aigv1a1.MCPRouteSpec{
						BackendRefs: []aigv1a1.MCPRouteBackendRef{
							{
								BackendObjectReference: gwapiv1.BackendObjectReference{Name: "te-backend"},
								SecurityPolicy: &aigv1a1.MCPBackendSecurityPolicy{
									TokenExchange: &aigv1a1.MCPBackendTokenExchange{
										STSEndpoint: "https://sts.example.com/token",
									},
								},
							},
						},
					},
				}
				require.NoError(t, c.Create(t.Context(), mcpRoute))
			},
			routes: []*routev3.RouteConfiguration{
				{
					VirtualHosts: []*routev3.VirtualHost{
						{
							Name:    "mcp-vh",
							Domains: []string{"*"},
							Routes: []*routev3.Route{
								{
									Name: internalapi.MCPPerBackendRefHTTPRoutePrefix + "te-backend/rule/0",
									Action: &routev3.Route_Route{
										Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{}},
									},
									Match: &routev3.RouteMatch{
										Headers: []*routev3.HeaderMatcher{
											{
												Name:                 internalapi.MCPBackendHeader,
												HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{ExactMatch: "te-backend"},
											},
											{
												Name:                 internalapi.MCPRouteHeader,
												HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{ExactMatch: "default/my-route"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			// expectedRoute is nil because we check per-route config directly below.
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
			fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
			if tt.setup != nil {
				tt.setup(t, fakeClient)
			}
			s := &Server{log: testr.New(t), k8sClient: fakeClient}
			route := s.createRoutesForBackendListener(t.Context(), tt.routes)

			switch {
			case tt.name == "with MCP route and token-exchange backend sets per-route config":
				// The route is returned but we also verify the per-route config was injected.
				require.NotNil(t, route)
				backendRoute := route.GetVirtualHosts()[0].GetRoutes()[0]
				require.NotNil(t, backendRoute.TypedPerFilterConfig,
					"token-exchange per-route config must be injected for TokenExchange backends")
				require.Contains(t, backendRoute.TypedPerFilterConfig, tokenExchangeFilterName)
			case tt.expectedRoute == nil:
				require.Nil(t, route)
			default:
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
		setup         func(*testing.T, client.Client)
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
		{
			name: "creates STS cluster for token-exchange backend",
			setup: func(t *testing.T, c client.Client) {
				mcpRoute := &aigv1a1.MCPRoute{
					ObjectMeta: metav1.ObjectMeta{Name: "sts-route", Namespace: "default"},
					Spec: aigv1a1.MCPRouteSpec{
						BackendRefs: []aigv1a1.MCPRouteBackendRef{
							{
								BackendObjectReference: gwapiv1.BackendObjectReference{Name: "sts-backend"},
								SecurityPolicy: &aigv1a1.MCPBackendSecurityPolicy{
									TokenExchange: &aigv1a1.MCPBackendTokenExchange{
										STSEndpoint: "https://sts.example.com/token",
									},
								},
							},
						},
					},
				}
				require.NoError(t, c.Create(t.Context(), mcpRoute))
			},
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
													{Name: internalapi.MCPPerBackendHTTPRouteFilterPrefix + "sts-filter"},
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
										Name: internalapi.MCPPerBackendRefHTTPRoutePrefix + "sts-backend/rule/0",
										Action: &routev3.Route_Route{
											Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{}},
										},
									},
								},
							},
						},
					},
				},
			},
			check: func(t *testing.T, req *egextension.PostTranslateModifyRequest) {
				// The backend listener and routes are added.
				require.Len(t, req.Listeners, 2)
				require.Equal(t, mcpBackendListenerName, req.Listeners[1].Name)
				// An STS cluster must have been appended for the token-exchange backend.
				require.Len(t, req.Clusters, 1)
				require.Equal(t, buildSTSClusterName("https://sts.example.com/token"), req.Clusters[0].Name)
				require.Equal(t, clusterv3.Cluster_STRICT_DNS, req.Clusters[0].GetClusterDiscoveryType().(*clusterv3.Cluster_Type).Type)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
			if tt.setup != nil {
				tt.setup(t, fakeClient)
			}
			s := &Server{log: testr.New(t), k8sClient: fakeClient}
			err := s.maybeGenerateResourcesForMCPGateway(t.Context(), tt.req)
			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				tt.check(t, tt.req)
			}
		})
	}
}
