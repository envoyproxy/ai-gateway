// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const localityTestCluster = "httproute/ns/myroute/rule/0"

// egLocality builds a LocalityLbEndpoints the way Envoy Gateway emits one, tagged in
// locality.region with the backendRef index it was built from. A negative index omits the region,
// which is what a cluster from outside Envoy Gateway's route translation looks like.
func egLocality(backendRefIndex int) *endpointv3.LocalityLbEndpoints {
	eps := &endpointv3.LocalityLbEndpoints{LbEndpoints: []*endpointv3.LbEndpoint{{}}}
	if backendRefIndex >= 0 {
		eps.Locality = &corev3.Locality{
			Region: fmt.Sprintf("%s/backend/%d", localityTestCluster, backendRefIndex),
		}
	}
	return eps
}

// backendNameOf returns the backend name metadata populated on a locality's first endpoint, or ""
// when none was populated.
func backendNameOf(eps *endpointv3.LocalityLbEndpoints) string {
	return eps.LbEndpoints[0].GetMetadata().
		GetFilterMetadata()[internalapi.InternalEndpointMetadataNamespace].
		GetFields()[internalapi.InternalMetadataBackendNameKey].GetStringValue()
}

func newLocalityTestServer(t *testing.T, log logr.Logger, refs ...aigv1b1.AIGatewayRouteRuleBackendRef) *Server {
	t.Helper()
	c := newFakeClient()
	require.NoError(t, c.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns"},
		Spec:       aigv1b1.AIGatewayRouteSpec{Rules: []aigv1b1.AIGatewayRouteRule{{BackendRefs: refs}}},
	}))
	s, err := New(c, log, udsPath, false, nil, nil, "envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)
	return s
}

func ref(name string) aigv1b1.AIGatewayRouteRuleBackendRef {
	return aigv1b1.AIGatewayRouteRuleBackendRef{Name: name}
}

func wantName(backend string, refIndex int) string {
	return internalapi.PerRouteRuleRefBackendName("ns", backend, "myroute", 0, refIndex)
}

// TestMaybeModifyClusterLocalitySkew covers the AIGatewayRoute and its generated HTTPRoute
// disagreeing on a rule's backend list. They are separate objects reconciled independently, so the
// endpoint localities in a cluster can be out of step with the backendRefs the names come from.
func TestMaybeModifyClusterLocalitySkew(t *testing.T) {
	for _, tc := range []struct {
		name       string
		refs       []aigv1b1.AIGatewayRouteRuleBackendRef
		localities []*endpointv3.LocalityLbEndpoints
		// want is the backend name expected on each locality, by locality index. "" means no
		// metadata should be populated for it.
		want []string
	}{
		{
			// The AIGatewayRoute has more backends than the cluster has localities. A running
			// counter indexes past the end of the locality list here, which is the panic.
			name: "more backendRefs than localities",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{ref("a"), ref("b"), ref("c")},
			localities: []*endpointv3.LocalityLbEndpoints{
				egLocality(0),
				egLocality(2),
			},
			want: []string{wantName("a", 0), wantName("c", 2)},
		},
		{
			// The gap is not at the tail, so a running counter labels c's endpoints as b.
			name:       "gap in the middle does not shift the backends after it",
			refs:       []aigv1b1.AIGatewayRouteRuleBackendRef{ref("a"), ref("b"), ref("c")},
			localities: []*endpointv3.LocalityLbEndpoints{egLocality(2)},
			want:       []string{wantName("c", 2)},
		},
		{
			// The cluster has more localities than the AIGatewayRoute has backends. No panic
			// today, but the extra locality must not be labelled with someone else's name.
			name: "more localities than backendRefs",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{ref("a"), ref("b")},
			localities: []*endpointv3.LocalityLbEndpoints{
				egLocality(0),
				egLocality(1),
				egLocality(2),
			},
			want: []string{wantName("a", 0), wantName("b", 1), ""},
		},
		{
			// Envoy Gateway leaves zero-weight backends out of the LoadAssignment entirely, and
			// numbers the regions of the ones it keeps by their original position.
			name: "zero weight backend",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{
				ref("a"),
				{Name: "disabled", Weight: ptr.To[int32](0)},
				ref("b"),
			},
			localities: []*endpointv3.LocalityLbEndpoints{egLocality(0), egLocality(2)},
			want:       []string{wantName("a", 0), wantName("b", 2)},
		},
		{
			// Localities are matched on their region, not their position in the list.
			name:       "localities out of order",
			refs:       []aigv1b1.AIGatewayRouteRuleBackendRef{ref("a"), ref("b")},
			localities: []*endpointv3.LocalityLbEndpoints{egLocality(1), egLocality(0)},
			want:       []string{wantName("b", 1), wantName("a", 0)},
		},
		{
			// A region naming a different rule cannot be used to index this one.
			name: "region from another rule is ignored",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{ref("a")},
			localities: []*endpointv3.LocalityLbEndpoints{{
				LbEndpoints: []*endpointv3.LbEndpoint{{}},
				Locality:    &corev3.Locality{Region: "httproute/ns/myroute/rule/7/backend/0"},
			}},
			want: []string{""},
		},
		{
			// Without regions there is nothing but order to go on, so fall back to it, bounded by
			// the locality count and skipping the zero-weight backends Envoy Gateway drops.
			name: "no regions falls back to order",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{
				ref("a"),
				{Name: "disabled", Weight: ptr.To[int32](0)},
				ref("b"),
			},
			localities: []*endpointv3.LocalityLbEndpoints{egLocality(-1), egLocality(-1)},
			want:       []string{wantName("a", 0), wantName("b", 2)},
		},
		{
			// The ordinal fallback must stop at the end of the locality list rather than run off it.
			name:       "no regions with more backendRefs than localities",
			refs:       []aigv1b1.AIGatewayRouteRuleBackendRef{ref("a"), ref("b"), ref("c")},
			localities: []*endpointv3.LocalityLbEndpoints{egLocality(-1)},
			want:       []string{wantName("a", 0)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLocalityTestServer(t, logr.Discard(), tc.refs...)
			cluster := &clusterv3.Cluster{
				Name:           localityTestCluster,
				LoadAssignment: &endpointv3.ClusterLoadAssignment{Endpoints: tc.localities},
			}
			var err error
			require.NotPanics(t, func() { err = s.maybeModifyCluster(t.Context(), cluster) })
			require.NoError(t, err)

			for i, want := range tc.want {
				require.Equal(t, want, backendNameOf(cluster.LoadAssignment.Endpoints[i]), "locality %d", i)
			}
		})
	}
}

// TestMaybeModifyClusterLocalitySkewPriority checks that the priority of a backendRef lands on the
// locality that backend actually owns, not on whichever one happens to sit at the same offset.
func TestMaybeModifyClusterLocalitySkewPriority(t *testing.T) {
	s := newLocalityTestServer(t, logr.Discard(),
		aigv1b1.AIGatewayRouteRuleBackendRef{Name: "a", Priority: ptr.To[uint32](0)},
		aigv1b1.AIGatewayRouteRuleBackendRef{Name: "b", Priority: ptr.To[uint32](1)},
		aigv1b1.AIGatewayRouteRuleBackendRef{Name: "c", Priority: ptr.To[uint32](2)},
	)
	cluster := &clusterv3.Cluster{
		Name: localityTestCluster,
		LoadAssignment: &endpointv3.ClusterLoadAssignment{Endpoints: []*endpointv3.LocalityLbEndpoints{
			egLocality(2),
		}},
	}
	require.NoError(t, s.maybeModifyCluster(t.Context(), cluster))
	require.Equal(t, uint32(2), cluster.LoadAssignment.Endpoints[0].Priority)
	require.Equal(t, wantName("c", 2), backendNameOf(cluster.LoadAssignment.Endpoints[0]))
}

// TestMaybeModifyClusterLocalitySkewLogs checks that a locality no backendRef accounts for is
// reported, since it leaves the upstream ext_proc filter unable to resolve the backend.
func TestMaybeModifyClusterLocalitySkewLogs(t *testing.T) {
	var buf bytes.Buffer
	s := newLocalityTestServer(t, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), ref("a"))

	cluster := &clusterv3.Cluster{
		Name: localityTestCluster,
		LoadAssignment: &endpointv3.ClusterLoadAssignment{Endpoints: []*endpointv3.LocalityLbEndpoints{
			egLocality(4),
		}},
	}
	require.NoError(t, s.maybeModifyCluster(t.Context(), cluster))

	require.Empty(t, backendNameOf(cluster.LoadAssignment.Endpoints[0]))
	require.Contains(t, buf.String(), "no AIGatewayRoute backendRef matches this endpoint locality")
	require.Contains(t, buf.String(), "locality_region="+localityTestCluster+"/backend/4")
}

// TestMaybeModifyClusterNilLoadAssignmentSingleBackend covers standalone mode (aigw run), where
// EDS-managed endpoints leave LoadAssignment nil and the cluster-level metadata fallback is the
// only way the upstream ext_proc filter can resolve the backend.
func TestMaybeModifyClusterNilLoadAssignmentSingleBackend(t *testing.T) {
	s := newLocalityTestServer(t, logr.Discard(), ref("only"))
	cluster := &clusterv3.Cluster{Name: localityTestCluster}
	require.NoError(t, s.maybeModifyCluster(t.Context(), cluster))

	require.Equal(t, wantName("only", 0),
		cluster.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace].
			Fields[internalapi.InternalMetadataBackendNameKey].GetStringValue())
}
