// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestInsertAIGatewayExtProcFilter(t *testing.T) {
	tests := []struct {
		name                string
		existingFilters     []*httpconnectionmanagerv3.HttpFilter
		expectedPosition    int
		shouldPanic         bool
		expectedPanicMsg    string
		expectedFilterCount int
	}{
		{
			name:                "insert with only router filter",
			existingFilters:     []*httpconnectionmanagerv3.HttpFilter{{Name: "envoy.filters.http.router"}},
			expectedPosition:    0,
			expectedFilterCount: 2,
		},
		{
			name: "insert before router filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 3,
		},
		{
			name: "insert before extproc filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.ext_proc.existing"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 4,
		},
		{
			name: "insert before multiple extproc filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.ext_proc.existing"},
				{Name: "envoy.filters.http.ext_proc.existing.another"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 5,
		},
		{
			name: "insert before wasm filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.wasm"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 4,
		},
		{
			name: "insert before rbac filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.rbac"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 4,
		},
		{
			name: "insert before local_ratelimit filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.local_ratelimit"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 4,
		},
		{
			name: "insert before ratelimit filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.ratelimit"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 4,
		},
		{
			name: "insert before custom_response filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.custom_response"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 4,
		},
		{
			name: "insert before credential_injector filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.credential_injector"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 4,
		},
		{
			name: "insert before compressor filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.compressor"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 4,
		},
		{
			name: "insert at end when only early filters present",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.cors"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    2,
			expectedFilterCount: 4,
		},
		{
			name: "insert with multiple filters requiring ordering",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.cors"},
				{Name: "envoy.filters.http.ext_proc.other"},
				{Name: "envoy.filters.http.rbac"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    2,
			expectedFilterCount: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := &httpconnectionmanagerv3.HttpConnectionManager{
				HttpFilters: make([]*httpconnectionmanagerv3.HttpFilter, len(tt.existingFilters)),
			}
			copy(mgr.HttpFilters, tt.existingFilters)

			newFilter := &httpconnectionmanagerv3.HttpFilter{
				Name:       aiGatewayExtProcName,
				ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{TypedConfig: &anypb.Any{}},
			}

			err := insertAIGatewayExtProcFilter(mgr, newFilter)
			require.NoError(t, err)

			require.Len(t, mgr.HttpFilters, tt.expectedFilterCount)
			require.Equal(t, aiGatewayExtProcName, mgr.HttpFilters[tt.expectedPosition].Name)

			for i, originalFilter := range tt.existingFilters {
				if i < tt.expectedPosition {
					require.Equal(t, originalFilter.Name, mgr.HttpFilters[i].Name, "filter at position %d should be preserved", i)
				} else {
					require.Equal(t, originalFilter.Name, mgr.HttpFilters[i+1].Name, "filter at position %d should be shifted by 1", i)
				}
			}
		})
	}
}

func TestServer_isRouteGeneratedByAIGateway(t *testing.T) {
	emptyStruct, err := structpb.NewStruct(map[string]any{})
	require.NoError(t, err)

	structWithEmptyResources, err := structpb.NewStruct(map[string]any{
		"resources": nil,
	})
	require.NoError(t, err)

	withAnnotationsListStruct, err := structpb.NewStruct(map[string]any{
		"resources": []any{
			map[string]any{
				"annotations": map[string]any{},
			},
		},
	})
	require.NoError(t, err)

	withOKAnnotationsListStruct, err := structpb.NewStruct(map[string]any{
		"resources": []any{
			map[string]any{
				"annotations": map[string]any{
					internalapi.AIGatewayGeneratedHTTPRouteAnnotation: "true",
				},
			},
		},
	})
	require.NoError(t, err)

	for _, tt := range []struct {
		name     string
		route    *routev3.Route
		expected bool
	}{
		{
			name:     "no metadata",
			route:    &routev3.Route{},
			expected: false,
		},
		{
			name: "no metadata.Fields",
			route: &routev3.Route{
				Metadata: &corev3.Metadata{},
			},
			expected: false,
		},
		{
			name: "no metadata.Fields 'envoy-ai_gateway'",
			route: &routev3.Route{
				Metadata: &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{}},
			},
			expected: false,
		},
		{
			name: "no resources in metadata.Fields 'envoy-gateway'",
			route: &routev3.Route{
				Metadata: &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{
					"envoy-gateway": emptyStruct,
				}},
			},
			expected: false,
		},
		{
			name: "resources do not have annotations",
			route: &routev3.Route{
				Metadata: &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{
					"envoy-gateway": structWithEmptyResources,
				}},
			},
			expected: false,
		},
		{
			name: "annotations are empty",
			route: &routev3.Route{
				Metadata: &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{
					"envoy-gateway": withAnnotationsListStruct,
				}},
			},
			expected: false,
		},
		{
			name: "annotations are empty",
			route: &routev3.Route{
				Metadata: &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{
					"envoy-gateway": withOKAnnotationsListStruct,
				}},
			},
			expected: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{log: zap.New()}
			result := s.isRouteGeneratedByAIGateway(tt.route)
			require.Equal(t, tt.expected, result)
		})
	}
}

func Test_shouldAIGatewayExtProcBeInserted(t *testing.T) {
	tests := []struct {
		name     string
		filters  []*httpconnectionmanagerv3.HttpFilter
		expected bool
	}{
		{
			filters:  []*httpconnectionmanagerv3.HttpFilter{{}},
			expected: true,
		},
		{
			filters:  []*httpconnectionmanagerv3.HttpFilter{{Name: aiGatewayExtProcName}},
			expected: false,
		},
		{
			filters:  []*httpconnectionmanagerv3.HttpFilter{{}, {Name: aiGatewayExtProcName}, {}},
			expected: false,
		},
		{
			filters:  []*httpconnectionmanagerv3.HttpFilter{{}, {}},
			expected: true,
		},
	}

	for _, tt := range tests {
		result := shouldAIGatewayExtProcBeInserted(tt.filters)
		require.Equal(t, tt.expected, result)
	}
}

func Test_findListenerRouteConfigs(t *testing.T) {
	newHCM := func(name string) *httpconnectionmanagerv3.HttpConnectionManager {
		return &httpconnectionmanagerv3.HttpConnectionManager{
			RouteSpecifier: &httpconnectionmanagerv3.HttpConnectionManager_Rds{
				Rds: &httpconnectionmanagerv3.Rds{RouteConfigName: name},
			},
		}
	}
	l := &listenerv3.Listener{
		DefaultFilterChain: &listenerv3.FilterChain{
			Filters: []*listenerv3.Filter{
				{
					Name:       wellknown.HTTPConnectionManager,
					ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, newHCM("foo"))},
				},
			},
		},
		FilterChains: []*listenerv3.FilterChain{
			{
				Filters: []*listenerv3.Filter{
					{
						Name:       wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, newHCM("bar"))},
					},
				},
			},
			// Non-HCM filter chain.
			{Filters: []*listenerv3.Filter{}},
		},
	}
	names := findListenerRouteConfigs(l)
	require.ElementsMatch(t, []string{"foo", "bar"}, names)
}

func TestEnableRouterLevelAIGatewayExtProcOnRoute(t *testing.T) {
	// Create metadata struct that marks a route as AIGateway-generated.
	aiGatewayAnnotation, err := structpb.NewStruct(map[string]any{
		"resources": []any{
			map[string]any{
				"annotations": map[string]any{
					internalapi.AIGatewayGeneratedHTTPRouteAnnotation: "true",
				},
			},
		},
	})
	require.NoError(t, err)

	t.Run("enables ext_proc for AIGateway-generated routes", func(t *testing.T) {
		s := &Server{log: zap.New()}
		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Name: "vh",
					Routes: []*routev3.Route{
						{
							Name: "ai-gateway-route",
							Metadata: &corev3.Metadata{
								FilterMetadata: map[string]*structpb.Struct{
									"envoy-gateway": aiGatewayAnnotation,
								},
							},
						},
					},
				},
			},
		}

		enabled, err := s.enableRouterLevelAIGatewayExtProcOnRoute(routeConfig)
		require.NoError(t, err)
		require.True(t, enabled)

		route := routeConfig.VirtualHosts[0].Routes[0]
		require.NotNil(t, route.TypedPerFilterConfig)
		require.Contains(t, route.TypedPerFilterConfig, aiGatewayExtProcName)

		// Verify the config is ExtProcPerRoute with Overrides (which enables the filter).
		perRoute := &extprocv3.ExtProcPerRoute{}
		err = route.TypedPerFilterConfig[aiGatewayExtProcName].UnmarshalTo(perRoute)
		require.NoError(t, err)
		require.NotNil(t, perRoute.GetOverride())
		overrides, ok := perRoute.GetOverride().(*extprocv3.ExtProcPerRoute_Overrides)
		require.True(t, ok)
		require.NotNil(t, overrides.Overrides)
	})

	t.Run("does not modify non-AIGateway routes", func(t *testing.T) {
		s := &Server{log: zap.New()}
		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Name: "vh",
					Routes: []*routev3.Route{
						{
							Name: "normal-route",
							// No AIGateway metadata.
						},
					},
				},
			},
		}

		enabled, err := s.enableRouterLevelAIGatewayExtProcOnRoute(routeConfig)
		require.NoError(t, err)
		require.False(t, enabled)

		route := routeConfig.VirtualHosts[0].Routes[0]
		require.Nil(t, route.TypedPerFilterConfig)
	})

	t.Run("handles mixed routes", func(t *testing.T) {
		s := &Server{log: zap.New()}
		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{
				{
					Name: "vh",
					Routes: []*routev3.Route{
						{
							Name: "ai-gateway-route",
							Metadata: &corev3.Metadata{
								FilterMetadata: map[string]*structpb.Struct{
									"envoy-gateway": aiGatewayAnnotation,
								},
							},
						},
						{
							Name: "normal-route",
							// No AIGateway metadata.
						},
					},
				},
			},
		}

		enabled, err := s.enableRouterLevelAIGatewayExtProcOnRoute(routeConfig)
		require.NoError(t, err)
		require.True(t, enabled)

		// AIGateway route should have ext_proc enabled.
		aiRoute := routeConfig.VirtualHosts[0].Routes[0]
		require.NotNil(t, aiRoute.TypedPerFilterConfig)
		require.Contains(t, aiRoute.TypedPerFilterConfig, aiGatewayExtProcName)

		// Non-AIGateway route should not be modified.
		normalRoute := routeConfig.VirtualHosts[0].Routes[1]
		require.Nil(t, normalRoute.TypedPerFilterConfig)
	})

	t.Run("handles empty route config", func(t *testing.T) {
		s := &Server{log: zap.New()}
		routeConfig := &routev3.RouteConfiguration{
			VirtualHosts: []*routev3.VirtualHost{},
		}

		enabled, err := s.enableRouterLevelAIGatewayExtProcOnRoute(routeConfig)
		require.NoError(t, err)
		require.False(t, enabled)
	})
}
