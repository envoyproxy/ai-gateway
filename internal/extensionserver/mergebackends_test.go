// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"slices"
	"strings"
	"testing"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// egResourceCluster builds a cluster with the Envoy Gateway resource metadata the given resource
// would produce. Mirrors internal/xds/translator/metadata.go.
func egResourceCluster(name, kind, namespace, resourceName, sectionName string) *clusterv3.Cluster {
	fields := map[string]*structpb.Value{
		egXdsMetadataKeyKind:      structpb.NewStringValue(kind),
		egXdsMetadataKeyNamespace: structpb.NewStringValue(namespace),
		egXdsMetadataKeyName:      structpb.NewStringValue(resourceName),
	}
	if sectionName != "" {
		fields[egXdsMetadataKeySectionName] = structpb.NewStringValue(sectionName)
	}
	return &clusterv3.Cluster{
		Name: name,
		Metadata: &corev3.Metadata{
			FilterMetadata: map[string]*structpb.Struct{
				egXdsMetadataNamespace: {
					Fields: map[string]*structpb.Value{
						egXdsMetadataKeyResources: structpb.NewListValue(&structpb.ListValue{
							Values: []*structpb.Value{structpb.NewStructValue(&structpb.Struct{Fields: fields})},
						}),
					},
				},
			},
		},
	}
}

func TestMergedClusterBackendKey(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cluster *clusterv3.Cluster
		want    mergedBackendKey
		merged  bool
	}{
		{
			// What AIServiceBackend produces: it may only reference an EG Backend, whose clusters
			// carry no port section.
			name:    "Backend",
			cluster: egResourceCluster("backend/default/openai/0", "Backend", "default", "openai", ""),
			want:    mergedBackendKey{namespace: "default", name: "openai"},
			merged:  true,
		},
		{
			// Envoy Gateway emits a Service-kind cluster for its own per-Gateway proxy service on
			// every translation, MergeBackends or not. Treating it as merged would make the index
			// non-empty when the feature is off and pull every AI route through the mapping path.
			name:    "Service is never an AIServiceBackend target",
			cluster: egResourceCluster("service/ns/svc/8080", "Service", "ns", "svc", "8080"),
			merged:  false,
		},
		{
			name:    "route cluster is not merged",
			cluster: egResourceCluster("httproute/default/myroute/rule/0", "HTTPRoute", "default", "myroute", ""),
			merged:  false,
		},
		{
			name:    "cluster without metadata is not merged",
			cluster: &clusterv3.Cluster{Name: "ai-gateway-extproc-uds"},
			merged:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, merged := mergedClusterBackendKey(tc.cluster)
			require.Equal(t, tc.merged, merged)
			if tc.merged {
				require.Equal(t, tc.want, got)
			}
		})
	}
}

func TestRouteActionClusterNames(t *testing.T) {
	t.Run("single cluster", func(t *testing.T) {
		action := &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: "backend/default/openai/0"}}
		require.Equal(t, []string{"backend/default/openai/0"}, routeActionClusterNames(action))
	})
	t.Run("weighted clusters mix merged and route-scoped entries", func(t *testing.T) {
		// EG emits unmerged Settings before merged BackendClusterRefs, so the order does not
		// follow backendRef order.
		action := &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_WeightedClusters{
			WeightedClusters: &routev3.WeightedCluster{Clusters: []*routev3.WeightedCluster_ClusterWeight{
				{Name: "httproute/default/myroute/rule/0/backend/1"},
				{Name: "backend/default/openai/0"},
			}},
		}}
		require.Equal(t, []string{
			"httproute/default/myroute/rule/0/backend/1",
			"backend/default/openai/0",
		}, routeActionClusterNames(action))
	})
	t.Run("non-forwarding action", func(t *testing.T) {
		require.Nil(t, routeActionClusterNames(&routev3.RouteAction{}))
	})
}

func TestParseAIGatewayRouteName(t *testing.T) {
	for _, tc := range []struct {
		in        string
		namespace string
		route     string
		ruleIndex int
		ok        bool
	}{
		{in: "httproute/default/myroute/rule/2/match/0", namespace: "default", route: "myroute", ruleIndex: 2, ok: true},
		{in: "httproute/default/myroute/rule/0", namespace: "default", route: "myroute", ruleIndex: 0, ok: true},
		{in: "httproute/default/myroute/rule/x/match/0"},
		{in: "httproute//myroute/rule/0/match/0"},
		{in: "grpcroute/default/myroute/rule/0/match/0"},
		{in: "backend/default/openai/0"},
		{in: ""},
	} {
		t.Run(tc.in, func(t *testing.T) {
			namespace, route, ruleIndex, ok := parseAIGatewayRouteName(tc.in)
			require.Equal(t, tc.ok, ok)
			if tc.ok {
				require.Equal(t, tc.namespace, namespace)
				require.Equal(t, tc.route, route)
				require.Equal(t, tc.ruleIndex, ruleIndex)
			}
		})
	}
}

// aigwRouteWithBackends creates an AIGatewayRoute whose single rule references the named
// AIServiceBackends, plus one AIServiceBackend each pointing at the given EG Backend.
func aigwRouteWithBackends(t *testing.T, s *Server, namespace, name string, backends map[string]string) {
	t.Helper()
	refs := make([]aigv1b1.AIGatewayRouteRuleBackendRef, 0, len(backends))
	// Sort by AIServiceBackend name for a deterministic backendRef order.
	names := make([]string, 0, len(backends))
	for aisb := range backends {
		names = append(names, aisb)
	}
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, aisb := range names {
		refs = append(refs, aigv1b1.AIGatewayRouteRuleBackendRef{Name: aisb})
		require.NoError(t, s.k8sClient.Create(t.Context(), &aigv1b1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: aisb, Namespace: namespace},
			Spec: aigv1b1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{
					Group: ptr.To[gwapiv1.Group]("gateway.envoyproxy.io"),
					Kind:  ptr.To[gwapiv1.Kind]("Backend"),
					Name:  gwapiv1.ObjectName(backends[aisb]),
				},
			},
		}))
	}
	require.NoError(t, s.k8sClient.Create(t.Context(), &aigv1b1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       aigv1b1.AIGatewayRouteSpec{Rules: []aigv1b1.AIGatewayRouteRule{{BackendRefs: refs}}},
	}))
}

// aiGeneratedRoute builds an xDS route carrying the AI Gateway generated annotation, so
// isRouteGeneratedByAIGateway accepts it.
func aiGeneratedRoute(name string, clusters ...string) *routev3.Route {
	var action *routev3.RouteAction
	if len(clusters) == 1 {
		action = &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: clusters[0]}}
	} else {
		weighted := make([]*routev3.WeightedCluster_ClusterWeight, 0, len(clusters))
		for _, c := range clusters {
			weighted = append(weighted, &routev3.WeightedCluster_ClusterWeight{Name: c})
		}
		action = &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_WeightedClusters{
			WeightedClusters: &routev3.WeightedCluster{Clusters: weighted},
		}}
	}
	return &routev3.Route{
		Name:   name,
		Action: &routev3.Route_Route{Route: action},
		Metadata: &corev3.Metadata{FilterMetadata: map[string]*structpb.Struct{
			egXdsMetadataNamespace: {Fields: map[string]*structpb.Value{
				egXdsMetadataKeyResources: structpb.NewListValue(&structpb.ListValue{
					Values: []*structpb.Value{structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{
						egXdsMetadataKeyKind:      structpb.NewStringValue("HTTPRoute"),
						egXdsMetadataKeyNamespace: structpb.NewStringValue("default"),
						egXdsMetadataKeyName:      structpb.NewStringValue("myroute"),
						"annotations": structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{
							internalapi.AIGatewayGeneratedHTTPRouteAnnotation: structpb.NewStringValue("true"),
						}}),
					}})},
				}),
			}},
		}},
	}
}

func routeConfigOf(routes ...*routev3.Route) []*routev3.RouteConfiguration {
	return []*routev3.RouteConfiguration{{
		Name:         "rc",
		VirtualHosts: []*routev3.VirtualHost{{Name: "vh", Domains: []string{"*"}, Routes: routes}},
	}}
}

// mergedBackendNamesOf reads back the mapping applyMergedBackendRouting stamped on the route.
func mergedBackendNamesOf(t *testing.T, route *routev3.Route) map[string]string {
	t.Helper()
	md := route.GetMetadata().GetFilterMetadata()[internalapi.InternalEndpointMetadataNamespace]
	value, ok := md.GetFields()[internalapi.InternalMetadataMergedBackendNamesKey]
	if !ok {
		return nil
	}
	encoded := value.GetStringValue()
	require.NotEmpty(t, encoded, "the mapping must be a non-empty string: Envoy delivers struct-valued metadata to ext_proc as the literal \"CelMap value\"")
	out := map[string]string{}
	for _, pair := range strings.Split(encoded, ";") {
		cluster, backend, found := strings.Cut(pair, "=")
		require.True(t, found, "malformed entry %q", pair)
		out[cluster] = backend
	}
	return out
}

// mergedClusterNames returns the sorted names of the merged clusters applyMergedBackendRouting
// reported as reachable from an AIGatewayRoute.
func mergedClusterNames(used map[string]*mergedClusterUse) []string {
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func newMergeTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(newFakeClient(), testr.New(t), udsPath, false, nil, nil,
		"envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)
	return s
}

func TestApplyMergedBackendRouting(t *testing.T) {
	t.Run("no merged clusters is a no-op", func(t *testing.T) {
		s := newMergeTestServer(t)
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{"openai": "openai-backend"})

		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", "httproute/default/myroute/rule/0")
		referenced, err := s.applyMergedBackendRouting(t.Context(),
			[]*clusterv3.Cluster{egResourceCluster("httproute/default/myroute/rule/0", "HTTPRoute", "default", "myroute", "")},
			routeConfigOf(route), nil)
		require.NoError(t, err)
		require.Empty(t, referenced)
		require.Nil(t, mergedBackendNamesOf(t, route), "route metadata must be untouched when MergeBackends is off")
	})

	t.Run("single merged backend", func(t *testing.T) {
		s := newMergeTestServer(t)
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{"openai": "openai-backend"})

		const merged = "backend/default/openai-backend/0"
		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", merged)
		referenced, err := s.applyMergedBackendRouting(t.Context(),
			[]*clusterv3.Cluster{egResourceCluster(merged, "Backend", "default", "openai-backend", "")},
			routeConfigOf(route), nil)
		require.NoError(t, err)

		require.Equal(t, []string{merged}, mergedClusterNames(referenced))
		require.Equal(t, map[string]string{
			merged: internalapi.PerRouteRuleRefBackendName("default", "openai", "myroute", 0, 0),
		}, mergedBackendNamesOf(t, route))
	})

	t.Run("weighted rule maps each merged cluster to its own backendRef", func(t *testing.T) {
		s := newMergeTestServer(t)
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{
			"anthropic": "anthropic-backend",
			"openai":    "openai-backend",
		})

		const (
			mergedAnthropic = "backend/default/anthropic-backend/0"
			mergedOpenAI    = "backend/default/openai-backend/0"
		)
		// Deliberately listed in the opposite order to the backendRefs, since Envoy Gateway does
		// not emit weighted entries in backendRef order.
		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", mergedOpenAI, mergedAnthropic)
		referenced, err := s.applyMergedBackendRouting(t.Context(), []*clusterv3.Cluster{
			egResourceCluster(mergedOpenAI, "Backend", "default", "openai-backend", ""),
			egResourceCluster(mergedAnthropic, "Backend", "default", "anthropic-backend", ""),
		}, routeConfigOf(route), nil)
		require.NoError(t, err)

		require.Len(t, referenced, 2)
		// backendRefs are sorted by AIServiceBackend name: anthropic is ref 0, openai is ref 1.
		require.Equal(t, map[string]string{
			mergedAnthropic: internalapi.PerRouteRuleRefBackendName("default", "anthropic", "myroute", 0, 0),
			mergedOpenAI:    internalapi.PerRouteRuleRefBackendName("default", "openai", "myroute", 0, 1),
		}, mergedBackendNamesOf(t, route))
	})

	t.Run("mixed rule leaves the route-scoped cluster alone", func(t *testing.T) {
		s := newMergeTestServer(t)
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{
			"anthropic": "anthropic-backend",
			"openai":    "openai-backend",
		})

		const (
			merged      = "backend/default/openai-backend/0"
			routeScoped = "httproute/default/myroute/rule/0/backend/0"
		)
		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", routeScoped, merged)
		referenced, err := s.applyMergedBackendRouting(t.Context(), []*clusterv3.Cluster{
			egResourceCluster(routeScoped, "HTTPRoute", "default", "myroute", ""),
			egResourceCluster(merged, "Backend", "default", "openai-backend", ""),
		}, routeConfigOf(route), nil)
		require.NoError(t, err)

		require.Equal(t, []string{merged}, mergedClusterNames(referenced))
		// The route-scoped cluster resolves via endpoint metadata, so it is not mapped.
		require.Equal(t, map[string]string{
			merged: internalapi.PerRouteRuleRefBackendName("default", "openai", "myroute", 0, 1),
		}, mergedBackendNamesOf(t, route))
	})

	t.Run("two AIServiceBackends on one Backend are left unmapped but still claimed", func(t *testing.T) {
		s := newMergeTestServer(t)
		// One cluster, and no way to tell which one's schema or credentials to apply.
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{
			"openai-a": "shared-backend",
			"openai-b": "shared-backend",
		})

		const merged = "backend/default/shared-backend/0"
		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", merged)
		referenced, err := s.applyMergedBackendRouting(t.Context(),
			[]*clusterv3.Cluster{egResourceCluster(merged, "Backend", "default", "shared-backend", "")},
			routeConfigOf(route), nil)
		require.NoError(t, err)

		// No mapping can be written, but the cluster is still claimed so the upstream filters are
		// installed and the request fails in the external processor. Leaving it unclaimed would
		// send it to the provider with neither credentials nor schema translation.
		require.Equal(t, []string{merged}, mergedClusterNames(referenced))
		require.Nil(t, mergedBackendNamesOf(t, route))
	})

	t.Run("a weight-0 duplicate does not make the rule ambiguous", func(t *testing.T) {
		s := newMergeTestServer(t)
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{
			"openai-a": "shared-backend",
			"openai-b": "shared-backend",
		})
		// Envoy Gateway drops a weight-0 destination entirely, so only openai-b can be behind the
		// cluster and the mapping is unambiguous.
		var fetched aigv1b1.AIGatewayRoute
		require.NoError(t, s.k8sClient.Get(t.Context(), client.ObjectKey{Namespace: "default", Name: "myroute"}, &fetched))
		fetched.Spec.Rules[0].BackendRefs[0].Weight = ptr.To[int32](0)
		require.NoError(t, s.k8sClient.Update(t.Context(), &fetched))

		const merged = "backend/default/shared-backend/0"
		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", merged)
		referenced, err := s.applyMergedBackendRouting(t.Context(),
			[]*clusterv3.Cluster{egResourceCluster(merged, "Backend", "default", "shared-backend", "")},
			routeConfigOf(route), nil)
		require.NoError(t, err)

		require.Equal(t, []string{merged}, mergedClusterNames(referenced))
		require.Equal(t, map[string]string{
			merged: internalapi.PerRouteRuleRefBackendName("default", "openai-b", "myroute", 0, 1),
		}, mergedBackendNamesOf(t, route))
	})

	t.Run("a repeated backendRef maps to the first ref", func(t *testing.T) {
		s := newMergeTestServer(t)
		// The documented same-provider model fallback: one AIServiceBackend listed twice, the
		// second only overriding the model. Credentials and schema are identical, so the first ref
		// is a safe answer and the rule keeps working.
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{"openai": "openai-backend"})
		var fetched aigv1b1.AIGatewayRoute
		require.NoError(t, s.k8sClient.Get(t.Context(), client.ObjectKey{Namespace: "default", Name: "myroute"}, &fetched))
		fetched.Spec.Rules[0].BackendRefs = append(fetched.Spec.Rules[0].BackendRefs,
			aigv1b1.AIGatewayRouteRuleBackendRef{Name: "openai", ModelNameOverride: "gpt-5-nano-mini"})
		require.NoError(t, s.k8sClient.Update(t.Context(), &fetched))

		const merged = "backend/default/openai-backend/0"
		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", merged)
		referenced, err := s.applyMergedBackendRouting(t.Context(),
			[]*clusterv3.Cluster{egResourceCluster(merged, "Backend", "default", "openai-backend", "")},
			routeConfigOf(route), nil)
		require.NoError(t, err)

		require.Equal(t, []string{merged}, mergedClusterNames(referenced))
		require.Equal(t, map[string]string{
			merged: internalapi.PerRouteRuleRefBackendName("default", "openai", "myroute", 0, 0),
		}, mergedBackendNamesOf(t, route))
	})

	t.Run("two clusters claiming one backend object resolve neither", func(t *testing.T) {
		s := newMergeTestServer(t)
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{"openai": "openai-backend"})

		// Envoy Gateway keys clusters on port and protocol too, but records neither on a Backend's
		// metadata, so two clusters for one Backend are indistinguishable here.
		const (
			onPort443  = "backend/default/openai-backend/443"
			onPort8443 = "backend/default/openai-backend/8443"
		)
		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", onPort443, onPort8443)
		referenced, err := s.applyMergedBackendRouting(t.Context(), []*clusterv3.Cluster{
			egResourceCluster(onPort443, "Backend", "default", "openai-backend", ""),
			egResourceCluster(onPort8443, "Backend", "default", "openai-backend", ""),
		}, routeConfigOf(route), nil)
		require.NoError(t, err)

		// Both claimed so the filters go on and requests fail closed, but neither is mapped:
		// guessing would send one port's traffic under the other's identity.
		require.Equal(t, []string{onPort443, onPort8443}, mergedClusterNames(referenced))
		require.Nil(t, mergedBackendNamesOf(t, route))
	})

	t.Run("an ambiguous backend does not drop the rule's other clusters", func(t *testing.T) {
		s := newMergeTestServer(t)
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{
			"openai-a":  "shared-backend",
			"openai-b":  "shared-backend",
			"anthropic": "anthropic-backend",
		})

		const (
			ambiguous = "backend/default/shared-backend/0"
			resolved  = "backend/default/anthropic-backend/0"
		)
		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", ambiguous, resolved)
		referenced, err := s.applyMergedBackendRouting(t.Context(), []*clusterv3.Cluster{
			egResourceCluster(ambiguous, "Backend", "default", "shared-backend", ""),
			egResourceCluster(resolved, "Backend", "default", "anthropic-backend", ""),
		}, routeConfigOf(route), nil)
		require.NoError(t, err)

		require.Equal(t, []string{resolved, ambiguous}, mergedClusterNames(referenced))
		// anthropic is backendRef 0; only the shared pair is unresolvable.
		require.Equal(t, map[string]string{
			resolved: internalapi.PerRouteRuleRefBackendName("default", "anthropic", "myroute", 0, 0),
		}, mergedBackendNamesOf(t, route))
	})

	t.Run("merged cluster used only by a non-AIGatewayRoute is not claimed", func(t *testing.T) {
		s := newMergeTestServer(t)

		const merged = "backend/default/shared-backend/0"
		// No AI Gateway annotation, so isRouteGeneratedByAIGateway rejects it.
		plain := &routev3.Route{
			Name:   "httproute/default/plain/rule/0/match/0",
			Action: &routev3.Route_Route{Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: merged}}},
		}
		referenced, err := s.applyMergedBackendRouting(t.Context(),
			[]*clusterv3.Cluster{egResourceCluster(merged, "Backend", "default", "shared-backend", "")},
			routeConfigOf(plain), nil)
		require.NoError(t, err)
		require.Empty(t, referenced, "a cluster no AIGatewayRoute uses must not be modified")
	})

	t.Run("missing AIServiceBackend is skipped", func(t *testing.T) {
		s := newMergeTestServer(t)
		require.NoError(t, s.k8sClient.Create(t.Context(), &aigv1b1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
			Spec: aigv1b1.AIGatewayRouteSpec{Rules: []aigv1b1.AIGatewayRouteRule{
				{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "gone"}}},
			}},
		}))

		const merged = "backend/default/openai-backend/0"
		route := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", merged)
		referenced, err := s.applyMergedBackendRouting(t.Context(),
			[]*clusterv3.Cluster{egResourceCluster(merged, "Backend", "default", "openai-backend", "")},
			routeConfigOf(route), nil)
		require.NoError(t, err)
		// Claimed without a mapping: the rule is an AIGatewayRoute's, so its traffic must fail in
		// the external processor rather than reach the provider unprocessed.
		require.Equal(t, []string{merged}, mergedClusterNames(referenced))
		require.Nil(t, mergedBackendNamesOf(t, route))
	})

	t.Run("rule index beyond the current spec is skipped", func(t *testing.T) {
		s := newMergeTestServer(t)
		aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{"openai": "openai-backend"})

		const merged = "backend/default/openai-backend/0"
		route := aiGeneratedRoute("httproute/default/myroute/rule/7/match/0", merged)
		referenced, err := s.applyMergedBackendRouting(t.Context(),
			[]*clusterv3.Cluster{egResourceCluster(merged, "Backend", "default", "openai-backend", "")},
			routeConfigOf(route), nil)
		require.NoError(t, err)
		// The rule is gone from the spec but the Envoy route is still an AIGatewayRoute's, so the
		// cluster is claimed and the filters installed: its traffic must fail in the external
		// processor, not reach the provider unprocessed.
		require.Equal(t, []string{merged}, mergedClusterNames(referenced))
		require.Nil(t, mergedBackendNamesOf(t, route))
	})
}

// TestPostTranslateModify_MergedBackendCluster checks the full entry point: the external processor
// lands on a merged cluster an AIGatewayRoute uses, and not on one only a plain HTTPRoute uses,
// where it would break that route's traffic.
func TestPostTranslateModify_MergedBackendCluster(t *testing.T) {
	s := newMergeTestServer(t)
	aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{"openai": "openai-backend"})

	const (
		aiMerged    = "backend/default/openai-backend/0"
		plainMerged = "backend/default/other-backend/0"
	)
	aiRoute := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", aiMerged)
	plainRoute := &routev3.Route{
		Name:   "httproute/default/plain/rule/0/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: plainMerged}}},
	}

	resp, err := s.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{
		Clusters: []*clusterv3.Cluster{
			egResourceCluster(aiMerged, "Backend", "default", "openai-backend", ""),
			egResourceCluster(plainMerged, "Backend", "default", "other-backend", ""),
		},
		Routes: routeConfigOf(aiRoute, plainRoute),
	})
	require.NoError(t, err)

	byName := map[string]*clusterv3.Cluster{}
	for _, c := range resp.Clusters {
		byName[c.Name] = c
	}
	require.True(t, hasUpstreamExtProc(t, byName[aiMerged]),
		"a merged cluster an AIGatewayRoute routes to must get the upstream external processor")
	require.False(t, hasUpstreamExtProc(t, byName[plainMerged]),
		"a merged cluster only a plain HTTPRoute uses must be left alone")
}

// TestPostTranslateModify_MergedBackendClusterSharedWithPlainRoute covers the case MergeBackends
// exists to produce: one cluster reached by both an AIGatewayRoute and a plain HTTPRoute. The
// filter is installed cluster-wide and cannot be disabled per route, so the plain route's requests
// reach the external processor too — they carry no route metadata and are passed through there.
func TestPostTranslateModify_MergedBackendClusterSharedWithPlainRoute(t *testing.T) {
	s := newMergeTestServer(t)
	aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{"openai": "openai-backend"})

	const shared = "backend/default/openai-backend/0"
	aiRoute := aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", shared)
	plainRoute := &routev3.Route{
		Name:   "httproute/default/plain/rule/0/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: shared}}},
	}

	resp, err := s.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{
		Clusters: []*clusterv3.Cluster{egResourceCluster(shared, "Backend", "default", "openai-backend", "")},
		Routes:   routeConfigOf(aiRoute, plainRoute),
	})
	require.NoError(t, err)
	require.True(t, hasUpstreamExtProc(t, resp.Clusters[0]))

	// Only the AI route carries the mapping; the plain route is left untouched, which is what the
	// external processor keys on to pass its traffic through.
	require.Equal(t, map[string]string{
		shared: internalapi.PerRouteRuleRefBackendName("default", "openai", "myroute", 0, 0),
	}, mergedBackendNamesOf(t, aiRoute))
	require.Nil(t, mergedBackendNamesOf(t, plainRoute))
	require.Nil(t, plainRoute.Metadata, "a route AI Gateway does not own must not be annotated")
}

// TestPostTranslateModify_MergedBackendClusterForwardProxy pins that a merged cluster still gets
// the GatewayConfig forward proxy. Skipping it would silently route provider egress around a proxy
// the operator mandated.
func TestPostTranslateModify_MergedBackendClusterForwardProxy(t *testing.T) {
	s := newMergeTestServer(t)
	aigwRouteWithBackends(t, s, "default", "myroute", map[string]string{"openai": "openai-backend"})

	// Point the route at a Gateway whose GatewayConfig configures a forward proxy.
	var route aigv1b1.AIGatewayRoute
	require.NoError(t, s.k8sClient.Get(t.Context(), client.ObjectKey{Namespace: "default", Name: "myroute"}, &route))
	route.Spec.ParentRefs = []gwapiv1.ParentReference{{Name: "gw"}}
	require.NoError(t, s.k8sClient.Update(t.Context(), &route))
	require.NoError(t, s.k8sClient.Create(t.Context(), &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gw", Namespace: "default",
			Annotations: map[string]string{gatewayConfigAnnotationKey: "gwconfig"},
		},
	}))
	require.NoError(t, s.k8sClient.Create(t.Context(), &aigv1b1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "gwconfig", Namespace: "default"},
		Spec: aigv1b1.GatewayConfigSpec{
			ForwardProxy: &aigv1b1.GatewayConfigForwardProxy{Address: "proxy.example.com:3128"},
		},
	}))

	const merged = "backend/default/openai-backend/0"
	resp, err := s.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{
		Clusters: []*clusterv3.Cluster{egResourceCluster(merged, "Backend", "default", "openai-backend", "")},
		Routes:   routeConfigOf(aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", merged)),
	})
	require.NoError(t, err)
	require.Equal(t, http11ProxyTransportSocketName, resp.Clusters[0].TransportSocket.GetName())

	// A route AI Gateway does not own shares the cluster: the transport socket is cluster-wide, so
	// applying the proxy would tunnel that route's egress through a proxy it never configured.
	plain := &routev3.Route{
		Name:   "httproute/default/plain/rule/0/match/0",
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: merged}}},
	}
	shared, err := s.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{
		Clusters: []*clusterv3.Cluster{egResourceCluster(merged, "Backend", "default", "openai-backend", "")},
		Routes:   routeConfigOf(aiGeneratedRoute("httproute/default/myroute/rule/0/match/0", merged), plain),
	})
	require.NoError(t, err)
	require.Nil(t, shared.Clusters[0].TransportSocket,
		"a cluster shared with a route AI Gateway does not own must not be wrapped")
	require.True(t, hasUpstreamExtProc(t, shared.Clusters[0]),
		"the AI route still needs the filters even when the proxy is withheld")
}

// hasUpstreamExtProc reports whether the cluster's upstream filter chain has the external processor.
func hasUpstreamExtProc(t *testing.T, cluster *clusterv3.Cluster) bool {
	t.Helper()
	require.NotNil(t, cluster)
	raw, ok := cluster.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
	if !ok {
		return false
	}
	po := &httpv3.HttpProtocolOptions{}
	require.NoError(t, raw.UnmarshalTo(po))
	for _, f := range po.HttpFilters {
		if f.Name == aiGatewayExtProcName {
			return true
		}
	}
	return false
}
