// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"strings"
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	htomv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/header_to_metadata/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestServer_createBackendListener(t *testing.T) {
	s := &Server{log: zap.New()}

	listener := s.createBackendListener(nil)

	// Verify listener is created.
	require.NotNil(t, listener, "should create a properly configured backend listener for MCP Gateway")

	// Verify listener name.
	require.Equal(t, "aigateway-mcp-backend-listener", listener.Name, "listener should have correct name")

	// Verify listener address.
	require.NotNil(t, listener.Address, "listener should have an address")
	socketAddr := listener.Address.GetSocketAddress()
	require.NotNil(t, socketAddr, "listener should have socket address")
	require.Equal(t, "127.0.0.1", socketAddr.Address, "listener should bind to localhost")
	require.Equal(t, uint32(internalapi.MCPBackendListenerPort), socketAddr.GetPortValue(), "listener should use correct port")
	require.Equal(t, corev3.SocketAddress_TCP, socketAddr.Protocol, "listener should use TCP protocol")

	// Verify filter chains.
	require.Len(t, listener.FilterChains, 1, "listener should have exactly one filter chain")
	filterChain := listener.FilterChains[0]
	require.Len(t, filterChain.Filters, 1, "filter chain should have exactly one filter")

	// Verify HTTP Connection Manager filter.
	filter := filterChain.Filters[0]
	require.Equal(t, wellknown.HTTPConnectionManager, filter.Name, "filter should be HTTP Connection Manager")
	require.NotNil(t, filter.GetTypedConfig(), "filter should have typed config")

	// Unmarshal the HCM config to verify its contents.
	var hcm httpconnectionmanagerv3.HttpConnectionManager
	err := filter.GetTypedConfig().UnmarshalTo(&hcm)
	require.NoError(t, err, "should be able to unmarshal HCM config")

	// Verify HCM configuration.
	require.Equal(t, "aigateway-mcp-backend-listener-http", hcm.StatPrefix, "HCM should have correct stat prefix")

	// Verify header-to-metadata configuration.
	headerToMetadataPresent := false
	for _, f := range hcm.GetHttpFilters() {
		if f.Name == "envoy.filters.http.header_to_metadata" {
			headerToMetadataPresent = true
			require.False(t, f.Disabled)

			var headersToMetadata htomv3.Config
			require.NoError(t, f.GetTypedConfig().UnmarshalTo(&headersToMetadata))
			require.Len(t, headersToMetadata.RequestRules, len(internalapi.MCPInternalHeadersToMetadata))
			for _, rule := range headersToMetadata.RequestRules {
				require.NotNil(t, rule.OnHeaderPresent)
				require.Nil(t, rule.OnHeaderMissing)
				require.Equal(t, strings.HasPrefix(rule.Header, internalapi.MCPMetadataHeaderPrefix), rule.Remove)
			}
		}
	}
	require.True(t, headerToMetadataPresent, "header to metadata filter should be present")

	// Verify RDS configuration.
	rds := hcm.GetRds()
	require.NotNil(t, rds, "HCM should use RDS")
	require.Equal(t, "aigateway-mcp-backend-listener-route-config", rds.RouteConfigName, "RDS should reference correct route config")

	// Verify config source.
	configSource := rds.ConfigSource
	require.NotNil(t, configSource, "RDS should have config source")
	require.NotNil(t, configSource.GetAds(), "config source should use ADS")
	require.Equal(t, corev3.ApiVersion_V3, configSource.ResourceApiVersion, "config source should use v3 API")
	require.NoError(t, listener.ValidateAll(), "listener configuration should be valid")
}

func TestServer_createRoutesForBackendListener(t *testing.T) {
	s := &Server{log: zap.New()}
	t.Run("empty", func(t *testing.T) { require.Nil(t, s.createRoutesForBackendListener([]*routev3.RouteConfiguration{})) })

	routes := []*routev3.RouteConfiguration{
		{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Name: "test-vh",
					Routes: []*routev3.Route{
						{Name: "normal"},
						{Name: internalapi.MCPHTTPRoutePrefix + "foo-bar/rule/0"},
						{
							Name: internalapi.MCPHTTPRoutePrefix + "foo-bar/rule/1",
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: internalapi.MCPHTTPRoutePrefix + "foo-bar/rule/1"},
								},
							},
						},
						{
							Name: internalapi.MCPHTTPRoutePrefix + "foo-bar/rule/2",
							Action: &routev3.Route_Route{
								Route: &routev3.RouteAction{
									ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: internalapi.MCPHTTPRoutePrefix + "foo-bar/rule/2"},
								},
							},
						},
					},
				},
			},
		},
	}
	route := s.createRoutesForBackendListener(routes)
	require.Len(t, routes, 1, "should not change the number of route configurations")
	require.Len(t, routes[0].VirtualHosts, 1, "should not change the number of virtual hosts")
	require.Len(t, routes[0].VirtualHosts[0].Routes, 2, "should remove MCP backend routes from original listener")
	require.Equal(t, "normal", routes[0].VirtualHosts[0].Routes[0].Name)
	require.Equal(t, internalapi.MCPHTTPRoutePrefix+"foo-bar/rule/0", routes[0].VirtualHosts[0].Routes[1].Name)
	require.NotNil(t, route, "should have created routes")
	require.Len(t, route.VirtualHosts, 1, "should have one virtual host")
	require.Len(t, route.VirtualHosts[0].Routes, 2, "should have two routes")
	require.Equal(t, internalapi.MCPHTTPRoutePrefix+"foo-bar/rule/1", route.VirtualHosts[0].Routes[0].Name)
	require.Equal(t, internalapi.MCPHTTPRoutePrefix+"foo-bar/rule/2", route.VirtualHosts[0].Routes[1].Name)
}

func TestServer_modifyMCPGatewayGeneratedCluster(t *testing.T) {
	clusters := []*clusterv3.Cluster{
		{Name: "normal-cluster"},
		{Name: internalapi.MCPHTTPRoutePrefix + "foo-bar/rule/0"},
	}
	s := &Server{log: zap.New()}
	s.modifyMCPGatewayGeneratedCluster(clusters)

	require.Len(t, clusters, 2, "should not change the number of clusters")
	require.Equal(t, "normal-cluster", clusters[0].Name, "should not modify normal clusters")
	second := clusters[1]
	require.NotNil(t, second.LoadAssignment, "should have a load balancer assignment")
	require.NotEmpty(t, second.LoadAssignment.Endpoints)
	require.Len(t, second.LoadAssignment.Endpoints[0].LbEndpoints, 1, "should have exactly one LB endpoint")
	lbendpoint := second.LoadAssignment.Endpoints[0].LbEndpoints[0]
	require.NotNil(t, lbendpoint.HostIdentifier, "LB endpoint should have a host identifier")
	socketAddr := lbendpoint.HostIdentifier.(*endpointv3.LbEndpoint_Endpoint)
	require.NotNil(t, socketAddr, "LB endpoint should have a socket address")
	addr := socketAddr.Endpoint.Address.GetSocketAddress()
	require.NotNil(t, addr, "socket address should be set")
	require.Equal(t, "127.0.0.1", addr.Address, "should point to localhost")
}

func TestServer_modifyMCPOAuthCustomResponseRoute(t *testing.T) {
	r := &routev3.RouteConfiguration{
		VirtualHosts: []*routev3.VirtualHost{
			{
				Routes: []*routev3.Route{
					{
						Name: "first-route",
					},
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
	}

	s := &Server{log: zap.New()}
	s.modifyMCPOAuthCustomResponseRoute([]*routev3.RouteConfiguration{r})

	require.Len(t, r.VirtualHosts[0].Routes, 2, "should have added one route")
	require.Len(t, r.VirtualHosts[0].Routes[1].ResponseHeadersToAdd, 3)
}

func TestServer_maybeUpdateMCPRoutes(t *testing.T) {
	s := &Server{log: zap.New()}
	emptyConfig := &anypb.Any{TypeUrl: "type.googleapis.com/google.protobuf.Empty"}

	configs := []*routev3.RouteConfiguration{
		{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Name: "vh",
					Routes: []*routev3.Route{
						{
							Name: internalapi.MCPHTTPRoutePrefix + "foo/rule/0",
							TypedPerFilterConfig: map[string]*anypb.Any{
								jwtAuthnFilterName: emptyConfig,
							},
						},
						{
							Name: internalapi.MCPHTTPRoutePrefix + "foo/rule/1",
							TypedPerFilterConfig: map[string]*anypb.Any{
								jwtAuthnFilterName: emptyConfig,
								"other-filter":     emptyConfig,
							},
						},
					},
				},
			},
		},
	}

	s.maybeUpdateMCPRoutes(configs)
	require.Contains(t, configs[0].VirtualHosts[0].Routes[0].TypedPerFilterConfig, jwtAuthnFilterName)
	require.NotContains(t, configs[0].VirtualHosts[0].Routes[1].TypedPerFilterConfig, jwtAuthnFilterName)
	require.Contains(t, configs[0].VirtualHosts[0].Routes[1].TypedPerFilterConfig, "other-filter")
}
