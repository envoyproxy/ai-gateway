// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestSecretController_Reconcile(t *testing.T) {
	ch := make(chan ConfigSinkEvent, 100)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewSecretController(cl, fake2.NewClientset(), ctrl.Log, ch)

	err := cl.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mysecret", Namespace: "default"},
		StringData: map[string]string{"key": "value"},
	})
	require.NoError(t, err)

	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: "default", Name: "mysecret",
	}})
	require.NoError(t, err)

	item, ok := <-ch
	require.True(t, ok)
	require.Equal(t, ConfigSinkEventSecretUpdate{Namespace: "default", Name: "mysecret"}, item)

	// Test the case where the Secret is being deleted.
	err = cl.Delete(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mysecret", Namespace: "default"},
	})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: "default", Name: "mysecret",
	}})
	require.NoError(t, err)
}
