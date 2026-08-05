// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"fmt"
	"testing"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	fake2 "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/extensionserver"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func objectMeta(name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: namespace}
}

// readFilterConfig reassembles the external processor config the Gateway controller just wrote.
func readFilterConfig(t *testing.T, kube kubernetes.Interface, namespace, gatewayName string) *filterapi.Config {
	t.Helper()
	get := func(name string) ([]byte, error) {
		s, err := kube.CoreV1().Secrets(namespace).Get(t.Context(), name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if b, ok := s.Data[FilterConfigBundlePartKey]; ok {
			return b, nil
		}
		if b, ok := s.StringData[FilterConfigBundlePartKey]; ok {
			return []byte(b), nil
		}
		if b, ok := s.Data[FilterConfigBundleIndexKey]; ok {
			return b, nil
		}
		if b, ok := s.StringData[FilterConfigBundleIndexKey]; ok {
			return []byte(b), nil
		}
		return nil, fmt.Errorf("no config data in secret %s", name)
	}
	raw, err := get(FilterConfigBundleIndexSecretName(gatewayName, namespace))
	require.NoError(t, err)
	index, err := filterapi.UnmarshalConfigBundleIndex(raw)
	require.NoError(t, err)
	cfg, err := filterapi.ReassembleBundleConfig(index, func(part filterapi.ConfigBundlePart) ([]byte, error) {
		return get(part.Name)
	})
	require.NoError(t, err)
	return cfg
}

// clustersForHTTPRoute models how Envoy Gateway translates one HTTPRoute rule into clusters, which
// is what the seam test needs in order to feed the extension server something shaped like the real
// thing.
//
// Envoy Gateway emits a single cluster per rule and distinguishes the backends inside it by
// locality, unless any of them is invalid or has no endpoints, in which case it emits one cluster
// per backend instead (NeedsClusterPerSetting in its internal/ir/xds.go). Either way it records
// which backendRef a set of endpoints came from: in locality.region for the shared cluster, and in
// the cluster name itself for the per-backend ones.
func clustersForHTTPRoute(t *testing.T, httpRoute *gwapiv1.HTTPRoute, ruleIndex int, invalid map[int]bool) []*clusterv3.Cluster {
	t.Helper()
	rule := httpRoute.Spec.Rules[ruleIndex]
	base := fmt.Sprintf("httproute/%s/%s/rule/%d", httpRoute.Namespace, httpRoute.Name, ruleIndex)

	var anyInvalid bool
	for j := range rule.BackendRefs {
		if invalid[j] {
			anyInvalid = true
		}
	}

	if !anyInvalid {
		shared := &clusterv3.Cluster{
			Name:           base,
			LoadAssignment: &endpointv3.ClusterLoadAssignment{ClusterName: base},
		}
		for j, br := range rule.BackendRefs {
			if br.Weight != nil && *br.Weight == 0 {
				continue // Envoy Gateway leaves zero-weight backends out of the LoadAssignment.
			}
			shared.LoadAssignment.Endpoints = append(shared.LoadAssignment.Endpoints, &endpointv3.LocalityLbEndpoints{
				Locality:    &corev3.Locality{Region: fmt.Sprintf("%s/backend/%d", base, j)},
				LbEndpoints: []*endpointv3.LbEndpoint{{}},
			})
		}
		return []*clusterv3.Cluster{shared}
	}

	clusters := make([]*clusterv3.Cluster, 0, len(rule.BackendRefs))
	for j, br := range rule.BackendRefs {
		if br.Weight != nil && *br.Weight == 0 {
			continue
		}
		name := fmt.Sprintf("%s/backend/%d", base, j)
		c := &clusterv3.Cluster{
			Name:           name,
			LoadAssignment: &endpointv3.ClusterLoadAssignment{ClusterName: name},
		}
		if !invalid[j] {
			c.LoadAssignment.Endpoints = []*endpointv3.LocalityLbEndpoints{{
				Locality:    &corev3.Locality{Region: name},
				LbEndpoints: []*endpointv3.LbEndpoint{{}},
			}}
		}
		clusters = append(clusters, c)
	}
	return clusters
}

// labelledBackendNames collects the backend name the extension server stamped on every endpoint and
// cluster it touched.
func labelledBackendNames(clusters []*clusterv3.Cluster) []string {
	var names []string
	read := func(m *corev3.Metadata) {
		if v := m.GetFilterMetadata()[internalapi.InternalEndpointMetadataNamespace].
			GetFields()[internalapi.InternalMetadataBackendNameKey].GetStringValue(); v != "" {
			names = append(names, v)
		}
	}
	for _, c := range clusters {
		read(c.Metadata)
		for _, eps := range c.GetLoadAssignment().GetEndpoints() {
			for _, ep := range eps.LbEndpoints {
				read(ep.Metadata)
			}
		}
	}
	return names
}

// TestBackendRefIndexSeam pins the contract that ties the three components together.
//
// The Gateway controller names each external processor backend after its position in the
// AIGatewayRoute. The AIGatewayRoute controller generates the HTTPRoute that Envoy Gateway turns
// into clusters. The extension server labels the endpoints of those clusters with a name it derives
// the same way. At request time the external processor looks the label up in the config, so a name
// the extension server can produce but the config does not contain is a request that fails.
//
// Nothing else in the tree exercises both halves, which is how they were able to disagree: one
// unresolvable backendRef used to make the AIGatewayRoute controller abandon the HTTPRoute entirely
// while the Gateway controller silently skipped just that one entry.
func TestBackendRefIndexSeam(t *testing.T) {
	const ns = "ns"
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	// A rule mixing backends that resolve with one that does not.
	route := &aigv1b1.AIGatewayRoute{
		ObjectMeta: objectMeta("seamroute", ns),
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{{
				BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{
					{Name: "apple"}, {Name: "missing"}, {Name: "cherry"},
				},
			}},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), route))
	for _, n := range []string{"apple", "cherry"} {
		b := aiServiceBackend(n)
		b.Namespace = ns
		require.NoError(t, fakeClient.Create(t.Context(), b))
	}

	// Half one: the AIGatewayRoute controller generates the HTTPRoute.
	routeController := &AIGatewayRouteController{client: fakeClient, referenceGrantValidator: newReferenceGrantValidator(fakeClient)}
	httpRoute := &gwapiv1.HTTPRoute{ObjectMeta: objectMeta("seamroute", ns)}
	unresolved, err := routeController.newHTTPRoute(t.Context(), httpRoute, route)
	require.NoError(t, err)
	require.Len(t, unresolved, 1)
	require.Len(t, httpRoute.Spec.Rules[0].BackendRefs, 3, "the unresolvable ref must keep its slot")

	// Half two: the Gateway controller writes the external processor config.
	gwController := newTestGatewayController(fakeClient, kube, ctrl.Log, "envoy-gateway-system",
		"docker.io/envoyproxy/ai-gateway-extproc:latest", "info", false, nil, true)
	_, err = gwController.reconcileFilterConfigSecret(t.Context(), "gw", ns, ns, []aigv1b1.AIGatewayRoute{*route}, nil, "uuid", nil)
	require.NoError(t, err)
	cfg := readFilterConfig(t, kube, ns, "gw")

	configured := map[string]filterapi.Backend{}
	for _, b := range cfg.Backends {
		configured[b.Name] = b
	}
	// Every backendRef has an entry, including the one that could not be resolved. Without it the
	// request would fail as an unknown backend, which says nothing about what to fix.
	require.Len(t, configured, 3)
	require.Empty(t, configured[internalapi.PerRouteRuleRefBackendName(ns, "apple", "seamroute", 0, 0)].Unresolved)
	require.NotEmpty(t, configured[internalapi.PerRouteRuleRefBackendName(ns, "missing", "seamroute", 0, 1)].Unresolved)
	require.Empty(t, configured[internalapi.PerRouteRuleRefBackendName(ns, "cherry", "seamroute", 0, 2)].Unresolved)

	// Half three: the extension server labels the endpoints Envoy Gateway generated.
	extSrv, err := extensionserver.New(fakeClient, logr.Discard(), "/tmp/uds/test.sock", false, nil, nil,
		"envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)
	clusters := clustersForHTTPRoute(t, httpRoute, 0, map[int]bool{1: true})
	_, err = extSrv.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{Clusters: clusters})
	require.NoError(t, err)

	labelled := labelledBackendNames(clusters)
	require.NotEmpty(t, labelled)
	for _, name := range labelled {
		require.Contains(t, configured, name,
			"the extension server labelled an endpoint with a backend the external processor config does not have")
	}

	// And specifically, the backend after the unresolvable one is still itself rather than shifted
	// onto its neighbour's name.
	require.Contains(t, labelled, internalapi.PerRouteRuleRefBackendName(ns, "cherry", "seamroute", 0, 2))
	require.NotContains(t, labelled, internalapi.PerRouteRuleRefBackendName(ns, "cherry", "seamroute", 0, 1))
}

// TestBackendRefIndexSeamAllResolvable runs the same contract over a rule where every backendRef
// resolves, which is the shape that puts all the backends in one shared cluster.
func TestBackendRefIndexSeamAllResolvable(t *testing.T) {
	const ns = "ns"
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()

	route := &aigv1b1.AIGatewayRoute{
		ObjectMeta: objectMeta("goodroute", ns),
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{{
				BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "apple"}, {Name: "cherry"}},
			}},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), route))
	for _, n := range []string{"apple", "cherry"} {
		b := aiServiceBackend(n)
		b.Namespace = ns
		require.NoError(t, fakeClient.Create(t.Context(), b))
	}

	routeController := &AIGatewayRouteController{client: fakeClient, referenceGrantValidator: newReferenceGrantValidator(fakeClient)}
	httpRoute := &gwapiv1.HTTPRoute{ObjectMeta: objectMeta("goodroute", ns)}
	unresolved, err := routeController.newHTTPRoute(t.Context(), httpRoute, route)
	require.NoError(t, err)
	require.Empty(t, unresolved)

	gwController := newTestGatewayController(fakeClient, kube, ctrl.Log, "envoy-gateway-system",
		"docker.io/envoyproxy/ai-gateway-extproc:latest", "info", false, nil, true)
	_, err = gwController.reconcileFilterConfigSecret(t.Context(), "gw", ns, ns, []aigv1b1.AIGatewayRoute{*route}, nil, "uuid", nil)
	require.NoError(t, err)
	cfg := readFilterConfig(t, kube, ns, "gw")

	configured := map[string]filterapi.Backend{}
	for _, b := range cfg.Backends {
		configured[b.Name] = b
	}

	extSrv, err := extensionserver.New(fakeClient, logr.Discard(), "/tmp/uds/test.sock", false, nil, nil,
		"envoy-ai-gateway-ratelimit.envoy-gateway-system", 5, false)
	require.NoError(t, err)
	clusters := clustersForHTTPRoute(t, httpRoute, 0, nil)
	require.Len(t, clusters, 1, "every backend resolvable means one shared cluster")
	_, err = extSrv.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{Clusters: clusters})
	require.NoError(t, err)

	labelled := labelledBackendNames(clusters)
	require.Len(t, labelled, 2)
	for _, name := range labelled {
		require.Contains(t, configured, name)
	}
}
