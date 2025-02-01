package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fake2 "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestBackendSecurityController_Reconcile(t *testing.T) {
	ch := make(chan ConfigSinkEvent, 100)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	tokenManager := NewTokenManager(ctrl.Log.WithName("token-manager"))
	c := newBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, ch, tokenManager)
	backendSecurityPolicyName := "mybackendSecurityPolicy"
	namespace := "default"

	err := cl.Create(context.Background(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: backendSecurityPolicyName, Namespace: namespace}})
	require.NoError(t, err)
	_, err = c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}})
	require.NoError(t, err)
	item, ok := <-ch
	require.True(t, ok)
	require.IsType(t, &aigv1a1.BackendSecurityPolicy{}, item)
	require.Equal(t, backendSecurityPolicyName, item.(*aigv1a1.BackendSecurityPolicy).Name)
	require.Equal(t, namespace, item.(*aigv1a1.BackendSecurityPolicy).Namespace)

	// Test the case where the BackendSecurityPolicy is being deleted.
	err = cl.Delete(context.Background(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: backendSecurityPolicyName, Namespace: namespace}})
	require.NoError(t, err)
	_, err = c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}})
	require.NoError(t, err)
}
