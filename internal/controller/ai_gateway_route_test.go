// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestAIGatewayRouteController_Reconcile(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewAIGatewayRouteController(fakeClient, fake2.NewClientset(), ctrl.Log, eventCh.Ch)

	err := fakeClient.Create(t.Context(), &gwapiv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "mytarget", Namespace: "default"}})
	require.NoError(t, err)
	err = fakeClient.Create(t.Context(), &aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)

	// Do it for the second time with a slightly different configuration.
	var current aigv1a1.AIGatewayRoute
	err = fakeClient.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: "myroute"}, &current)
	require.NoError(t, err)
	current.Spec.APISchema = aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: ptr.To("v123")}
	current.Spec.TargetRefs = []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
	}
	err = fakeClient.Update(t.Context(), &current)
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)

	var updated aigv1a1.AIGatewayRoute
	err = fakeClient.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: "myroute"}, &updated)
	require.NoError(t, err)

	require.Equal(t, "myroute", updated.Name)
	require.Equal(t, "default", updated.Namespace)
	require.Len(t, updated.Spec.TargetRefs, 1)
	require.Equal(t, "mytarget", string(updated.Spec.TargetRefs[0].Name))
	require.Equal(t, aigv1a1.APISchemaOpenAI, updated.Spec.APISchema.Name)

	// Test the case where the AIGatewayRoute is being deleted.
	err = fakeClient.Delete(t.Context(), &aigv1a1.AIGatewayRoute{ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "myroute"}})
	require.NoError(t, err)
}

func requireNewFakeClientWithIndexes(t *testing.T) client.Client {
	builder := fake.NewClientBuilder().WithScheme(Scheme).
		WithStatusSubresource(&aigv1a1.AIGatewayRoute{}).
		WithStatusSubresource(&aigv1a1.AIServiceBackend{}).
		WithStatusSubresource(&aigv1a1.BackendSecurityPolicy{})
	err := ApplyIndexing(t.Context(), func(_ context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
		builder = builder.WithIndex(obj, field, extractValue)
		return nil
	})
	require.NoError(t, err)
	return builder.Build()
}

func TestAIGatewayRouterController_syncAIGatewayRoute(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	s := NewAIGatewayRouteController(fakeClient, kube, logr.Discard(), eventCh.Ch)
	require.NotNil(t, s)

	for _, backend := range []*aigv1a1.AIServiceBackend{
		{ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: "ns1"}, Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns1")},
		}},
	} {
		err := fakeClient.Create(t.Context(), backend, &client.CreateOptions{})
		require.NoError(t, err)
	}

	t.Run("existing", func(t *testing.T) {
		route := &aigv1a1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns1"},
			Spec: aigv1a1.AIGatewayRouteSpec{
				Rules: []aigv1a1.AIGatewayRouteRule{
					{
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "apple", Weight: ptr.To[int32](1)}, {Name: "orange", Weight: ptr.To[int32](1)}},
					},
				},
				APISchema: aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: ptr.To("v123")},
			},
		}
		err := fakeClient.Create(t.Context(), route, &client.CreateOptions{})
		require.NoError(t, err)
		httpRoute := &gwapiv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "ns1", Labels: map[string]string{managedByLabel: "envoy-ai-gateway"}},
			Spec:       gwapiv1.HTTPRouteSpec{},
		}
		err = fakeClient.Create(t.Context(), httpRoute, &client.CreateOptions{})
		require.NoError(t, err)

		// Then sync, which should update the HTTPRoute.
		require.NoError(t, s.syncAIGatewayRoute(t.Context(), route))
		var updatedHTTPRoute gwapiv1.HTTPRoute
		err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "myroute", Namespace: "ns1"}, &updatedHTTPRoute)
		require.NoError(t, err)
		require.Len(t, updatedHTTPRoute.Spec.Rules, 2) // 1 rule + 1 for the default rule.
		require.Len(t, updatedHTTPRoute.Spec.Rules[0].BackendRefs, 2)
		require.Equal(t, "some-backend1", string(updatedHTTPRoute.Spec.Rules[0].BackendRefs[0].BackendRef.Name))
		require.Equal(t, "some-backend2", string(updatedHTTPRoute.Spec.Rules[0].BackendRefs[1].BackendRef.Name))
		require.Equal(t, "myroute-rule-0", updatedHTTPRoute.Spec.Rules[0].Matches[0].Headers[0].Value)
		// Defaulting to the empty path, which shouldn't reach in practice.
		require.Empty(t, updatedHTTPRoute.Spec.Rules[1].BackendRefs)
		require.Equal(t, "/", *updatedHTTPRoute.Spec.Rules[1].Matches[0].Path.Value)
	})

	// Check the namespace has the default host rewrite filter.
	var f egv1a1.HTTPRouteFilter
	err := s.client.Get(t.Context(), client.ObjectKey{Name: hostRewriteHTTPFilterName, Namespace: "ns1"}, &f)
	require.NoError(t, err)
	require.Equal(t, hostRewriteHTTPFilterName, f.Name)
}

func Test_newHTTPRoute(t *testing.T) {
	for _, ns := range []string{"", "ns1"} {
		t.Run(fmt.Sprintf("namespace-%s", ns), func(t *testing.T) {
			var refNs *gwapiv1.Namespace
			if ns != "" {
				refNs = ptr.To(gwapiv1.Namespace(ns))
			}

			var (
				timeout1       gwapiv1.Duration = "30s"
				timeout2       gwapiv1.Duration = "60s"
				defaultTimeout gwapiv1.Duration = "60s"
			)
			fakeClient := requireNewFakeClientWithIndexes(t)
			eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
			s := NewAIGatewayRouteController(fakeClient, nil, logr.Discard(), eventCh.Ch)
			httpRoute := &gwapiv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: ns},
				Spec:       gwapiv1.HTTPRouteSpec{},
			}
			aiGatewayRoute := &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: ns},
				Spec: aigv1a1.AIGatewayRouteSpec{
					TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
						{
							LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
								Name: "gtw", Kind: "Gateway", Group: "gateway.networking.k8s.io",
							},
						},
					},
					Rules: []aigv1a1.AIGatewayRouteRule{
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "apple", Weight: ptr.To[int32](100)}},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
								{Name: "orange", Weight: ptr.To[int32](100), Priority: ptr.To[uint32](0)},
								{Name: "apple", Weight: ptr.To[int32](100), Priority: ptr.To[uint32](1)},
								{Name: "pineapple", Weight: ptr.To[int32](100), Priority: ptr.To[uint32](2)},
							},
						},
						{
							BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "foo", Weight: ptr.To[int32](1)}},
							Timeouts:    &gwapiv1.HTTPRouteTimeouts{Request: &timeout1, BackendRequest: &timeout2},
						},
					},
				},
			}

			for _, backend := range []*aigv1a1.AIServiceBackend{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "apple", Namespace: ns},
					Spec: aigv1a1.AIServiceBackendSpec{
						BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: refNs},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "orange", Namespace: ns},
					Spec: aigv1a1.AIServiceBackendSpec{
						BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: refNs},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pineapple", Namespace: ns},
					Spec: aigv1a1.AIServiceBackendSpec{
						BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: refNs},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: ns},
					Spec: aigv1a1.AIServiceBackendSpec{
						BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: refNs},
					},
				},
			} {
				err := s.client.Create(t.Context(), backend, &client.CreateOptions{})
				require.NoError(t, err)
			}
			err := s.newHTTPRoute(t.Context(), httpRoute, aiGatewayRoute)
			require.NoError(t, err)

			rewriteFilters := []gwapiv1.HTTPRouteFilter{{
				Type: gwapiv1.HTTPRouteFilterExtensionRef,
				ExtensionRef: &gwapiv1.LocalObjectReference{
					Group: "gateway.envoyproxy.io",
					Kind:  "HTTPRouteFilter",
					Name:  hostRewriteHTTPFilterName,
				},
			}}
			expRules := []gwapiv1.HTTPRouteRule{
				{
					Matches: []gwapiv1.HTTPRouteMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedRouteHeaderKey, Value: "myroute-rule-0"}}},
					},
					BackendRefs: []gwapiv1.HTTPBackendRef{{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: refNs}, Weight: ptr.To[int32](100)}}},
					Timeouts:    &gwapiv1.HTTPRouteTimeouts{Request: &defaultTimeout},
					Filters:     rewriteFilters,
				},
				{
					Matches: []gwapiv1.HTTPRouteMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedRouteHeaderKey, Value: "myroute-rule-1"}}},
					},
					BackendRefs: []gwapiv1.HTTPBackendRef{
						{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: refNs}, Weight: ptr.To[int32](100)}},
						{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: refNs}, Weight: ptr.To[int32](100)}},
						{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: refNs}, Weight: ptr.To[int32](100)}},
					},
					Timeouts: &gwapiv1.HTTPRouteTimeouts{Request: &defaultTimeout},
					Filters:  rewriteFilters,
				},
				{
					Matches: []gwapiv1.HTTPRouteMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: selectedRouteHeaderKey, Value: "myroute-rule-2"}}},
					},
					BackendRefs: []gwapiv1.HTTPBackendRef{{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: refNs}, Weight: ptr.To[int32](1)}}},
					Timeouts:    &gwapiv1.HTTPRouteTimeouts{Request: &timeout1, BackendRequest: &timeout2},
					Filters:     rewriteFilters,
				},
				{
					// The default rule.
					Name:    ptr.To[gwapiv1.SectionName]("unreachable"),
					Matches: []gwapiv1.HTTPRouteMatch{{Path: &gwapiv1.HTTPPathMatch{Value: ptr.To("/")}}},
				},
			}
			require.Equal(t, expRules, httpRoute.Spec.Rules)
		})
	}
}

func TestAIGatewayRouteController_updateAIGatewayRouteStatus(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	s := NewAIGatewayRouteController(fakeClient, kube, logr.Discard(), eventCh.Ch)

	r := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route1",
			Namespace: "default",
		},
	}
	err := s.client.Create(t.Context(), r, &client.CreateOptions{})
	require.NoError(t, err)

	s.updateAIGatewayRouteStatus(t.Context(), r, aigv1a1.ConditionTypeNotAccepted, "err")

	var updatedRoute aigv1a1.AIGatewayRoute
	err = s.client.Get(t.Context(), client.ObjectKey{Name: "route1", Namespace: "default"}, &updatedRoute)
	require.NoError(t, err)
	require.Len(t, updatedRoute.Status.Conditions, 1)
	require.Equal(t, "err", updatedRoute.Status.Conditions[0].Message)
	require.Equal(t, aigv1a1.ConditionTypeNotAccepted, updatedRoute.Status.Conditions[0].Type)

	s.updateAIGatewayRouteStatus(t.Context(), &updatedRoute, aigv1a1.ConditionTypeAccepted, "ok")
	err = s.client.Get(t.Context(), client.ObjectKey{Name: "route1", Namespace: "default"}, &updatedRoute)
	require.NoError(t, err)
	require.Len(t, updatedRoute.Status.Conditions, 1)
	require.Equal(t, "ok", updatedRoute.Status.Conditions[0].Message)
	require.Equal(t, aigv1a1.ConditionTypeAccepted, updatedRoute.Status.Conditions[0].Type)
}

func Test_buildPriorityAnnotation(t *testing.T) {
	rules := []aigv1a1.AIGatewayRouteRule{
		{
			BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
				{Name: "orange", Weight: ptr.To[int32](100), Priority: ptr.To[uint32](0)},
				{Name: "apple", Weight: ptr.To[int32](100), Priority: ptr.To[uint32](1)},
				{Name: "pineapple", Weight: ptr.To[int32](100), Priority: ptr.To[uint32](2)},
			},
		},
	}
	annotation := buildPriorityAnnotation(rules)
	require.Equal(t, "0:orange:0,0:apple:1,0:pineapple:2", annotation)
}
