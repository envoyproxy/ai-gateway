// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/envoyproxy/ai-gateway/internal/controller"
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
			name: "insert before lua filter",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.fault"},
				{Name: "envoy.filters.http.lua"},
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
		{
			// Mirrors the EKS setup where an api-key ext_proc and a buffer filter are added ahead of AI
			// Gateway. The ext_proc at index 0 matches afterExtProcFilterPrefixes, but the buffer filter
			// must still run first so its larger request buffer limit applies to AI Gateway's BUFFERED
			// extproc. AI Gateway is inserted after the buffer filter (position 2).
			name: "insert after buffer when ext_proc precedes buffer",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.ext_proc.apikey"},
				{Name: "envoy.filters.http.buffer"},
				{Name: "envoy.filters.http.jwt_authn"},
				{Name: "envoy.filters.http.rbac"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    2,
			expectedFilterCount: 6,
		},
		{
			// When the buffer filter already precedes the first ext_proc filter, AI Gateway is inserted
			// right after the buffer filter (position 1), preserving Envoy Gateway's buffer-before-extproc
			// ordering.
			name: "insert after buffer when buffer precedes ext_proc",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.buffer"},
				{Name: "envoy.filters.http.ext_proc.apikey"},
				{Name: "envoy.filters.http.rbac"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    1,
			expectedFilterCount: 5,
		},
		{
			// Regression guard: with no buffer filter present, insertion behavior is unchanged and AI
			// Gateway lands ahead of the first ext_proc filter (position 0).
			name: "no buffer filter leaves ext_proc insertion unchanged",
			existingFilters: []*httpconnectionmanagerv3.HttpFilter{
				{Name: "envoy.filters.http.ext_proc.apikey"},
				{Name: "envoy.filters.http.rbac"},
				{Name: "envoy.filters.http.router"},
			},
			expectedPosition:    0,
			expectedFilterCount: 4,
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

func TestInsertHeaderToMetadataFilter(t *testing.T) {
	hcm := &httpconnectionmanagerv3.HttpConnectionManager{
		HttpFilters: []*httpconnectionmanagerv3.HttpFilter{{Name: wellknown.Router}},
	}
	filter, err := buildHeaderToMetadataFilter(map[string]string{"agent-session-id": "session.id"})
	require.NoError(t, err)
	err = insertHeaderToMetadataFilter(hcm, filter)
	require.NoError(t, err)
	require.Len(t, hcm.HttpFilters, 2)
	require.Equal(t, headerToMetadataFilterName, hcm.HttpFilters[0].Name)
	require.Equal(t, wellknown.Router, hcm.HttpFilters[1].Name)
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

func TestServer_insertRouterLevelAIGatewayExtProc_setsSchemeHeaderTransformation(t *testing.T) {
	hcm := &httpconnectionmanagerv3.HttpConnectionManager{
		HttpFilters: []*httpconnectionmanagerv3.HttpFilter{{Name: wellknown.Router}},
	}
	listener := &listenerv3.Listener{
		DefaultFilterChain: &listenerv3.FilterChain{
			Filters: []*listenerv3.Filter{
				{
					Name:       wellknown.HTTPConnectionManager,
					ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mustToAny(t, hcm)},
				},
			},
		},
	}
	s := &Server{log: zap.New()}
	require.NoError(t, s.insertRouterLevelAIGatewayExtProc(listener))

	updatedHCM, _, err := findHCM(listener.DefaultFilterChain)
	require.NoError(t, err)
	require.True(t, updatedHCM.GetSchemeHeaderTransformation().GetMatchUpstream(),
		"SchemeHeaderTransformation.MatchUpstream must be true so :scheme matches upstream TLS transport")
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

// routeWithLuaMetadata builds a routev3.Route that carries Envoy Gateway metadata.
func routeWithLuaMetadata(t *testing.T, namespace, name string, luaSlots []string) *routev3.Route {
	t.Helper()
	resources, err := structpb.NewStruct(map[string]any{
		"resources": []any{
			map[string]any{
				"namespace": namespace,
				"name":      name,
			},
		},
	})
	require.NoError(t, err)
	route := &routev3.Route{
		Metadata: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{
				"envoy-gateway": resources,
			},
		},
		TypedPerFilterConfig: make(map[string]*anypb.Any),
	}
	for _, slot := range luaSlots {
		route.TypedPerFilterConfig[slot] = &anypb.Any{}
	}
	return route
}

func TestBuildBeforeExtProcLuaFilterNames(t *testing.T) {
	makePolicy := func(namespace, name, targetName string, annotated bool) egv1a1.EnvoyExtensionPolicy {
		annotations := map[string]string{}
		if annotated {
			annotations[internalapi.LuaFilterOrderAnnotation] = internalapi.LuaFilterOrderBeforeExtProc
		}
		return egv1a1.EnvoyExtensionPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   namespace,
				Name:        name,
				Annotations: annotations,
			},
			Spec: egv1a1.EnvoyExtensionPolicySpec{
				PolicyTargetReferences: egv1a1.PolicyTargetReferences{
					TargetRefs: []gwapiv1.LocalPolicyTargetReferenceWithSectionName{
						{LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{Name: gwapiv1.ObjectName(targetName)}},
					},
				},
			},
		}
	}

	t.Run("no policies — no names", func(t *testing.T) {
		result := buildBeforeExtProcLuaFilterNames(nil, nil)
		require.Empty(t, result)
	})

	t.Run("unannotated policy — no names", func(t *testing.T) {
		policy := makePolicy("ns", "p1", "myroute", false)
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0"}),
				},
			}},
		}}
		result := buildBeforeExtProcLuaFilterNames([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Empty(t, result)
	})

	t.Run("annotated policy — collects lua slot names from route", func(t *testing.T) {
		policy := makePolicy("ns", "p1", "myroute", true)
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0", "envoy.filters.http.lua/1"}),
				},
			}},
		}}
		result := buildBeforeExtProcLuaFilterNames([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Equal(t, map[string]bool{
			"envoy.filters.http.lua/0": true,
			"envoy.filters.http.lua/1": true,
		}, result)
	})

	t.Run("annotated policy — does not collect non-lua filter names", func(t *testing.T) {
		policy := makePolicy("ns", "p1", "myroute", true)
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0"}),
				},
			}},
		}}
		routes[0].VirtualHosts[0].Routes[0].TypedPerFilterConfig["envoy.filters.http.rbac"] = &anypb.Any{}
		result := buildBeforeExtProcLuaFilterNames([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Equal(t, map[string]bool{"envoy.filters.http.lua/0": true}, result)
	})

	t.Run("mix of annotated and unannotated policies", func(t *testing.T) {
		policies := []egv1a1.EnvoyExtensionPolicy{
			makePolicy("ns", "annotated", "route-a", true),
			makePolicy("ns", "plain", "route-b", false),
		}
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "route-a", []string{"envoy.filters.http.lua/0"}),
					routeWithLuaMetadata(t, "ns", "route-b", []string{"envoy.filters.http.lua/1"}),
				},
			}},
		}}
		result := buildBeforeExtProcLuaFilterNames(policies, routes)
		require.Equal(t, map[string]bool{"envoy.filters.http.lua/0": true}, result)
	})
}

func TestMoveFiltersBeforeAIGatewayExtProc(t *testing.T) {
	f := func(name string) *httpconnectionmanagerv3.HttpFilter {
		return &httpconnectionmanagerv3.HttpFilter{Name: name}
	}
	mgr := func(names ...string) *httpconnectionmanagerv3.HttpConnectionManager {
		filters := make([]*httpconnectionmanagerv3.HttpFilter, len(names))
		for i, n := range names {
			filters[i] = f(n)
		}
		return &httpconnectionmanagerv3.HttpConnectionManager{HttpFilters: filters}
	}
	filterNames := func(m *httpconnectionmanagerv3.HttpConnectionManager) []string {
		names := make([]string, len(m.HttpFilters))
		for i, fi := range m.HttpFilters {
			names[i] = fi.Name
		}
		return names
	}

	t.Run("no names to move — chain unchanged", func(t *testing.T) {
		m := mgr(aiGatewayExtProcName, "envoy.filters.http.lua/0", "envoy.filters.http.router")
		moved := moveFiltersBeforeAIGatewayExtProc(m, map[string]bool{})
		require.False(t, moved)
		require.Equal(t, []string{aiGatewayExtProcName, "envoy.filters.http.lua/0", "envoy.filters.http.router"}, filterNames(m))
	})

	t.Run("ext_proc absent — chain unchanged", func(t *testing.T) {
		m := mgr("envoy.filters.http.lua/0", "envoy.filters.http.router")
		moved := moveFiltersBeforeAIGatewayExtProc(m, map[string]bool{"envoy.filters.http.lua/0": true})
		require.False(t, moved)
		require.Equal(t, []string{"envoy.filters.http.lua/0", "envoy.filters.http.router"}, filterNames(m))
	})

	t.Run("single lua filter moved to before AI GW ext_proc", func(t *testing.T) {
		// input: ext_proc before lua/0.
		m := mgr(aiGatewayExtProcName, "envoy.filters.http.lua/0", "envoy.filters.http.rbac", "envoy.filters.http.router")
		moved := moveFiltersBeforeAIGatewayExtProc(m, map[string]bool{"envoy.filters.http.lua/0": true})
		require.True(t, moved)
		// expected: lua/0 moved before AI-GW-ext_proc so response path runs lua after ext_proc.
		require.Equal(t, []string{
			"envoy.filters.http.lua/0",
			aiGatewayExtProcName,
			"envoy.filters.http.rbac",
			"envoy.filters.http.router",
		}, filterNames(m))
	})

	t.Run("multiple lua filters moved, order among them preserved", func(t *testing.T) {
		m := mgr(
			aiGatewayExtProcName,
			"envoy.filters.http.lua/0",
			"envoy.filters.http.lua/1",
			"envoy.filters.http.rbac",
			"envoy.filters.http.router",
		)
		moved := moveFiltersBeforeAIGatewayExtProc(m, map[string]bool{
			"envoy.filters.http.lua/0": true,
			"envoy.filters.http.lua/1": true,
		})
		require.True(t, moved)
		require.Equal(t, []string{
			"envoy.filters.http.lua/0",
			"envoy.filters.http.lua/1",
			aiGatewayExtProcName,
			"envoy.filters.http.rbac",
			"envoy.filters.http.router",
		}, filterNames(m))
	})

	t.Run("only annotated lua filter moved, others stay in place", func(t *testing.T) {
		// lua/0 is not annotated, lua/1 is annotated. only lua/1 should move before ext_proc.
		m := mgr(
			aiGatewayExtProcName,
			"envoy.filters.http.lua/0",
			"envoy.filters.http.lua/1",
			"envoy.filters.http.router",
		)
		moved := moveFiltersBeforeAIGatewayExtProc(m, map[string]bool{"envoy.filters.http.lua/1": true})
		require.True(t, moved)
		require.Equal(t, []string{
			"envoy.filters.http.lua/1",
			aiGatewayExtProcName,
			"envoy.filters.http.lua/0",
			"envoy.filters.http.router",
		}, filterNames(m))
	})
}

func TestMaybeReorderBeforeExtProcLuaFilters(t *testing.T) {
	makeListener := func(t *testing.T, filters ...*httpconnectionmanagerv3.HttpFilter) *listenerv3.Listener {
		t.Helper()
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{HttpFilters: filters}
		hcmAny := mustToAny(t, hcm)
		return &listenerv3.Listener{
			FilterChains: []*listenerv3.FilterChain{{
				Filters: []*listenerv3.Filter{{
					Name:       wellknown.HTTPConnectionManager,
					ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
				}},
			}},
		}
	}

	extractFilterNames := func(t *testing.T, listener *listenerv3.Listener) []string {
		t.Helper()
		hcm, _, err := findHCM(listener.FilterChains[0])
		require.NoError(t, err)
		names := make([]string, len(hcm.HttpFilters))
		for i, f := range hcm.HttpFilters {
			names[i] = f.Name
		}
		return names
	}

	makePolicy := func(namespace, name, targetName string) *egv1a1.EnvoyExtensionPolicy {
		return &egv1a1.EnvoyExtensionPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
				Annotations: map[string]string{
					internalapi.LuaFilterOrderAnnotation: internalapi.LuaFilterOrderBeforeExtProc,
				},
			},
			Spec: egv1a1.EnvoyExtensionPolicySpec{
				PolicyTargetReferences: egv1a1.PolicyTargetReferences{
					TargetRefs: []gwapiv1.LocalPolicyTargetReferenceWithSectionName{
						{LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{Name: gwapiv1.ObjectName(targetName)}},
					},
				},
			},
		}
	}

	t.Run("no annotated policies — chain unchanged", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
		s := &Server{log: zap.New(), k8sClient: k8sClient}

		// input mirrors the real state: ext_proc before lua/0.
		listener := makeListener(t,
			&httpconnectionmanagerv3.HttpFilter{Name: aiGatewayExtProcName},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.lua/0"},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.router"},
		)
		require.NoError(t, s.maybeReorderBeforeExtProcLuaFilters(context.Background(), []*listenerv3.Listener{listener}, nil))
		// chain must be unchanged, no annotation, nothing moved.
		require.Equal(t,
			[]string{aiGatewayExtProcName, "envoy.filters.http.lua/0", "envoy.filters.http.router"},
			extractFilterNames(t, listener),
		)
	})

	t.Run("annotated policy — lua filter moved before AI GW ext_proc", func(t *testing.T) {
		policy := makePolicy("ns", "wrap", "myroute")
		k8sClient := fake.NewClientBuilder().WithScheme(controller.Scheme).WithObjects(policy).Build()
		s := &Server{log: zap.New(), k8sClient: k8sClient}

		// input: ext_proc before lua/0.
		listener := makeListener(t,
			&httpconnectionmanagerv3.HttpFilter{Name: aiGatewayExtProcName},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.lua/0"},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.router"},
		)

		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0"}),
				},
			}},
		}}

		require.NoError(t, s.maybeReorderBeforeExtProcLuaFilters(context.Background(), []*listenerv3.Listener{listener}, routes))
		// after reorder: lua/0 before AI-GW-ext_proc.
		require.Equal(t,
			[]string{"envoy.filters.http.lua/0", aiGatewayExtProcName, "envoy.filters.http.router"},
			extractFilterNames(t, listener),
		)
	})
}
