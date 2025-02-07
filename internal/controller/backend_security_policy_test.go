package controller

import (
	"context"
	"fmt"
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
	c := newBackendSecurityPolicyController(cl, fake2.NewClientset(), ctrl.Log, ch)
	backendSecurityPolicyName := "mybackendSecurityPolicy"
	namespace := "default"

	err := cl.Create(context.Background(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: backendSecurityPolicyName, Namespace: namespace}})
	require.NoError(t, err)
	err = cl.Create(context.Background(), &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName), Namespace: namespace},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
			AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
				Region:            "us-east-1",
				OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{},
			},
		},
	})
	require.NoError(t, err)
	_, err = c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}})
	require.NoError(t, err)
	item, ok := <-ch
	require.True(t, ok)
	require.IsType(t, &aigv1a1.BackendSecurityPolicy{}, item)
	require.Equal(t, backendSecurityPolicyName, item.(*aigv1a1.BackendSecurityPolicy).Name)
	require.Equal(t, namespace, item.(*aigv1a1.BackendSecurityPolicy).Namespace)

	// Test backendSecurityPolicy with OIDC credentials have the annotation added
	oidcBackendSecurityPolicy := &aigv1a1.BackendSecurityPolicy{}
	err = cl.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: fmt.Sprintf("%s-OIDC", backendSecurityPolicyName)}, oidcBackendSecurityPolicy)
	require.NoError(t, err)
	require.Len(t, oidcBackendSecurityPolicy.Annotations, 1)
	time, ok := oidcBackendSecurityPolicy.Annotations["refresh"]
	require.True(t, ok)
	require.NotEmpty(t, time)

	// Test the case where the BackendSecurityPolicy is being deleted.
	err = cl.Delete(context.Background(), &aigv1a1.BackendSecurityPolicy{ObjectMeta: metav1.ObjectMeta{Name: backendSecurityPolicyName, Namespace: namespace}})
	require.NoError(t, err)
	_, err = c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: backendSecurityPolicyName}})
	require.NoError(t, err)
}
