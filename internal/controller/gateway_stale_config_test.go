// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	"sigs.k8s.io/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// TestGatewayController_StaleFilterConfigAfterAllRoutesDeleted demonstrates a bug where
// the filter config secret is NOT cleaned up when all AIGatewayRoutes are deleted.
//
// BUG LOCATION: gateway.go:114-118
//
// The bug is that when len(aiRoutes.Items) == 0 && len(mcpRoutes.Items) == 0,
// the controller returns early WITHOUT updating the filter config secret.
// This leaves stale backends/models in the ext-proc configuration.
//
// EXPECTED BEHAVIOR:
// When all routes are deleted, the filter config secret should be updated to
// have empty backends and models lists.
//
// ACTUAL BEHAVIOR (BUG):
// The filter config secret retains stale entries from deleted routes.
func TestGatewayController_StaleFilterConfigAfterAllRoutesDeleted(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapcore.DebugLevel})))
	c := NewGatewayController(fakeClient, fakeKube, ctrl.Log,
		"docker.io/envoyproxy/ai-gateway-extproc:latest", false, nil, true)

	const namespace = "test-ns"
	const gwName = "test-gateway"

	// Step 1: Create the Gateway
	gw := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: gwName, Namespace: namespace},
		Spec:       gwapiv1.GatewaySpec{},
	}
	require.NoError(t, fakeClient.Create(t.Context(), gw))

	// Step 2: Create AIGatewayRoute attached to the Gateway
	targets := []gwapiv1a2.ParentReference{
		{
			Name:  gwName,
			Kind:  ptr.To(gwapiv1a2.Kind("Gateway")),
			Group: ptr.To(gwapiv1a2.Group("gateway.networking.k8s.io")),
		},
	}
	route := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: namespace},
		Spec: aigv1a1.AIGatewayRouteSpec{
			ParentRefs: targets,
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "test-backend"}},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{
							Headers: []gwapiv1.HTTPHeaderMatch{
								{
									Name:  internalapi.ModelNameHeaderKeyDefault,
									Value: "test-model",
								},
							},
						},
					},
				},
			},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), route))

	// Step 3: Create the AIServiceBackend
	backend := &aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "test-backend", Namespace: namespace},
		Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{
				Name:      "some-service",
				Namespace: ptr.To[gwapiv1.Namespace](namespace),
			},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), backend))

	// Step 4: Create Gateway Pod and Deployment (required for reconciliation)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gw-pod",
			Namespace: namespace,
			Labels: map[string]string{
				egOwningGatewayNameLabel:      gwName,
				egOwningGatewayNamespaceLabel: namespace,
			},
		},
		Spec: corev1.PodSpec{},
	}
	_, err := fakeKube.CoreV1().Pods(namespace).Create(t.Context(), pod, metav1.CreateOptions{})
	require.NoError(t, err)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gw-deployment",
			Namespace: namespace,
			Labels: map[string]string{
				egOwningGatewayNameLabel:      gwName,
				egOwningGatewayNamespaceLabel: namespace,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{},
		},
	}
	_, err = fakeKube.AppsV1().Deployments(namespace).Create(t.Context(), deployment, metav1.CreateOptions{})
	require.NoError(t, err)

	// Step 5: Reconcile - this should create the filter config secret with backends/models
	res, err := c.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: gwName, Namespace: namespace},
	})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res)

	// Step 6: Verify filter config secret was created with backend and model
	secretName := FilterConfigSecretPerGatewayName(gwName, namespace)
	secret, err := fakeKube.CoreV1().Secrets(namespace).Get(t.Context(), secretName, metav1.GetOptions{})
	require.NoError(t, err)

	configStr, ok := secret.StringData[FilterConfigKeyInSecret]
	require.True(t, ok)

	var fc filterapi.Config
	require.NoError(t, yaml.Unmarshal([]byte(configStr), &fc))

	// Verify the filter config has the backend and model
	require.Len(t, fc.Backends, 1, "Should have 1 backend before deletion")
	require.Len(t, fc.Models, 1, "Should have 1 model before deletion")
	require.Equal(t, "test-model", fc.Models[0].Name)
	t.Logf("Before deletion - Backends: %d, Models: %d", len(fc.Backends), len(fc.Models))

	// Step 7: Delete the AIGatewayRoute
	require.NoError(t, fakeClient.Delete(t.Context(), route))

	// Step 8: Reconcile again - this is where the bug manifests
	res, err = c.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: gwName, Namespace: namespace},
	})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res)

	// Step 9: Read the filter config secret again
	secret, err = fakeKube.CoreV1().Secrets(namespace).Get(t.Context(), secretName, metav1.GetOptions{})
	require.NoError(t, err)

	configStr, ok = secret.StringData[FilterConfigKeyInSecret]
	require.True(t, ok)

	var fcAfterDeletion filterapi.Config
	require.NoError(t, yaml.Unmarshal([]byte(configStr), &fcAfterDeletion))

	t.Logf("After deletion - Backends: %d, Models: %d", len(fcAfterDeletion.Backends), len(fcAfterDeletion.Models))

	// ASSERTION: This is what SHOULD happen (but currently doesn't due to the bug)
	// The filter config should be empty after all routes are deleted
	//
	// CURRENT BUG: The controller returns early when no routes exist,
	// leaving the stale config in the secret.
	require.Empty(t, fcAfterDeletion.Backends,
		"BUG: Filter config should have 0 backends after all routes are deleted, but has %d. "+
			"The stale config in ext-proc can cause routing to non-existent backends.",
		len(fcAfterDeletion.Backends))
	require.Empty(t, fcAfterDeletion.Models,
		"BUG: Filter config should have 0 models after all routes are deleted, but has %d. "+
			"The stale config in ext-proc lists models that no longer exist.",
		len(fcAfterDeletion.Models))
}

// TestGatewayController_PartialRouteDeletion verifies that when SOME routes are deleted
// (but not all), the filter config is correctly updated to only contain the remaining routes.
func TestGatewayController_PartialRouteDeletion(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapcore.DebugLevel})))
	c := NewGatewayController(fakeClient, fakeKube, ctrl.Log,
		"docker.io/envoyproxy/ai-gateway-extproc:latest", false, nil, true)

	const namespace = "test-ns"
	const gwName = "test-gateway"

	// Create Gateway
	gw := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: gwName, Namespace: namespace},
		Spec:       gwapiv1.GatewaySpec{},
	}
	require.NoError(t, fakeClient.Create(t.Context(), gw))

	// Create two AIGatewayRoutes
	targets := []gwapiv1a2.ParentReference{
		{
			Name:  gwName,
			Kind:  ptr.To(gwapiv1a2.Kind("Gateway")),
			Group: ptr.To(gwapiv1a2.Group("gateway.networking.k8s.io")),
		},
	}

	route1 := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route-1", Namespace: namespace},
		Spec: aigv1a1.AIGatewayRouteSpec{
			ParentRefs: targets,
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "backend-1"}},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: internalapi.ModelNameHeaderKeyDefault, Value: "model-1"}}},
					},
				},
			},
		},
	}
	route2 := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route-2", Namespace: namespace},
		Spec: aigv1a1.AIGatewayRouteSpec{
			ParentRefs: targets,
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "backend-2"}},
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{
						{Headers: []gwapiv1.HTTPHeaderMatch{{Name: internalapi.ModelNameHeaderKeyDefault, Value: "model-2"}}},
					},
				},
			},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), route1))
	require.NoError(t, fakeClient.Create(t.Context(), route2))

	// Create backends
	for _, name := range []string{"backend-1", "backend-2"} {
		backend := &aigv1a1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: aigv1a1.AIServiceBackendSpec{
				BackendRef: gwapiv1.BackendObjectReference{
					Name:      gwapiv1.ObjectName(name + "-svc"),
					Namespace: ptr.To[gwapiv1.Namespace](namespace),
				},
			},
		}
		require.NoError(t, fakeClient.Create(t.Context(), backend))
	}

	// Create Gateway resources
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gw-pod", Namespace: namespace,
			Labels: map[string]string{
				egOwningGatewayNameLabel:      gwName,
				egOwningGatewayNamespaceLabel: namespace,
			},
		},
		Spec: corev1.PodSpec{},
	}
	_, err := fakeKube.CoreV1().Pods(namespace).Create(t.Context(), pod, metav1.CreateOptions{})
	require.NoError(t, err)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gw-deployment", Namespace: namespace,
			Labels: map[string]string{
				egOwningGatewayNameLabel:      gwName,
				egOwningGatewayNamespaceLabel: namespace,
			},
		},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{}},
	}
	_, err = fakeKube.AppsV1().Deployments(namespace).Create(t.Context(), deployment, metav1.CreateOptions{})
	require.NoError(t, err)

	// Initial reconcile
	res, err := c.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: gwName, Namespace: namespace},
	})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res)

	// Verify initial state has 2 backends and 2 models
	secretName := FilterConfigSecretPerGatewayName(gwName, namespace)
	secret, err := fakeKube.CoreV1().Secrets(namespace).Get(t.Context(), secretName, metav1.GetOptions{})
	require.NoError(t, err)

	var fc filterapi.Config
	require.NoError(t, yaml.Unmarshal([]byte(secret.StringData[FilterConfigKeyInSecret]), &fc))
	require.Len(t, fc.Backends, 2, "Should have 2 backends before deletion")
	require.Len(t, fc.Models, 2, "Should have 2 models before deletion")

	// Delete route-1
	require.NoError(t, fakeClient.Delete(t.Context(), route1))

	// Reconcile after partial deletion
	res, err = c.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: gwName, Namespace: namespace},
	})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res)

	// Verify only route-2's backend and model remain
	secret, err = fakeKube.CoreV1().Secrets(namespace).Get(t.Context(), secretName, metav1.GetOptions{})
	require.NoError(t, err)

	var fcAfter filterapi.Config
	require.NoError(t, yaml.Unmarshal([]byte(secret.StringData[FilterConfigKeyInSecret]), &fcAfter))

	require.Len(t, fcAfter.Backends, 1, "Should have 1 backend after partial deletion")
	require.Len(t, fcAfter.Models, 1, "Should have 1 model after partial deletion")
	require.Equal(t, "model-2", fcAfter.Models[0].Name, "Remaining model should be model-2")
}

// TestGatewayController_RouteRecreationAfterDeletion verifies that when a route is deleted
// and then recreated with the same name, the filter config is correctly updated.
func TestGatewayController_RouteRecreationAfterDeletion(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapcore.DebugLevel})))
	c := NewGatewayController(fakeClient, fakeKube, ctrl.Log,
		"docker.io/envoyproxy/ai-gateway-extproc:latest", false, nil, true)

	const namespace = "test-ns"
	const gwName = "test-gateway"

	// Create Gateway
	gw := &gwapiv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: gwName, Namespace: namespace},
		Spec:       gwapiv1.GatewaySpec{},
	}
	require.NoError(t, fakeClient.Create(t.Context(), gw))

	targets := []gwapiv1a2.ParentReference{
		{
			Name:  gwName,
			Kind:  ptr.To(gwapiv1a2.Kind("Gateway")),
			Group: ptr.To(gwapiv1a2.Group("gateway.networking.k8s.io")),
		},
	}

	createRoute := func(modelName string) *aigv1a1.AIGatewayRoute {
		return &aigv1a1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: namespace},
			Spec: aigv1a1.AIGatewayRouteSpec{
				ParentRefs: targets,
				Rules: []aigv1a1.AIGatewayRouteRule{
					{
						BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "test-backend"}},
						Matches: []aigv1a1.AIGatewayRouteRuleMatch{
							{Headers: []gwapiv1.HTTPHeaderMatch{{Name: internalapi.ModelNameHeaderKeyDefault, Value: modelName}}},
						},
					},
				},
			},
		}
	}

	// Create initial route with model-v1
	route := createRoute("model-v1")
	require.NoError(t, fakeClient.Create(t.Context(), route))

	// Create backend
	backend := &aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "test-backend", Namespace: namespace},
		Spec: aigv1a1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{
				Name:      "test-svc",
				Namespace: ptr.To[gwapiv1.Namespace](namespace),
			},
		},
	}
	require.NoError(t, fakeClient.Create(t.Context(), backend))

	// Create Gateway resources
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gw-pod", Namespace: namespace,
			Labels: map[string]string{
				egOwningGatewayNameLabel:      gwName,
				egOwningGatewayNamespaceLabel: namespace,
			},
		},
		Spec: corev1.PodSpec{},
	}
	_, err := fakeKube.CoreV1().Pods(namespace).Create(t.Context(), pod, metav1.CreateOptions{})
	require.NoError(t, err)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gw-deployment", Namespace: namespace,
			Labels: map[string]string{
				egOwningGatewayNameLabel:      gwName,
				egOwningGatewayNamespaceLabel: namespace,
			},
		},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{}},
	}
	_, err = fakeKube.AppsV1().Deployments(namespace).Create(t.Context(), deployment, metav1.CreateOptions{})
	require.NoError(t, err)

	// Initial reconcile
	res, err := c.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: gwName, Namespace: namespace},
	})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res)

	// Verify initial model is model-v1
	secretName := FilterConfigSecretPerGatewayName(gwName, namespace)
	secret, err := fakeKube.CoreV1().Secrets(namespace).Get(t.Context(), secretName, metav1.GetOptions{})
	require.NoError(t, err)

	var fc filterapi.Config
	require.NoError(t, yaml.Unmarshal([]byte(secret.StringData[FilterConfigKeyInSecret]), &fc))
	require.Len(t, fc.Models, 1)
	require.Equal(t, "model-v1", fc.Models[0].Name)

	// Delete the route
	require.NoError(t, fakeClient.Delete(t.Context(), route))

	// Reconcile after deletion (this triggers the bug if all routes deleted)
	res, err = c.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: gwName, Namespace: namespace},
	})
	require.NoError(t, err)

	// Wait a bit to simulate real-world delay
	time.Sleep(10 * time.Millisecond)

	// Recreate route with different model
	routeV2 := createRoute("model-v2")
	require.NoError(t, fakeClient.Create(t.Context(), routeV2))

	// Reconcile after recreation
	res, err = c.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: gwName, Namespace: namespace},
	})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res)

	// Verify the model is updated to model-v2
	secret, err = fakeKube.CoreV1().Secrets(namespace).Get(t.Context(), secretName, metav1.GetOptions{})
	require.NoError(t, err)

	var fcAfter filterapi.Config
	require.NoError(t, yaml.Unmarshal([]byte(secret.StringData[FilterConfigKeyInSecret]), &fcAfter))

	require.Len(t, fcAfter.Models, 1, "Should have 1 model after recreation")
	require.Equal(t, "model-v2", fcAfter.Models[0].Name,
		"Model should be updated to model-v2, but stale config might show model-v1")
}
