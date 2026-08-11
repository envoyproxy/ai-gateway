// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	xdsmatcherv3 "github.com/cncf/xds/go/xds/type/matcher/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	clusterspecifierv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/router/cluster_specifiers/matcher/v3"
	typematcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// dynFallbackTestFoldedCluster builds a folded cluster in the shape maybeModifyCluster leaves
// it: one locality per named backend with identity metadata and priorities (first locality at
// priority 1, the rest at 0), plus a transport_socket_matches entry consumed by the second
// backend when present.
func dynFallbackTestFoldedCluster(name string, backendIdentities ...string) *clusterv3.Cluster {
	endpoint := func(backendName, tsmName string) *endpointv3.LbEndpoint {
		md := map[string]*structpb.Struct{
			internalapi.InternalEndpointMetadataNamespace: {
				Fields: map[string]*structpb.Value{
					internalapi.InternalMetadataBackendNameKey: structpb.NewStringValue(backendName),
				},
			},
		}
		if tsmName != "" {
			md[transportSocketMatchMetadataNamespace] = &structpb.Struct{
				Fields: map[string]*structpb.Value{"name": structpb.NewStringValue(tsmName)},
			}
		}
		return &endpointv3.LbEndpoint{Metadata: &corev3.Metadata{FilterMetadata: md}}
	}
	c := &clusterv3.Cluster{
		Name:                 name,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STRICT_DNS},
		ConnectTimeout:       durationpb.New(1),
		OutlierDetection:     &clusterv3.OutlierDetection{},
		TransportSocketMatches: []*clusterv3.Cluster_TransportSocketMatch{
			{
				Name:            name + "/tls/1",
				Match:           &structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue(name + "/tls/1")}},
				TransportSocket: &corev3.TransportSocket{Name: "tls-for-second"},
			},
		},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{ClusterName: name},
	}
	for i, identity := range backendIdentities {
		tsm := ""
		priority := uint32(0)
		if i == 0 {
			priority = 1
		}
		if i == 1 {
			tsm = name + "/tls/1"
		}
		c.LoadAssignment.Endpoints = append(c.LoadAssignment.Endpoints, &endpointv3.LocalityLbEndpoints{
			Priority:    priority,
			LbEndpoints: []*endpointv3.LbEndpoint{endpoint(identity, tsm)},
		})
	}
	return c
}

func TestApplyDynamicFallback(t *testing.T) {
	c := newFakeClient()
	// "aaa" has the WORSE priority (1) and "bbb" the better one (0): the matcher defaults must
	// follow priority order (bbb first), not declaration order.
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dynroute",
			Namespace:   "ns",
			Annotations: map[string]string{internalapi.DynamicFallbackAnnotationKey: "true"},
		},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{
						{Name: "aaa", Priority: ptr.To[uint32](1)},
						{Name: "disabled", Weight: ptr.To[int32](0)},
						// The alias, not the resource name, is the published vocabulary.
						{Name: "bbb-resource", Alias: "bbb", Priority: ptr.To[uint32](0)},
					},
				},
				{
					// Single active backend: nothing to fall back to; must be skipped.
					BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "solo"}},
				},
			},
		},
	}))
	// A second opted-in route sharing backend "aaa": it must REUSE aaa's shared cluster.
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dynroute2",
			Namespace:   "ns",
			Annotations: map[string]string{internalapi.DynamicFallbackAnnotationKey: "true"},
		},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "aaa"}, {Name: "ccc", Priority: ptr.To[uint32](1)}}},
			},
		},
	}))
	// Same shape but without the annotation: must remain untouched.
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "plainroute", Namespace: "ns"},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "aaa"}, {Name: "bbb-resource"}}},
			},
		},
	}))

	s, err := New(c, logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)

	const (
		foldedName  = "httproute/ns/dynroute/rule/0"
		folded2Name = "httproute/ns/dynroute2/rule/0"
		sharedAAA   = "aigw-dynfb/backend/ns/aaa"
		sharedBBB   = "aigw-dynfb/backend/ns/bbb-resource/bbb"
		sharedCCC   = "aigw-dynfb/backend/ns/ccc"
	)
	folded := dynFallbackTestFoldedCluster(foldedName, "aaa-identity", "bbb-identity")
	folded2 := dynFallbackTestFoldedCluster(folded2Name, "aaa-identity-2", "ccc-identity")
	plain := dynFallbackTestFoldedCluster("httproute/ns/plainroute/rule/0", "aaa-identity", "bbb-identity")
	soloCluster := &clusterv3.Cluster{Name: "httproute/ns/dynroute/rule/1"}

	routeWithRetry := &routev3.Route{
		Name: "httproute/ns/dynroute/rule/0/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: foldedName},
			RetryPolicy:      &routev3.RetryPolicy{RetryOn: "retriable-status-codes"},
		}},
	}
	routeNoRetry := &routev3.Route{
		Name: "httproute/ns/dynroute/rule/0/match/1",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: foldedName},
		}},
	}
	route2 := &routev3.Route{
		Name: "httproute/ns/dynroute2/rule/0/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: folded2Name},
		}},
	}
	soloRoute := &routev3.Route{
		Name: "httproute/ns/dynroute/rule/1/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "httproute/ns/dynroute/rule/1"},
		}},
	}
	plainRoute := &routev3.Route{
		Name: "httproute/ns/plainroute/rule/0/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "httproute/ns/plainroute/rule/0"},
		}},
	}
	directResponse := &routev3.Route{Name: "httproute/ns/dynroute/rule/0/match/2"}

	vh := &routev3.VirtualHost{Routes: []*routev3.Route{routeWithRetry, routeNoRetry, route2, soloRoute, plainRoute, directResponse}}
	clusters, err := s.applyDynamicFallback(t.Context(),
		[]*clusterv3.Cluster{folded, folded2, plain, soloCluster},
		[]*routev3.RouteConfiguration{{VirtualHosts: []*routev3.VirtualHost{vh}}})
	require.NoError(t, err)

	// Three distinct backends across the two opted-in routes ("aaa" is SHARED) → exactly three
	// synthesized clusters; the folded clusters are retained for in-flight streams.
	require.Len(t, clusters, 7)
	dynAAA := requireClusterByName(t, clusters, sharedAAA)
	dynBBB := requireClusterByName(t, clusters, sharedBBB)
	dynCCC := requireClusterByName(t, clusters, sharedCCC)
	requireClusterByName(t, clusters, foldedName)
	requireClusterByName(t, clusters, folded2Name)

	// Each shared cluster: one locality at priority 0, backend-key identity metadata (the
	// per-rule-ref identity is REPLACED), cluster fields inherited.
	for _, dyn := range []*clusterv3.Cluster{dynAAA, dynBBB, dynCCC} {
		require.Len(t, dyn.LoadAssignment.Endpoints, 1, dyn.Name)
		require.Zero(t, dyn.LoadAssignment.Endpoints[0].Priority, dyn.Name)
		require.Equal(t, dyn.Name, dyn.LoadAssignment.ClusterName, dyn.Name)
		require.NotNil(t, dyn.OutlierDetection, dyn.Name)
		require.Empty(t, dyn.TransportSocketMatches, dyn.Name)
	}
	requireEndpointIdentity(t, dynAAA, "backend/ns/aaa")
	requireEndpointIdentity(t, dynBBB, "backend/ns/bbb-resource/bbb")
	requireEndpointIdentity(t, dynCCC, "backend/ns/ccc")
	// First-seen rule shaped aaa's cluster: dynroute's locality (no TLS), not dynroute2's.
	require.Nil(t, dynAAA.TransportSocket)
	require.NotNil(t, dynBBB.TransportSocket)
	require.Equal(t, "tls-for-second", dynBBB.TransportSocket.Name)

	// Both match routes of the rule are rewritten identically.
	for _, route := range []*routev3.Route{routeWithRetry, routeNoRetry} {
		action := route.GetRoute()
		plugin := action.GetInlineClusterSpecifierPlugin()
		require.NotNil(t, plugin, route.Name)
		require.Equal(t, matcherClusterSpecifierName, plugin.Extension.Name)

		specifier := &clusterspecifierv3.MatcherClusterSpecifier{}
		require.NoError(t, plugin.Extension.TypedConfig.UnmarshalTo(specifier))
		top := specifier.ClusterMatcher
		requireHeaderInput(t, top.GetMatcherTree().GetInput().GetTypedConfig(), internalapi.EnvoyAttemptCountHeader)
		// Priority order is bbb (0) then aaa (1): every default lands accordingly.
		require.Equal(t, sharedBBB, onMatchCluster(t, top.OnNoMatch))

		slots := top.GetMatcherTree().GetExactMatchMap().Map
		require.Len(t, slots, 2)
		for slot, expDefault := range map[string]string{
			"0": sharedBBB, // first default: bbb
			"1": sharedAAA, // second default: aaa
		} {
			inner := slots[slot].GetMatcher()
			require.NotNil(t, inner, "slot %s", slot)
			requireHeaderInput(t, inner.GetMatcherTree().GetInput().GetTypedConfig(),
				internalapi.DynamicFallbackSlotHeaderPrefix+slot)
			byName := inner.GetMatcherTree().GetExactMatchMap().Map
			require.Equal(t, sharedAAA, onMatchCluster(t, byName["aaa"]), "slot %s", slot)
			require.Equal(t, sharedBBB, onMatchCluster(t, byName["bbb"]), "slot %s", slot)
			require.Equal(t, expDefault, onMatchCluster(t, inner.OnNoMatch), "slot %s", slot)
		}

		require.True(t, action.RetryPolicy.RefreshClusterOnRetry, route.Name)
		// The rule key that composes with the shared clusters' backend keys.
		requireRouteRuleKey(t, route, "ns/route/dynroute/rule/0")
	}
	// The user-supplied retry policy is preserved; the defaulted one covers provider failures
	// with one retry per remaining chain entry.
	require.Equal(t, "retriable-status-codes", routeWithRetry.GetRoute().RetryPolicy.RetryOn)
	require.Equal(t, "5xx", routeNoRetry.GetRoute().RetryPolicy.RetryOn)
	require.Equal(t, uint32(1), routeNoRetry.GetRoute().RetryPolicy.NumRetries.Value)

	// The second route carries its own rule key and reuses aaa's shared cluster in its matcher.
	requireRouteRuleKey(t, route2, "ns/route/dynroute2/rule/0")
	specifier2 := &clusterspecifierv3.MatcherClusterSpecifier{}
	require.NoError(t, route2.GetRoute().GetInlineClusterSpecifierPlugin().Extension.TypedConfig.UnmarshalTo(specifier2))
	slots2 := specifier2.ClusterMatcher.GetMatcherTree().GetExactMatchMap().Map
	require.Equal(t, sharedAAA, onMatchCluster(t, slots2["0"].GetMatcher().GetMatcherTree().GetExactMatchMap().Map["aaa"]))
	require.Equal(t, sharedCCC, onMatchCluster(t, slots2["0"].GetMatcher().GetMatcherTree().GetExactMatchMap().Map["ccc"]))

	// The virtual host must stamp the attempt count for the matcher input.
	require.True(t, vh.IncludeRequestAttemptCount)

	// Untouched routes: single-backend rule, non-annotated route, non-forwarding route.
	require.Equal(t, "httproute/ns/dynroute/rule/1", soloRoute.GetRoute().GetCluster())
	require.Equal(t, "httproute/ns/plainroute/rule/0", plainRoute.GetRoute().GetCluster())
	require.Nil(t, directResponse.GetRoute())
}

func TestApplyDynamicFallback_skips(t *testing.T) {
	c := newFakeClient()
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "dynroute",
			Namespace:   "ns",
			Annotations: map[string]string{internalapi.DynamicFallbackAnnotationKey: "true"},
		},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "aaa"}, {Name: "bbb", Priority: ptr.To[uint32](1)}}},
			},
		},
	}))
	s, err := New(c, logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		clusters []*clusterv3.Cluster
		route    *routev3.Route
	}{
		{
			name: "weighted clusters split mode",
			route: &routev3.Route{
				Name: "httproute/ns/dynroute/rule/0/match/0",
				Action: &routev3.Route_Route{Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_WeightedClusters{WeightedClusters: &routev3.WeightedCluster{}},
				}},
			},
		},
		{
			name: "referenced cluster missing",
			route: &routev3.Route{
				Name: "httproute/ns/dynroute/rule/0/match/0",
				Action: &routev3.Route_Route{Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "httproute/ns/dynroute/rule/0"},
				}},
			},
		},
		{
			name: "locality count does not match backendRefs",
			clusters: []*clusterv3.Cluster{{
				Name: "httproute/ns/dynroute/rule/0",
				LoadAssignment: &endpointv3.ClusterLoadAssignment{
					Endpoints: []*endpointv3.LocalityLbEndpoints{{}},
				},
			}},
			route: &routev3.Route{
				Name: "httproute/ns/dynroute/rule/0/match/0",
				Action: &routev3.Route_Route{Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "httproute/ns/dynroute/rule/0"},
				}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vh := &routev3.VirtualHost{Routes: []*routev3.Route{tc.route}}
			clusters, err := s.applyDynamicFallback(t.Context(), tc.clusters,
				[]*routev3.RouteConfiguration{{VirtualHosts: []*routev3.VirtualHost{vh}}})
			require.NoError(t, err)
			require.Len(t, clusters, len(tc.clusters), "no clusters must be synthesized")
			require.False(t, vh.IncludeRequestAttemptCount)
			if cl := tc.route.GetRoute().GetCluster(); cl != "" {
				require.Nil(t, tc.route.GetRoute().GetInlineClusterSpecifierPlugin())
			}
			require.False(t, tc.route.GetRoute().GetRetryPolicy().GetRefreshClusterOnRetry())
		})
	}
}

// TestApplyDynamicFallback_sameBackendModelRefs: two refs to the SAME backend distinguished by
// alias (the per-model modelNameOverride pattern) are eligible and get distinct entry-keyed
// clusters, so a chain can order "opus,sonnet". Without aliases the published names collide and
// the rule keeps static behavior.
func TestApplyDynamicFallback_sameBackendModelRefs(t *testing.T) {
	c := newFakeClient()
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "modelroute",
			Namespace:   "ns",
			Annotations: map[string]string{internalapi.DynamicFallbackAnnotationKey: "true"},
		},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{
					{Name: "anthropic", Alias: "opus"},
					{Name: "anthropic", Alias: "sonnet", Priority: ptr.To[uint32](1)},
				}},
				// Same shape without aliases: published names collide on "anthropic";
				// must keep static behavior.
				{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{
					{Name: "anthropic"},
					{Name: "anthropic", Priority: ptr.To[uint32](1)},
				}},
			},
		},
	}))
	s, err := New(c, logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)

	folded := dynFallbackTestFoldedCluster("httproute/ns/modelroute/rule/0", "opus-identity", "sonnet-identity")
	foldedNoAlias := dynFallbackTestFoldedCluster("httproute/ns/modelroute/rule/1", "a-identity", "b-identity")
	newRoute := func(name, cluster string) *routev3.Route {
		return &routev3.Route{
			Name: name,
			Action: &routev3.Route_Route{Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: cluster},
			}},
		}
	}
	route := newRoute("httproute/ns/modelroute/rule/0/match/0", folded.Name)
	routeNoAlias := newRoute("httproute/ns/modelroute/rule/1/match/0", foldedNoAlias.Name)
	vh := &routev3.VirtualHost{Routes: []*routev3.Route{route, routeNoAlias}}
	clusters, err := s.applyDynamicFallback(t.Context(),
		[]*clusterv3.Cluster{folded, foldedNoAlias},
		[]*routev3.RouteConfiguration{{VirtualHosts: []*routev3.VirtualHost{vh}}})
	require.NoError(t, err)

	// 2 folded (retained) + 2 entry clusters for the aliased rule; the alias-less rule
	// synthesizes nothing.
	require.Len(t, clusters, 4)
	opus := requireClusterByName(t, clusters, "aigw-dynfb/backend/ns/anthropic/opus")
	sonnet := requireClusterByName(t, clusters, "aigw-dynfb/backend/ns/anthropic/sonnet")
	requireEndpointIdentity(t, opus, "backend/ns/anthropic/opus")
	requireEndpointIdentity(t, sonnet, "backend/ns/anthropic/sonnet")

	specifier := &clusterspecifierv3.MatcherClusterSpecifier{}
	require.NoError(t, route.GetRoute().GetInlineClusterSpecifierPlugin().Extension.TypedConfig.UnmarshalTo(specifier))
	byName := specifier.ClusterMatcher.GetMatcherTree().GetExactMatchMap().Map["0"].GetMatcher().GetMatcherTree().GetExactMatchMap().Map
	require.Equal(t, opus.Name, onMatchCluster(t, byName["opus"]))
	require.Equal(t, sonnet.Name, onMatchCluster(t, byName["sonnet"]))
	requireRouteRuleKey(t, route, "ns/route/modelroute/rule/0")

	// The alias-less rule keeps its original cluster reference untouched.
	require.Equal(t, foldedNoAlias.Name, routeNoAlias.GetRoute().GetCluster())
	require.Nil(t, routeNoAlias.GetRoute().GetInlineClusterSpecifierPlugin())
}

// TestApplyDynamicFallback_shapeDivergence: a rule whose folded cluster carries different
// cluster-level settings (the shape a per-route BackendTrafficPolicy produces) must not inherit
// the shared clusters — it gets rule-scoped ones preserving its own settings.
func TestApplyDynamicFallback_shapeDivergence(t *testing.T) {
	c := newFakeClient()
	for _, name := range []string{"divroute", "divroute2"} {
		require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   "ns",
				Annotations: map[string]string{internalapi.DynamicFallbackAnnotationKey: "true"},
			},
			Spec: aigv1b1.AIGatewayRouteSpec{
				Rules: []aigv1b1.AIGatewayRouteRule{
					{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{
						{Name: "aaa"},
						{Name: "bbb-resource", Alias: "bbb", Priority: ptr.To[uint32](1)},
					}},
				},
			},
		}))
	}
	s, err := New(c, logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)

	const (
		folded1Name = "httproute/ns/divroute/rule/0"
		folded2Name = "httproute/ns/divroute2/rule/0"
		sharedAAA   = "aigw-dynfb/backend/ns/aaa"
		sharedBBB   = "aigw-dynfb/backend/ns/bbb-resource/bbb"
		scopedAAA   = "aigw-dynfb/rule/ns/route/divroute2/rule/0/backend/ns/aaa"
		scopedBBB   = "aigw-dynfb/rule/ns/route/divroute2/rule/0/backend/ns/bbb-resource/bbb"
	)
	folded1 := dynFallbackTestFoldedCluster(folded1Name, "aaa-identity", "bbb-identity")
	// Same backends, different cluster-level shaping; ConnectTimeout is the only real
	// divergence (the fixture's route-specific transport_socket_matches names must not count).
	folded2 := dynFallbackTestFoldedCluster(folded2Name, "aaa-identity", "bbb-identity")
	folded2.ConnectTimeout = durationpb.New(5)

	newRoute := func(name, cluster string) *routev3.Route {
		return &routev3.Route{
			Name: name,
			Action: &routev3.Route_Route{Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: cluster},
			}},
		}
	}
	route1 := newRoute("httproute/ns/divroute/rule/0/match/0", folded1Name)
	route2 := newRoute("httproute/ns/divroute2/rule/0/match/0", folded2Name)
	vh := &routev3.VirtualHost{Routes: []*routev3.Route{route1, route2}}
	clusters, err := s.applyDynamicFallback(t.Context(),
		[]*clusterv3.Cluster{folded1, folded2},
		[]*routev3.RouteConfiguration{{VirtualHosts: []*routev3.VirtualHost{vh}}})
	require.NoError(t, err)

	// 2 folded (retained) + 2 shared (from route 1) + 2 rule-scoped (route 2 diverges).
	require.Len(t, clusters, 6)
	shared := requireClusterByName(t, clusters, sharedAAA)
	scoped := requireClusterByName(t, clusters, scopedAAA)
	requireClusterByName(t, clusters, sharedBBB)
	scopedB := requireClusterByName(t, clusters, scopedBBB)

	// Each cluster preserves its own route's shaping.
	require.Equal(t, durationpb.New(1).AsDuration(), shared.ConnectTimeout.AsDuration())
	require.Equal(t, durationpb.New(5).AsDuration(), scoped.ConnectTimeout.AsDuration())
	// Identity is the backend key in both — the extproc's composed lookup is unaffected by
	// which cluster served the attempt.
	requireEndpointIdentity(t, shared, "backend/ns/aaa")
	requireEndpointIdentity(t, scoped, "backend/ns/aaa")
	// The rule-scoped TLS'd backend still resolves its transport socket.
	require.NotNil(t, scopedB.TransportSocket)
	require.Empty(t, scopedB.TransportSocketMatches)

	// Route 1's matcher points at the shared clusters, route 2's at its rule-scoped ones.
	for _, tc := range []struct {
		route              *routev3.Route
		expAAA, expDefault string
	}{
		{route1, sharedAAA, sharedAAA},
		{route2, scopedAAA, scopedAAA},
	} {
		specifier := &clusterspecifierv3.MatcherClusterSpecifier{}
		require.NoError(t, tc.route.GetRoute().GetInlineClusterSpecifierPlugin().Extension.TypedConfig.UnmarshalTo(specifier))
		top := specifier.ClusterMatcher
		require.Equal(t, tc.expDefault, onMatchCluster(t, top.OnNoMatch), tc.route.Name)
		byName := top.GetMatcherTree().GetExactMatchMap().Map["0"].GetMatcher().GetMatcherTree().GetExactMatchMap().Map
		require.Equal(t, tc.expAAA, onMatchCluster(t, byName["aaa"]), tc.route.Name)
	}
}

// TestApplyDynamicFallback_tlsSharingAcrossRoutes pins the false-divergence regression: two
// routes over the same TLS'd backends with IDENTICAL settings must share clusters even though
// their folded clusters carry route-specific transport_socket_matches names and endpoint
// metadata.
func TestApplyDynamicFallback_tlsSharingAcrossRoutes(t *testing.T) {
	c := newFakeClient()
	for _, name := range []string{"tlsroute", "tlsroute2"} {
		require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   "ns",
				Annotations: map[string]string{internalapi.DynamicFallbackAnnotationKey: "true"},
			},
			Spec: aigv1b1.AIGatewayRouteSpec{
				Rules: []aigv1b1.AIGatewayRouteRule{
					{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{
						{Name: "aaa"},
						{Name: "bbb-resource", Alias: "bbb", Priority: ptr.To[uint32](1)},
					}},
				},
			},
		}))
	}
	s, err := New(c, logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)

	folded1 := dynFallbackTestFoldedCluster("httproute/ns/tlsroute/rule/0", "aaa-identity", "bbb-identity")
	folded2 := dynFallbackTestFoldedCluster("httproute/ns/tlsroute2/rule/0", "aaa-identity", "bbb-identity")
	route1 := &routev3.Route{
		Name: "httproute/ns/tlsroute/rule/0/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: folded1.Name},
		}},
	}
	route2 := &routev3.Route{
		Name: "httproute/ns/tlsroute2/rule/0/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: folded2.Name},
		}},
	}
	vh := &routev3.VirtualHost{Routes: []*routev3.Route{route1, route2}}
	clusters, err := s.applyDynamicFallback(t.Context(),
		[]*clusterv3.Cluster{folded1, folded2},
		[]*routev3.RouteConfiguration{{VirtualHosts: []*routev3.VirtualHost{vh}}})
	require.NoError(t, err)

	// 2 folded + 2 shared. No rule-scoped clusters: the TLS'd backend (whose folded
	// transport_socket_matches names differ per route) must not register as divergence.
	require.Len(t, clusters, 4)
	requireClusterByName(t, clusters, "aigw-dynfb/backend/ns/aaa")
	bbb := requireClusterByName(t, clusters, "aigw-dynfb/backend/ns/bbb-resource/bbb")
	require.NotNil(t, bbb.TransportSocket)
	for _, cl := range clusters {
		require.NotContains(t, cl.Name, "aigw-dynfb/rule/", "no rule-scoped cluster expected: %s", cl.Name)
	}
}

func requireClusterByName(t *testing.T, clusters []*clusterv3.Cluster, name string) *clusterv3.Cluster {
	t.Helper()
	for _, c := range clusters {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cluster %s not found", name)
	return nil
}

func requireEndpointIdentity(t *testing.T, c *clusterv3.Cluster, expected string) {
	t.Helper()
	md := c.LoadAssignment.Endpoints[0].LbEndpoints[0].GetMetadata().
		GetFilterMetadata()[internalapi.InternalEndpointMetadataNamespace]
	require.NotNil(t, md, c.Name)
	require.Equal(t, expected, md.Fields[internalapi.InternalMetadataBackendNameKey].GetStringValue(), c.Name)
}

func requireRouteRuleKey(t *testing.T, route *routev3.Route, expected string) {
	t.Helper()
	md := route.GetMetadata().GetFilterMetadata()[internalapi.InternalEndpointMetadataNamespace]
	require.NotNil(t, md, route.Name)
	require.Equal(t, expected,
		md.Fields[internalapi.InternalMetadataDynamicFallbackRuleKey].GetStringValue(), route.Name)
}

func requireHeaderInput(t *testing.T, typedConfig *anypb.Any, expected string) {
	t.Helper()
	input := &typematcherv3.HttpRequestHeaderMatchInput{}
	require.NoError(t, typedConfig.UnmarshalTo(input))
	require.Equal(t, expected, input.HeaderName)
}

func onMatchCluster(t *testing.T, om *xdsmatcherv3.Matcher_OnMatch) string {
	t.Helper()
	action := &clusterspecifierv3.ClusterAction{}
	require.NoError(t, om.GetAction().GetTypedConfig().UnmarshalTo(action))
	return action.Cluster
}
