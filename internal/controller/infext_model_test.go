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
	ctrl "sigs.k8s.io/controller-runtime"
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestInferenceModelControllerReconcile(t *testing.T) {
	client := requireNewFakeClientWithIndexes(t)

	require.NoError(t, client.Create(t.Context(), &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "inference-pool",
			Namespace: "default",
		},
		Spec: gwaiev1a2.InferencePoolSpec{},
	}))
	require.NoError(t, client.Create(t.Context(), &gwaiev1a2.InferenceModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "inference-pool",
			Namespace: "default",
		},
		Spec: gwaiev1a2.InferenceModelSpec{PoolRef: gwaiev1a2.PoolObjectReference{
			Kind: "InferencePool",
			Name: "inference-pool",
		}},
	}))
	require.NoError(t, client.Create(t.Context(), &gwaiev1a2.InferenceModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ref-non-existing-pool",
			Namespace: "default",
		},
		Spec: gwaiev1a2.InferenceModelSpec{PoolRef: gwaiev1a2.PoolObjectReference{
			Kind: "InferencePool",
			Name: "non-existing-pool",
		}},
	}))

	syncFn := internaltesting.NewSyncFnImpl[gwaiev1a2.InferencePool]()
	infCtrl := newInferenceModelController(client, fake2.NewClientset(), ctrl.Log, syncFn.Sync)

	t.Run("ok", func(t *testing.T) {
		defer syncFn.Reset()
		_, err := infCtrl.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "inference-pool", Namespace: "default"}})
		require.NoError(t, err)
		actual := syncFn.GetItems()
		require.Len(t, actual, 1)
		require.Equal(t, "inference-pool", actual[0].Name)
	})
	t.Run("referencing pool not exist", func(t *testing.T) {
		defer syncFn.Reset()
		_, err := infCtrl.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "ref-non-existing-pool", Namespace: "default"}})
		require.NoError(t, err)
		actual := syncFn.GetItems()
		require.Empty(t, actual)
	})
	t.Run("inference model not exist", func(t *testing.T) {
		defer syncFn.Reset()
		_, err := infCtrl.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "inference-pool2", Namespace: "default"}})
		require.NoError(t, err)
		actual := syncFn.GetItems()
		require.Empty(t, actual)
	})
}
