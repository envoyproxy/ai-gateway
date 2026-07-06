// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
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

// newMirrorTestServer builds a Server backed by the given fake client using the
// production New constructor so maybeModifyCluster runs the full filter-injection path.
func newMirrorTestServer(t *testing.T, c client.Client) *Server {
	t.Helper()
	s, err := New(c, logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)
	return s
}

func mirrorCluster(name string) *clusterv3.Cluster {
	return &clusterv3.Cluster{
		Name: name,
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			Endpoints: []*endpointv3.LocalityLbEndpoints{
				{LbEndpoints: []*endpointv3.LbEndpoint{{}}},
			},
		},
	}
}

func Test_maybeModifyCluster_handlesMirrorClusters(t *testing.T) {
	// Envoy Gateway names mirror backend clusters "httproute/<ns>/<name>/rule/<ruleIdx>-mirror-<mirrorIdx>".
	// The extension server must apply the same upstream ExtProc + header-mutation filters
	// to mirror clusters as it does to primary clusters so shadow traffic honors
	// per-backend ModelNameOverride / HeaderMutation / BodyMutation.
	c := newFakeClient()
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "primary"}},
					Mirrors: []aigv1b1.AIGatewayRouteRuleMirror{
						{
							BackendRef: aigv1b1.AIGatewayRouteRuleBackendRef{
								Name:              "shadow",
								ModelNameOverride: "shadow-model",
							},
						},
					},
				},
			},
		},
	}))
	s := newMirrorTestServer(t, c)

	// Envoy Gateway names mirror clusters with 1-based indexing — the first
	// mirror of rule 0 is "0-mirror-1". The extension server must convert that
	// back to 0-based for slice access into httpRouteRule.Mirrors.
	cluster := mirrorCluster("httproute/ns/myroute/rule/0-mirror-1")
	require.NoError(t, s.maybeModifyCluster(t.Context(), cluster))

	// Cluster metadata must contain the mirror backend name and the mirror flag
	// (used downstream to suppress LLMRequestCost double-emission).
	require.NotNil(t, cluster.Metadata)
	internalMD := cluster.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace]
	require.NotNil(t, internalMD)
	require.Equal(t,
		internalapi.PerRouteRuleMirrorBackendName("ns", "shadow", "myroute", 0, 0),
		internalMD.Fields[internalapi.InternalMetadataBackendNameKey].GetStringValue())
	require.True(t, internalMD.Fields[internalapi.InternalMetadataMirrorKey].GetBoolValue())

	// Endpoint metadata must mirror the cluster-level metadata.
	epMD := cluster.LoadAssignment.Endpoints[0].LbEndpoints[0].Metadata
	require.NotNil(t, epMD)
	epInternal := epMD.FilterMetadata[internalapi.InternalEndpointMetadataNamespace]
	require.Equal(t,
		internalapi.PerRouteRuleMirrorBackendName("ns", "shadow", "myroute", 0, 0),
		epInternal.Fields[internalapi.InternalMetadataBackendNameKey].GetStringValue())
	require.True(t, epInternal.Fields[internalapi.InternalMetadataMirrorKey].GetBoolValue())

	// TypedExtensionProtocolOptions must include the upstream ExtProc filter and
	// the header-mutation filter — same chain we install on primary clusters.
	raw, ok := cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
	require.True(t, ok, "mirror cluster must carry HttpProtocolOptions with ExtProc filter")
	var po httpv3.HttpProtocolOptions
	require.NoError(t, raw.UnmarshalTo(&po))
	filterNames := make([]string, 0, len(po.HttpFilters))
	for _, f := range po.HttpFilters {
		filterNames = append(filterNames, f.Name)
	}
	require.Contains(t, filterNames, aiGatewayExtProcName)
	require.Contains(t, filterNames, "envoy.filters.http.header_mutation")
}

func Test_maybeModifyCluster_mirrorIndexIsOneBased(t *testing.T) {
	// Envoy Gateway emits mirror cluster names with 1-based mirror indices
	// (the first mirror of rule 0 is "0-mirror-1", the second is "0-mirror-2").
	// The extension server must convert that suffix back to 0-based for slice
	// access into httpRouteRule.Mirrors. Without the conversion, every single-
	// mirror route silently skips ExtProc injection on the mirror cluster
	// because mirrors[1] is out of range against a length-1 slice.
	c := newFakeClient()
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "primary"}},
					Mirrors: []aigv1b1.AIGatewayRouteRuleMirror{
						{BackendRef: aigv1b1.AIGatewayRouteRuleBackendRef{Name: "shadow-a"}},
						{BackendRef: aigv1b1.AIGatewayRouteRuleBackendRef{Name: "shadow-b"}},
					},
				},
			},
		},
	}))
	s := newMirrorTestServer(t, c)

	cases := []struct {
		clusterName        string
		expectInstalled    bool
		expectBackend      string
		expectMirrorIndex0 int // 0-based mirror index after the conversion
	}{
		// "0-mirror-1" → mirrors[0] = shadow-a, ExtProc inserted.
		{"httproute/ns/myroute/rule/0-mirror-1", true, "shadow-a", 0},
		// "0-mirror-2" → mirrors[1] = shadow-b, ExtProc inserted.
		{"httproute/ns/myroute/rule/0-mirror-2", true, "shadow-b", 1},
		// "0-mirror-0" is invalid (mirror indices start at 1) — bail without error.
		{"httproute/ns/myroute/rule/0-mirror-0", false, "", 0},
		// "0-mirror-3" exceeds the slice — bail without error.
		{"httproute/ns/myroute/rule/0-mirror-3", false, "", 0},
	}

	for _, tc := range cases {
		t.Run(tc.clusterName, func(t *testing.T) {
			cluster := mirrorCluster(tc.clusterName)
			require.NoError(t, s.maybeModifyCluster(t.Context(), cluster))

			_, installed := cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
			require.Equal(t, tc.expectInstalled, installed,
				"ExtProc filter chain installation should match expectation for %q", tc.clusterName)

			if tc.expectInstalled {
				internalMD := cluster.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace]
				require.NotNil(t, internalMD)
				require.Equal(t,
					internalapi.PerRouteRuleMirrorBackendName("ns", tc.expectBackend, "myroute", 0, tc.expectMirrorIndex0),
					internalMD.Fields[internalapi.InternalMetadataBackendNameKey].GetStringValue(),
					"cluster metadata must reference the correct 0-based mirror entry for %q", tc.clusterName)
			}
		})
	}
}

func Test_maybeModifyCluster_rejectsMalformedMirrorClusterName(t *testing.T) {
	// Malformed mirror suffixes (non-numeric indices) should log and bail without
	// returning an error so a bad cluster name doesn't tear down the whole xDS push.
	s := newMirrorTestServer(t, newFakeClient())
	for _, name := range []string{
		"httproute/ns/myroute/rule/abc-mirror-0",
		"httproute/ns/myroute/rule/0-mirror-xyz",
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, s.maybeModifyCluster(t.Context(), &clusterv3.Cluster{Name: name}))
		})
	}
}
