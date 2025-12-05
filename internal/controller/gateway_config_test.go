// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

// requireNewFakeClientForGatewayConfig creates a fake client for GatewayConfig tests.
func requireNewFakeClientForGatewayConfig(t *testing.T) client.Client {
	t.Helper()
	builder := fake.NewClientBuilder().WithScheme(Scheme).
		WithStatusSubresource(&aigv1a1.GatewayConfig{})
	return builder.Build()
}

func TestGatewayConfigController_Reconcile(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	// Create a GatewayConfig.
	gatewayConfig := &aigv1a1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "default",
		},
		Spec: aigv1a1.GatewayConfigSpec{
			ExtProc: &aigv1a1.GatewayConfigExtProc{
				Env: []corev1.EnvVar{
					{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://otel-collector:4317"},
				},
				Resources: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
				},
			},
		},
	}
	err := fakeClient.Create(t.Context(), gatewayConfig)
	require.NoError(t, err)

	// Reconcile - should succeed with no referencing Gateways.
	result, err := c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, result)

	// Verify status was updated to Accepted.
	var updated aigv1a1.GatewayConfig
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-config", Namespace: "default"}, &updated)
	require.NoError(t, err)
	require.Len(t, updated.Status.Conditions, 1)
	require.Equal(t, aigv1a1.ConditionTypeAccepted, updated.Status.Conditions[0].Type)
}

func TestGatewayConfigController_FinalizerManagement(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	// Create a GatewayConfig.
	gatewayConfig := &aigv1a1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "default",
		},
		Spec: aigv1a1.GatewayConfigSpec{
			ExtProc: &aigv1a1.GatewayConfigExtProc{
				Env: []corev1.EnvVar{
					{Name: "LOG_LEVEL", Value: "debug"},
				},
			},
		},
	}
	err := fakeClient.Create(t.Context(), gatewayConfig)
	require.NoError(t, err)

	// Reconcile without any referencing Gateway - should not add finalizer.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.NoError(t, err)

	var updatedConfig aigv1a1.GatewayConfig
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-config", Namespace: "default"}, &updatedConfig)
	require.NoError(t, err)
	require.NotContains(t, updatedConfig.Finalizers, GatewayConfigFinalizerName)

	// Create a Gateway that references the GatewayConfig.
	gateway := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gateway",
			Namespace: "default",
			Annotations: map[string]string{
				GatewayConfigAnnotationKey: "test-config",
			},
		},
		Spec: gwapiv1.GatewaySpec{
			GatewayClassName: "test-class",
			Listeners: []gwapiv1.Listener{
				{
					Name:     "http",
					Port:     8080,
					Protocol: gwapiv1.HTTPProtocolType,
				},
			},
		},
	}
	err = fakeClient.Create(t.Context(), gateway)
	require.NoError(t, err)

	// Reconcile again - should add finalizer now.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.NoError(t, err)

	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-config", Namespace: "default"}, &updatedConfig)
	require.NoError(t, err)
	require.Contains(t, updatedConfig.Finalizers, GatewayConfigFinalizerName)

	// Gateway event should be sent.
	events := eventCh.RequireItemsEventually(t, 1)
	require.Len(t, events, 1)

	// Delete the Gateway reference by updating it.
	gateway.Annotations = nil
	err = fakeClient.Update(t.Context(), gateway)
	require.NoError(t, err)

	// Reconcile - should remove finalizer.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.NoError(t, err)

	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-config", Namespace: "default"}, &updatedConfig)
	require.NoError(t, err)
	require.NotContains(t, updatedConfig.Finalizers, GatewayConfigFinalizerName)
}

func TestGatewayConfigController_MapGatewayToGatewayConfig(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	t.Run("Gateway with GatewayConfig annotation", func(t *testing.T) {
		gateway := &gwapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "default",
				Annotations: map[string]string{
					GatewayConfigAnnotationKey: "my-config",
				},
			},
		}

		requests := c.MapGatewayToGatewayConfig(context.Background(), gateway)
		require.Len(t, requests, 1)
		require.Equal(t, "my-config", requests[0].Name)
		require.Equal(t, "default", requests[0].Namespace)
	})

	t.Run("Gateway without annotation", func(t *testing.T) {
		gateway := &gwapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "default",
			},
		}

		requests := c.MapGatewayToGatewayConfig(context.Background(), gateway)
		require.Empty(t, requests)
	})

	t.Run("Gateway with empty annotation", func(t *testing.T) {
		gateway := &gwapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-gateway",
				Namespace: "default",
				Annotations: map[string]string{
					GatewayConfigAnnotationKey: "",
				},
			},
		}

		requests := c.MapGatewayToGatewayConfig(context.Background(), gateway)
		require.Empty(t, requests)
	})
}

func TestGatewayConfigController_MultipleGatewaysReferencing(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	// Create a GatewayConfig.
	gatewayConfig := &aigv1a1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-config",
			Namespace: "default",
		},
		Spec: aigv1a1.GatewayConfigSpec{
			ExtProc: &aigv1a1.GatewayConfigExtProc{
				Env: []corev1.EnvVar{
					{Name: "SHARED_VAR", Value: "shared-value"},
				},
			},
		},
	}
	err := fakeClient.Create(t.Context(), gatewayConfig)
	require.NoError(t, err)

	// Create two Gateways that reference the same GatewayConfig.
	for _, name := range []string{"gateway-1", "gateway-2"} {
		gateway := &gwapiv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
				Annotations: map[string]string{
					GatewayConfigAnnotationKey: "shared-config",
				},
			},
			Spec: gwapiv1.GatewaySpec{
				GatewayClassName: "test-class",
				Listeners: []gwapiv1.Listener{
					{
						Name:     "http",
						Port:     8080,
						Protocol: gwapiv1.HTTPProtocolType,
					},
				},
			},
		}
		err = fakeClient.Create(t.Context(), gateway)
		require.NoError(t, err)
	}

	// Reconcile - should add finalizer and notify both gateways.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "shared-config", Namespace: "default"},
	})
	require.NoError(t, err)

	var updatedConfig aigv1a1.GatewayConfig
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "shared-config", Namespace: "default"}, &updatedConfig)
	require.NoError(t, err)
	require.Contains(t, updatedConfig.Finalizers, GatewayConfigFinalizerName)

	// Both Gateways should have been notified.
	events := eventCh.RequireItemsEventually(t, 2)
	require.Len(t, events, 2)
}

func TestGatewayConfigController_DeletionBlocked(t *testing.T) {
	fakeClient := requireNewFakeClientForGatewayConfig(t)
	eventCh := internaltesting.NewControllerEventChan[*gwapiv1.Gateway]()
	c := NewGatewayConfigController(fakeClient, ctrl.Log, eventCh.Ch)

	// Create a GatewayConfig first.
	gatewayConfig := &aigv1a1.GatewayConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-config",
			Namespace:  "default",
			Finalizers: []string{GatewayConfigFinalizerName},
		},
		Spec: aigv1a1.GatewayConfigSpec{},
	}
	err := fakeClient.Create(t.Context(), gatewayConfig)
	require.NoError(t, err)

	// Create a Gateway that references the GatewayConfig.
	gateway := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gateway",
			Namespace: "default",
			Annotations: map[string]string{
				GatewayConfigAnnotationKey: "test-config",
			},
		},
		Spec: gwapiv1.GatewaySpec{
			GatewayClassName: "test-class",
			Listeners: []gwapiv1.Listener{
				{
					Name:     "http",
					Port:     8080,
					Protocol: gwapiv1.HTTPProtocolType,
				},
			},
		},
	}
	err = fakeClient.Create(t.Context(), gateway)
	require.NoError(t, err)

	// Reconcile to verify the GatewayConfig is accepted since it has references.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.NoError(t, err)

	// Get the GatewayConfig to verify finalizer exists.
	var updatedConfig aigv1a1.GatewayConfig
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-config", Namespace: "default"}, &updatedConfig)
	require.NoError(t, err)
	require.Contains(t, updatedConfig.Finalizers, GatewayConfigFinalizerName)

	// Try to delete the GatewayConfig (simulate via Delete API).
	// With finalizer, it won't actually be deleted but will have DeletionTimestamp set.
	err = fakeClient.Delete(t.Context(), &updatedConfig)
	require.NoError(t, err)

	// Get the GatewayConfig again - it should still exist with DeletionTimestamp.
	err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "test-config", Namespace: "default"}, &updatedConfig)
	require.NoError(t, err)
	require.NotNil(t, updatedConfig.DeletionTimestamp)

	// Reconcile again - should fail because GatewayConfig is still referenced.
	_, err = c.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: "test-config", Namespace: "default"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "still referenced")
}
