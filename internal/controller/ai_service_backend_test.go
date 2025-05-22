// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestAIServiceBackendController_Reconcile(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	eventChan := internaltesting.NewControllerEventChan[*aigv1a1.AIGatewayRoute]()
	c := NewAIServiceBackendController(fakeClient, fake2.NewClientset(), ctrl.Log, eventChan.Ch)
	originals := []*aigv1a1.AIGatewayRoute{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute", Namespace: "default"},
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
						Matches:     []aigv1a1.AIGatewayRouteRuleMatch{{}},
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "mybackend"}},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "myroute2", Namespace: "default"},
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
						Matches:     []aigv1a1.AIGatewayRouteRuleMatch{{}},
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "mybackend"}},
					},
				},
			},
		},
	}
	for _, route := range originals {
		require.NoError(t, fakeClient.Create(t.Context(), route))
	}

	err := fakeClient.Create(t.Context(), &aigv1a1.AIServiceBackend{ObjectMeta: metav1.ObjectMeta{Name: "mybackend", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "mybackend"}})
	require.NoError(t, err)
	require.Equal(t, originals, eventChan.RequireItemsEventually(t, 2))

	// Check that the status was updated.
	var backend aigv1a1.AIServiceBackend
	require.NoError(t, fakeClient.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: "mybackend"}, &backend))
	require.Len(t, backend.Status.Conditions, 1)
	require.Equal(t, aigv1a1.ConditionTypeAccepted, backend.Status.Conditions[0].Type)
	require.Equal(t, "AIServiceBackend reconciled successfully", backend.Status.Conditions[0].Message)

	// Test the case where the AIServiceBackend is being deleted.
	err = fakeClient.Delete(t.Context(), &aigv1a1.AIServiceBackend{ObjectMeta: metav1.ObjectMeta{Name: "mybackend", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "mybackend"}})
	require.NoError(t, err)
}

func Test_AiServiceBackendIndexFunc(t *testing.T) {
	c := requireNewFakeClientWithIndexes(t)

	// Create Backend Security Policies.
	for _, bsp := range []*aigv1a1.BackendSecurityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-1", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-3", Namespace: "ns"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret-policy-3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				},
			},
		},
	} {
		require.NoError(t, c.Create(t.Context(), bsp, &client.CreateOptions{}))
	}

	// Create AI Service Backends.
	for _, backend := range []*aigv1a1.AIServiceBackend{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "one", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend1", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "two", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend2", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-1"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "three", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef:               gwapiv1.BackendObjectReference{Name: "some-backend3", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
				BackendSecurityPolicyRef: &gwapiv1.LocalObjectReference{Name: "some-backend-security-policy-3"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "four", Namespace: "ns"},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{Name: "some-backend4", Namespace: ptr.To[gwapiv1.Namespace]("ns")},
			},
		},
	} {
		require.NoError(t, c.Create(t.Context(), backend, &client.CreateOptions{}))
	}

	var aiServiceBackend aigv1a1.AIServiceBackendList
	require.NoError(t, c.List(t.Context(), &aiServiceBackend,
		client.MatchingFields{k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend: "some-backend-security-policy-1.ns"}))
	require.Len(t, aiServiceBackend.Items, 2)
	require.Equal(t, "one", aiServiceBackend.Items[0].Name)
	require.Equal(t, "two", aiServiceBackend.Items[1].Name)

	require.NoError(t, c.List(t.Context(), &aiServiceBackend,
		client.MatchingFields{k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend: "some-backend-security-policy-2.ns"}))
	require.Empty(t, aiServiceBackend.Items)

	require.NoError(t, c.List(t.Context(), &aiServiceBackend,
		client.MatchingFields{k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend: "some-backend-security-policy-3.ns"}))
	require.Len(t, aiServiceBackend.Items, 1)
	require.Equal(t, "three", aiServiceBackend.Items[0].Name)
}
