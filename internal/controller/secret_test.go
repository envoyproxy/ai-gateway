// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestSecretController_Reconcile(t *testing.T) {
	eventCh := internaltesting.NewControllerEventChan[*aigv1a1.BackendSecurityPolicy]()
	fakeClient := requireNewFakeClientWithIndexes(t)
	c := NewSecretController(fakeClient, fake2.NewClientset(), ctrl.Log, eventCh.Ch)

	err := fakeClient.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mysecret", Namespace: "default"},
		StringData: map[string]string{"key": "value"},
	})
	require.NoError(t, err)

	// Create a bsp that references the secret.
	originals := []*aigv1a1.BackendSecurityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type:   aigv1a1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{SecretRef: &gwapiv1.SecretObjectReference{Name: "mysecret"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "bar", Namespace: "default"},
			Spec: aigv1a1.BackendSecurityPolicySpec{
				Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
				AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
					Region:          "us-west-2",
					CredentialsFile: &aigv1a1.AWSCredentialsFile{SecretRef: &gwapiv1.SecretObjectReference{Name: "mysecret"}},
				},
			},
		},
	}
	for _, bsp := range originals {
		require.NoError(t, fakeClient.Create(t.Context(), bsp))
	}

	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: "default", Name: "mysecret",
	}})
	require.NoError(t, err)
	actual := eventCh.RequireItemsEventually(t, len(originals))
	sort.Slice(actual, func(i, j int) bool {
		return actual[i].Name < actual[j].Name
	})
	sort.Slice(originals, func(i, j int) bool {
		return originals[i].Name < originals[j].Name
	})
	require.Equal(t, originals, actual)

	// Test the case where the Secret is being deleted.
	err = fakeClient.Delete(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mysecret", Namespace: "default"},
	})
	require.NoError(t, err)
	_, err = c.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{
		Namespace: "default", Name: "mysecret",
	}})
	require.NoError(t, err)
}
