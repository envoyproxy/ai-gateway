// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/backendpolicy"
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

// credentialOverrideBSP builds a policy attached to backendName that sources its credential from
// the given Envoy metadata namespace.
func credentialOverrideBSP(name, namespace, backendName, metadataNamespace string) *aigv1b1.BackendSecurityPolicy {
	return &aigv1b1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: aigv1b1.BackendSecurityPolicySpec{
			Type: aigv1b1.BackendSecurityPolicyTypeAPIKey,
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{{
				Group: backendpolicy.AIServiceBackendGroup,
				Kind:  backendpolicy.AIServiceBackendKind,
				Name:  gwapiv1.ObjectName(backendName),
			}},
			CredentialOverride: &aigv1b1.BackendSecurityPolicyCredentialOverride{
				FromDynamicMetadata: &aigv1b1.CredentialOverrideFromDynamicMetadata{Namespace: metadataNamespace},
			},
		},
	}
}

// extProcFilterConfig returns the upstream ai-gateway ext_proc configuration of the cluster.
func extProcFilterConfig(t *testing.T, cluster *clusterv3.Cluster) *extprocv3.ExternalProcessor {
	t.Helper()
	po := &httpv3.HttpProtocolOptions{}
	require.NoError(t, cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"].UnmarshalTo(po))
	for _, f := range po.HttpFilters {
		if f.Name == aiGatewayExtProcName {
			cfg := &extprocv3.ExternalProcessor{}
			require.NoError(t, f.GetTypedConfig().UnmarshalTo(cfg))
			return cfg
		}
	}
	t.Fatal("upstream ext_proc filter not found")
	return nil
}

// Verifies the upstream ext_proc filter forwards the namespaces referenced by
// credentialOverride.fromDynamicMetadata; without them Envoy sends an empty MetadataContext.
//
// The list is scoped to the backends behind the cluster being modified: a policy on an unrelated
// backend must not widen what Envoy copies into this cluster's ProcessingRequests.
func Test_maybeModifyCluster_forwardsCredentialOverrideNamespaces(t *testing.T) {
	c := newFakeClient()
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{
					{Name: "some-backend"},
					{Name: "plain-backend"},
				}},
			},
		},
	}))
	require.NoError(t, c.Create(t.Context(), credentialOverrideBSP("md-source", "ns", "some-backend", "envoy.filters.http.ext_authz")))
	// Attached to a backend this route never references: must not show up anywhere below.
	require.NoError(t, c.Create(t.Context(), credentialOverrideBSP("other", "ns", "unrelated-backend", "zzz.unrelated")))
	s, err := New(c, logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)

	t.Run("rule-level cluster covers every backend of the rule", func(t *testing.T) {
		cluster := &clusterv3.Cluster{Name: "httproute/ns/myroute/rule/0"}
		require.NoError(t, s.maybeModifyCluster(t.Context(), cluster, nil))

		cfg := extProcFilterConfig(t, cluster)
		require.Equal(t, []string{aigv1b1.AIGatewayFilterMetadataNamespace},
			cfg.MetadataOptions.GetReceivingNamespaces().GetUntyped())
		require.Equal(t, []string{"envoy.filters.http.ext_authz"},
			cfg.MetadataOptions.GetForwardingNamespaces().GetUntyped())
	})

	t.Run("per-backend cluster only gets its own backend's namespaces", func(t *testing.T) {
		cluster := &clusterv3.Cluster{Name: "httproute/ns/myroute/rule/0/backend/1"}
		require.NoError(t, s.maybeModifyCluster(t.Context(), cluster, nil))

		// backend/1 is plain-backend, which has no policy at all.
		require.Nil(t, extProcFilterConfig(t, cluster).MetadataOptions.GetForwardingNamespaces())
	})
}

// A cluster that already carries the ai-gateway ext_proc - a re-translation, or a user-supplied
// typed_extension_protocol_options - must still pick up the current forwarding namespaces, or the
// credential override silently never works for that backend.
func Test_maybeModifyCluster_refreshesForwardingNamespacesOnExistingFilter(t *testing.T) {
	c := newFakeClient()
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "some-backend"}}},
			},
		},
	}))
	require.NoError(t, c.Create(t.Context(), credentialOverrideBSP("md-source", "ns", "some-backend", "envoy.filters.http.ext_authz")))
	s, err := New(c, logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)

	// A chain that already has our ext_proc, with no forwarding namespaces on it.
	existing, err := toAny(&extprocv3.ExternalProcessor{
		MetadataOptions: &extprocv3.MetadataOptions{
			ReceivingNamespaces: &extprocv3.MetadataOptions_MetadataNamespaces{
				Untyped: []string{aigv1b1.AIGatewayFilterMetadataNamespace},
			},
		},
	})
	require.NoError(t, err)
	poAny, err := toAny(&httpv3.HttpProtocolOptions{
		HttpFilters: []*httpconnectionmanagerv3.HttpFilter{{
			Name:       aiGatewayExtProcName,
			ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{TypedConfig: existing},
		}},
	})
	require.NoError(t, err)
	cluster := &clusterv3.Cluster{
		Name: "httproute/ns/myroute/rule/0",
		TypedExtensionProtocolOptions: map[string]*anypb.Any{
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": poAny,
		},
	}

	require.NoError(t, s.maybeModifyCluster(t.Context(), cluster, nil))

	cfg := extProcFilterConfig(t, cluster)
	require.Equal(t, []string{"envoy.filters.http.ext_authz"},
		cfg.MetadataOptions.GetForwardingNamespaces().GetUntyped())
	// The rest of the existing configuration is left alone.
	require.Equal(t, []string{aigv1b1.AIGatewayFilterMetadataNamespace},
		cfg.MetadataOptions.GetReceivingNamespaces().GetUntyped())
}
