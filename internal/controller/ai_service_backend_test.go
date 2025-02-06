package controller

import (
	"context"
	"testing"

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

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestAIServiceBackendController_Reconcile(t *testing.T) {
	ch := make(chan ConfigSinkEvent, 100)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewAIServiceBackendController(cl, fake2.NewClientset(), ctrl.Log, ch)

	err := cl.Create(context.Background(), &aigv1a1.AIServiceBackend{ObjectMeta: metav1.ObjectMeta{Name: "mybackend", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "mybackend"}})
	require.NoError(t, err)
	item, ok := <-ch
	require.True(t, ok)
	require.IsType(t, &aigv1a1.AIServiceBackend{}, item)
	require.Equal(t, "mybackend", item.(*aigv1a1.AIServiceBackend).Name)
	require.Equal(t, "default", item.(*aigv1a1.AIServiceBackend).Namespace)

	// Test the case where the AIServiceBackend is being deleted.
	err = cl.Delete(context.Background(), &aigv1a1.AIServiceBackend{ObjectMeta: metav1.ObjectMeta{Name: "mybackend", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "mybackend"}})
	require.NoError(t, err)
}

func Test_AiServiceBackendIndexFunc(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&aigv1a1.AIServiceBackend{}, k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend, aiServiceBackendIndexFunc).
		Build()

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
		require.NoError(t, c.Create(context.Background(), bsp, &client.CreateOptions{}))
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
		require.NoError(t, c.Create(context.Background(), backend, &client.CreateOptions{}))
	}

	var aiServiceBackend aigv1a1.AIServiceBackendList
	require.NoError(t, c.List(context.Background(), &aiServiceBackend,
		client.MatchingFields{k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend: "some-backend-security-policy-1.ns"}))
	require.Len(t, aiServiceBackend.Items, 2)
	require.Equal(t, "one", aiServiceBackend.Items[0].Name)
	require.Equal(t, "two", aiServiceBackend.Items[1].Name)

	require.NoError(t, c.List(context.Background(), &aiServiceBackend,
		client.MatchingFields{k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend: "some-backend-security-policy-2.ns"}))
	require.Empty(t, aiServiceBackend.Items)

	require.NoError(t, c.List(context.Background(), &aiServiceBackend,
		client.MatchingFields{k8sClientIndexBackendSecurityPolicyToReferencingAIServiceBackend: "some-backend-security-policy-3.ns"}))
	require.Len(t, aiServiceBackend.Items, 1)
	require.Equal(t, "three", aiServiceBackend.Items[0].Name)
}
