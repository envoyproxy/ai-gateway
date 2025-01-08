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

func TestLlmBackendController_Reconcile(t *testing.T) {
	ch := make(chan configSinkEvent, 100)
	c := newLLMBackendController(fake.NewClientBuilder().WithScheme(scheme).Build(), fake2.NewClientset(), ctrl.Log, ch)

	// Deleted case.
	_, err := c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "mybackend"}})
	require.NoError(t, err)
	item, ok := <-ch
	require.True(t, ok)
	require.IsType(t, configSinkEventLLMBackendDeleted{}, item)
	require.Equal(t, "default", item.(configSinkEventLLMBackendDeleted).namespace)
	require.Equal(t, "mybackend", item.(configSinkEventLLMBackendDeleted).name)

	// Updated case.
	err = c.client.Create(context.Background(), &aigv1a1.LLMBackend{ObjectMeta: metav1.ObjectMeta{Name: "mybackend", Namespace: "default"}})
	require.NoError(t, err)
	_, err = c.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "mybackend"}})
	require.NoError(t, err)
	item, ok = <-ch
	require.True(t, ok)
	require.IsType(t, &aigv1a1.LLMBackend{}, item)
	require.Equal(t, "mybackend", item.(*aigv1a1.LLMBackend).Name)
	require.Equal(t, "default", item.(*aigv1a1.LLMBackend).Namespace)
}
