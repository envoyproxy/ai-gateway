// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// aigExtProcAttributes returns whether the AI Gateway upstream extproc filter is installed on the
// cluster and, if so, the RequestAttributes the filter is configured with.
func aigExtProcAttributes(t *testing.T, c *clusterv3.Cluster) (bool, []string) {
	t.Helper()
	raw, ok := c.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
	if !ok {
		return false, nil
	}
	po := &httpv3.HttpProtocolOptions{}
	require.NoError(t, raw.UnmarshalTo(po))
	for _, f := range po.HttpFilters {
		if f.Name == aiGatewayExtProcName {
			ep := &extprocv3.ExternalProcessor{}
			require.NoError(t, f.GetTypedConfig().UnmarshalTo(ep))
			return true, ep.RequestAttributes
		}
	}
	return false, nil
}

// TestMaybeModifyCluster_MergedName verifies the MergeBackends adapter: a "backend/<...>" cluster
// (Envoy Gateway MergeBackends collapse) gets the upstream extproc filter installed only when an
// AIGateway route references it, and the filter is configured to resolve the backend from route
// metadata (so sister routes sharing the cluster keep distinct identities).
func TestMaybeModifyCluster_MergedName(t *testing.T) {
	const mergedName = "backend/Service/ns/myroute/8080"

	t.Run("merged + AIGW-referenced installs upstream filter with route-metadata resolution", func(t *testing.T) {
		s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
		require.NoError(t, err)
		cluster := &clusterv3.Cluster{Name: mergedName}

		require.NoError(t, s.maybeModifyCluster(t.Context(), cluster, map[string]bool{mergedName: true}))

		installed, attrs := aigExtProcAttributes(t, cluster)
		require.True(t, installed, "merged AIGW-referenced cluster must get the upstream extproc filter")
		require.Contains(t, attrs, internalapi.XDSRouteMetadataBackendNamePath,
			"merged clusters must resolve the backend from per-route metadata")
	})

	t.Run("merged but NOT AIGW-referenced is skipped (egress safety gate)", func(t *testing.T) {
		s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
		require.NoError(t, err)
		cluster := &clusterv3.Cluster{Name: mergedName}

		require.NoError(t, s.maybeModifyCluster(t.Context(), cluster, map[string]bool{}))

		installed, _ := aigExtProcAttributes(t, cluster)
		require.False(t, installed, "merged cluster not referenced by any AIGateway route must be left untouched")
		require.Empty(t, cluster.TypedExtensionProtocolOptions)
	})

	t.Run("non-AIGateway cluster name is skipped", func(t *testing.T) {
		s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
		require.NoError(t, err)
		cluster := &clusterv3.Cluster{Name: "egress/ns/some-service/8080"}

		require.NoError(t, s.maybeModifyCluster(t.Context(), cluster, map[string]bool{"egress/ns/some-service/8080": true}))

		installed, _ := aigExtProcAttributes(t, cluster)
		require.False(t, installed, "a name that is neither httproute/... nor backend/... must never be modified")
	})
}

func TestCollectAIGatewayReferencedClusters(t *testing.T) {
	s, err := New(newFakeClient(), logr.Discard(), udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)

	aigwRoute := func(cluster string) *routev3.Route {
		return &routev3.Route{
			Name:     "httproute/ns/r/rule/0/match/0",
			Metadata: aiGatewayRouteMetadata(t),
			Action: &routev3.Route_Route{Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: cluster},
			}},
		}
	}
	aigwWeighted := func(clusters ...string) *routev3.Route {
		var cw []*routev3.WeightedCluster_ClusterWeight
		for _, c := range clusters {
			cw = append(cw, &routev3.WeightedCluster_ClusterWeight{Name: c})
		}
		return &routev3.Route{
			Name:     "httproute/ns/r/rule/1/match/0",
			Metadata: aiGatewayRouteMetadata(t),
			Action: &routev3.Route_Route{Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_WeightedClusters{
					WeightedClusters: &routev3.WeightedCluster{Clusters: cw},
				},
			}},
		}
	}
	// Non-AIGateway route: no ai-gateway-generated metadata, so its cluster must NOT be collected.
	nonAIGW := &routev3.Route{
		Name: "httproute/ns/plain/rule/0/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "backend/Service/ns/not-ai/8080"},
		}},
	}

	routes := []*routev3.RouteConfiguration{{
		Name: "rc",
		VirtualHosts: []*routev3.VirtualHost{{
			Name:   "vh",
			Routes: []*routev3.Route{aigwRoute("backend/Service/ns/svc/8080"), aigwWeighted("backend/Service/ns/a/80", "backend/Service/ns/b/80"), nonAIGW},
		}},
	}}

	got := s.collectAIGatewayReferencedClusters(routes)
	require.Equal(t, map[string]bool{
		"backend/Service/ns/svc/8080": true,
		"backend/Service/ns/a/80":     true,
		"backend/Service/ns/b/80":     true,
	}, got)
	require.NotContains(t, got, "backend/Service/ns/not-ai/8080", "clusters of non-AIGateway routes must be excluded")
}

// TestPopulateRouteMetadataBackendName_MatchesEndpointMetadata asserts the backward-compatibility
// invariant: for a single-backendRef rule, the per-route backend name written into route metadata
// is identical to the per-endpoint backend name the legacy cluster path writes. extproc consults
// the route metadata first, so this equality guarantees the change does not alter which backend is
// resolved when MergeBackends is off.
func TestPopulateRouteMetadataBackendName_MatchesEndpointMetadata(t *testing.T) {
	route := &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{
				{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "backend-a"}}},
			},
		},
	}
	s := newTestServerWithRoute(t, route)

	// Legacy per-rule cluster: backend identity lands on the single endpoint's metadata.
	cluster := &clusterv3.Cluster{
		Name: "httproute/ns/myroute/rule/0",
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			Endpoints: []*endpointv3.LocalityLbEndpoints{
				{LbEndpoints: []*endpointv3.LbEndpoint{{}}},
			},
		},
	}
	require.NoError(t, s.maybeModifyCluster(t.Context(), cluster, nil))
	endpointBackendName := cluster.LoadAssignment.Endpoints[0].LbEndpoints[0].
		Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace].
		Fields[internalapi.InternalMetadataBackendNameKey].GetStringValue()

	// Matching route: backend identity lands on the route's internal metadata.
	r := &routev3.Route{Name: "httproute/ns/myroute/rule/0/match/0"}
	require.NoError(t, s.populateRouteMetadataBackendName(t.Context(), r))
	routeBackendName := r.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace].
		Fields[internalapi.InternalMetadataBackendNameKey].GetStringValue()

	require.Equal(t, internalapi.PerRouteRuleRefBackendName("ns", "backend-a", "myroute", 0, 0), routeBackendName)
	require.Equal(t, endpointBackendName, routeBackendName,
		"route metadata backend name must equal the legacy endpoint metadata backend name for single-backendRef rules")
}
