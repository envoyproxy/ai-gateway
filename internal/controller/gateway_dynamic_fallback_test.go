// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// TestGatewayController_reconcileFilterConfigSecret_DynamicFallback verifies that an annotated
// route turns on the gateway-wide extproc flag and emits composed-keyed backend entries (rule
// key + backend key) alongside the per-rule-ref entries, while non-annotated routes contribute
// neither.
func TestGatewayController_reconcileFilterConfigSecret_DynamicFallback(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()
	c := newTestGatewayController(fakeClient, kube, ctrl.Log, "envoy-gateway-system",
		"docker.io/envoyproxy/ai-gateway-extproc:latest", "info", false, nil, true)

	const ns = "ns"
	for _, backend := range []string{"apple", "orange"} {
		require.NoError(t, fakeClient.Create(t.Context(), &aigv1b1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: backend, Namespace: ns},
			Spec: aigv1b1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend", Namespace: ptr.To[gwapiv1.Namespace](ns)},
			},
		}))
	}

	newRoute := func(name string, annotated bool) aigv1b1.AIGatewayRoute {
		r := aigv1b1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: aigv1b1.AIGatewayRouteSpec{
				Rules: []aigv1b1.AIGatewayRouteRule{
					{
						BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{
							{Name: "apple", Alias: "apple-alias", Priority: ptr.To[uint32](0)},
							{Name: "orange", ModelNameOverride: "alt", Priority: ptr.To[uint32](1)},
						},
						Matches: []aigv1b1.AIGatewayRouteRuleMatch{
							{Headers: []gwapiv1.HTTPHeaderMatch{{Name: internalapi.ModelNameHeaderKeyDefault, Value: "mymodel"}}},
						},
					},
				},
			},
		}
		if annotated {
			r.Annotations = map[string]string{internalapi.DynamicFallbackAnnotationKey: "true"}
		}
		return r
	}

	reconcile := func(t *testing.T, gwName string, routes []aigv1b1.AIGatewayRoute) *filterapi.Config {
		const someNamespace = "some-namespace"
		effective, err := c.reconcileFilterConfigSecret(t.Context(), gwName, ns, someNamespace, routes, nil, "uuid", nil)
		require.NoError(t, err)
		require.True(t, effective)
		secret, err := kube.CoreV1().Secrets(someNamespace).Get(t.Context(), FilterConfigBundleIndexSecretName(gwName, ns), metav1.GetOptions{})
		require.NoError(t, err)
		indexRaw := secret.Data[FilterConfigBundleIndexKey]
		if len(indexRaw) == 0 {
			indexRaw = []byte(secret.StringData[FilterConfigBundleIndexKey])
		}
		index, err := filterapi.UnmarshalConfigBundleIndex(indexRaw)
		require.NoError(t, err)
		cfg, err := filterapi.ReassembleBundleConfig(index, func(part filterapi.ConfigBundlePart) ([]byte, error) {
			partSecret, getErr := kube.CoreV1().Secrets(someNamespace).Get(t.Context(), part.Name, metav1.GetOptions{})
			if getErr != nil {
				return nil, getErr
			}
			if b, exists := partSecret.Data[FilterConfigBundlePartKey]; exists {
				return b, nil
			}
			if b, exists := partSecret.StringData[FilterConfigBundlePartKey]; exists {
				return []byte(b), nil
			}
			return nil, fmt.Errorf("missing key %q in part secret %s", FilterConfigBundlePartKey, part.Name)
		})
		require.NoError(t, err)
		return cfg
	}

	backendNames := func(cfg *filterapi.Config) []string {
		names := make([]string, 0, len(cfg.Backends))
		for _, b := range cfg.Backends {
			names = append(names, b.Name)
		}
		return names
	}

	t.Run("annotated route emits composed entries", func(t *testing.T) {
		cfg := reconcile(t, "gw-dyn", []aigv1b1.AIGatewayRoute{newRoute("dynroute", true)})
		require.True(t, cfg.DynamicFallbackEnabled)
		names := backendNames(cfg)
		// Per-rule-ref entries are retained for the folded cluster...
		require.Contains(t, names, internalapi.PerRouteRuleRefBackendName(ns, "apple", "dynroute", 0, 0))
		require.Contains(t, names, internalapi.PerRouteRuleRefBackendName(ns, "orange", "dynroute", 0, 1))
		// ...and the composed entries serve the shared per-backend clusters.
		// The aliased ref's entry key carries the published name; the alias-less one equals
		// the plain backend key.
		appleKey := internalapi.DynamicFallbackFilterBackendName(
			internalapi.DynamicFallbackRuleKey(ns, "dynroute", 0),
			internalapi.DynamicFallbackEntryKey(ns, "apple", "apple-alias"))
		orangeKey := internalapi.DynamicFallbackFilterBackendName(
			internalapi.DynamicFallbackRuleKey(ns, "dynroute", 0),
			internalapi.DynamicFallbackEntryKey(ns, "orange", "orange"))
		require.Contains(t, names, appleKey)
		require.Contains(t, names, orangeKey)
		// The composed entry carries the rule-scoped config.
		for _, b := range cfg.Backends {
			if b.Name == orangeKey {
				require.Equal(t, "alt", b.ModelNameOverride)
			}
		}
		// The model surfaces the published vocabulary (alias preferred over resource name).
		require.Len(t, cfg.Models, 1)
		require.Equal(t, []string{"apple-alias", "orange"}, cfg.Models[0].FallbackCandidates)
	})

	t.Run("same-backend model refs emit per-entry composed configs", func(t *testing.T) {
		route := aigv1b1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "modelroute",
				Namespace:   ns,
				Annotations: map[string]string{internalapi.DynamicFallbackAnnotationKey: "true"},
			},
			Spec: aigv1b1.AIGatewayRouteSpec{
				Rules: []aigv1b1.AIGatewayRouteRule{
					{
						BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{
							{Name: "apple", Alias: "opus", ModelNameOverride: "claude-opus-4"},
							{Name: "apple", Alias: "sonnet", ModelNameOverride: "claude-sonnet-4", Priority: ptr.To[uint32](1)},
						},
						Matches: []aigv1b1.AIGatewayRouteRuleMatch{
							{Headers: []gwapiv1.HTTPHeaderMatch{{Name: internalapi.ModelNameHeaderKeyDefault, Value: "claude"}}},
						},
					},
				},
			},
		}
		cfg := reconcile(t, "gw-model", []aigv1b1.AIGatewayRoute{route})
		require.Equal(t, []string{"opus", "sonnet"}, cfg.Models[0].FallbackCandidates)
		ruleKey := internalapi.DynamicFallbackRuleKey(ns, "modelroute", 0)
		overridesByName := map[string]string{}
		for _, b := range cfg.Backends {
			overridesByName[b.Name] = b.ModelNameOverride
		}
		// Each entry resolves its own model override despite sharing the backend.
		require.Equal(t, "claude-opus-4",
			overridesByName[internalapi.DynamicFallbackFilterBackendName(ruleKey, internalapi.DynamicFallbackEntryKey(ns, "apple", "opus"))])
		require.Equal(t, "claude-sonnet-4",
			overridesByName[internalapi.DynamicFallbackFilterBackendName(ruleKey, internalapi.DynamicFallbackEntryKey(ns, "apple", "sonnet"))])
	})

	t.Run("non-annotated route emits neither flag nor composed entries", func(t *testing.T) {
		cfg := reconcile(t, "gw-plain", []aigv1b1.AIGatewayRoute{newRoute("plainroute", false)})
		require.False(t, cfg.DynamicFallbackEnabled)
		for _, name := range backendNames(cfg) {
			require.NotContains(t, name, "/backend/", "no composed entries expected: %s", name)
		}
		require.Len(t, cfg.Models, 1)
		require.Empty(t, cfg.Models[0].FallbackCandidates)
	})
}
