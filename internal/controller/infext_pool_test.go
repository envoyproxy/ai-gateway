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
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestInferencePoolControllerReconcile(t *testing.T) {
	client := requireNewFakeClientWithIndexes(t)
	require.NoError(t, client.Create(t.Context(), &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "default",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget2"}},
			},
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{},
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend2", Weight: 1},
						{Name: "inference-pool", Weight: 1, Kind: ptr.To(aigv1a1.AIGatewayRouteRuleBackendRefInferencePool)},
					},
				},
			},
		},
	}))

	require.NoError(t, client.Create(t.Context(), &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "inference-pool",
			Namespace: "default",
		},
		Spec: gwaiev1a2.InferencePoolSpec{
			EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
				ExtensionRef: &gwaiev1a2.Extension{
					ExtensionReference: gwaiev1a2.ExtensionReference{Name: "envoy-ai-gateway"},
				},
			},
		},
	}))

	syncFn := internaltesting.NewSyncFnImpl[aigv1a1.AIGatewayRoute]()
	infCtrl := newInferencePoolController(client, fake2.NewClientset(), ctrl.Log, syncFn.Sync)

	t.Run("exist", func(t *testing.T) {
		defer syncFn.Reset()
		_, err := infCtrl.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "inference-pool", Namespace: "default"}})
		require.NoError(t, err)
		actual := syncFn.GetItems()
		require.Len(t, actual, 1)
		require.Equal(t, "myroute", actual[0].Name)
	})
	t.Run("not found", func(t *testing.T) {
		defer syncFn.Reset()
		_, err := infCtrl.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "not-found", Namespace: "default"}})
		require.NoError(t, err)
		actual := syncFn.GetItems()
		require.Empty(t, actual)
	})
}
