// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package client_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/envoyproxy/ai-gateway/api/v1alpha1"
	fakeclientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned/fake"
	informers "github.com/envoyproxy/ai-gateway/pkg/client/informers/externalversions"
)

func TestAIGatewayRouteInformer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := fakeclientset.NewSimpleClientset()
	factory := informers.NewSharedInformerFactory(client, 0)

	informer := factory.Aigateway().V1alpha1().AIGatewayRoutes()
	lister := informer.Lister()

	// Channel to track events
	addedCh := make(chan string, 10)
	updatedCh := make(chan string, 10)
	deletedCh := make(chan string, 10)

	informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			route := obj.(*v1alpha1.AIGatewayRoute)
			addedCh <- route.Name
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			route := newObj.(*v1alpha1.AIGatewayRoute)
			updatedCh <- route.Name
		},
		DeleteFunc: func(obj interface{}) {
			route := obj.(*v1alpha1.AIGatewayRoute)
			deletedCh <- route.Name
		},
	})

	// Start informers
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	t.Run("Informer handles Add events", func(t *testing.T) {
		route := &v1alpha1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-route-add",
				Namespace: "default",
			},
		}

		_, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
		require.NoError(t, err)

		select {
		case name := <-addedCh:
			assert.Equal(t, "test-route-add", name)
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for add event")
		}

		// Verify lister can retrieve the route
		fetched, err := lister.AIGatewayRoutes("default").Get("test-route-add")
		require.NoError(t, err)
		assert.Equal(t, "test-route-add", fetched.Name)
	})

	t.Run("Lister lists routes", func(t *testing.T) {
		// Create multiple routes
		for i := 0; i < 3; i++ {
			route := &v1alpha1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-route-list-" + string(rune('a'+i)),
					Namespace: "default",
				},
			}
			_, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
			require.NoError(t, err)
		}

		// Wait for events
		time.Sleep(100 * time.Millisecond)

		routes, err := lister.AIGatewayRoutes("default").List(labels.Everything())
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(routes), 3)
	})
}

func TestAIServiceBackendInformer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := fakeclientset.NewSimpleClientset()
	factory := informers.NewSharedInformerFactory(client, 0)

	informer := factory.Aigateway().V1alpha1().AIServiceBackends()
	lister := informer.Lister()

	// Start informers
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	t.Run("Informer and Lister work together", func(t *testing.T) {
		backend := &v1alpha1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-backend-informer",
				Namespace: "default",
			},
			Spec: v1alpha1.AIServiceBackendSpec{
				APISchema: v1alpha1.VersionedAPISchema{
					Name: v1alpha1.APISchemaOpenAI,
				},
				BackendRef: gwapiv1.BackendObjectReference{
					Name:  "test-service",
					Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
					Kind:  ptrTo(gwapiv1.Kind("Backend")),
				},
			},
		}

		_, err := client.AigatewayV1alpha1().AIServiceBackends("default").Create(ctx, backend, metav1.CreateOptions{})
		require.NoError(t, err)

		// Wait for informer to sync
		time.Sleep(100 * time.Millisecond)

		// Verify lister can retrieve the backend
		fetched, err := lister.AIServiceBackends("default").Get("test-backend-informer")
		require.NoError(t, err)
		assert.Equal(t, "test-backend-informer", fetched.Name)
		assert.Equal(t, v1alpha1.APISchemaOpenAI, fetched.Spec.APISchema.Name)
	})

	t.Run("Lister handles namespace scoping", func(t *testing.T) {
		// Create backend in different namespace
		backend1 := &v1alpha1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "backend-ns1",
				Namespace: "namespace1",
			},
			Spec: v1alpha1.AIServiceBackendSpec{
				APISchema: v1alpha1.VersionedAPISchema{
					Name: v1alpha1.APISchemaOpenAI,
				},
				BackendRef: gwapiv1.BackendObjectReference{
					Name:  "test-service",
					Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
					Kind:  ptrTo(gwapiv1.Kind("Backend")),
				},
			},
		}

		backend2 := &v1alpha1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "backend-ns2",
				Namespace: "namespace2",
			},
			Spec: v1alpha1.AIServiceBackendSpec{
				APISchema: v1alpha1.VersionedAPISchema{
					Name: v1alpha1.APISchemaOpenAI,
				},
				BackendRef: gwapiv1.BackendObjectReference{
					Name:  "test-service",
					Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
					Kind:  ptrTo(gwapiv1.Kind("Backend")),
				},
			},
		}

		_, err := client.AigatewayV1alpha1().AIServiceBackends("namespace1").Create(ctx, backend1, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = client.AigatewayV1alpha1().AIServiceBackends("namespace2").Create(ctx, backend2, metav1.CreateOptions{})
		require.NoError(t, err)

		// Wait for informer to sync
		time.Sleep(100 * time.Millisecond)

		// List backends in namespace1
		backends1, err := lister.AIServiceBackends("namespace1").List(labels.Everything())
		require.NoError(t, err)
		assert.Len(t, backends1, 1)
		assert.Equal(t, "backend-ns1", backends1[0].Name)

		// List backends in namespace2
		backends2, err := lister.AIServiceBackends("namespace2").List(labels.Everything())
		require.NoError(t, err)
		assert.Len(t, backends2, 1)
		assert.Equal(t, "backend-ns2", backends2[0].Name)
	})
}

func TestBackendSecurityPolicyInformer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := fakeclientset.NewSimpleClientset()
	factory := informers.NewSharedInformerFactory(client, 0)

	informer := factory.Aigateway().V1alpha1().BackendSecurityPolicies()
	lister := informer.Lister()

	// Start informers
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	t.Run("BackendSecurityPolicy informer works", func(t *testing.T) {
		policy := &v1alpha1.BackendSecurityPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-policy-informer",
				Namespace: "default",
			},
			Spec: v1alpha1.BackendSecurityPolicySpec{
				Type: v1alpha1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &v1alpha1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{
						Name: "api-key-secret",
					},
				},
			},
		}

		_, err := client.AigatewayV1alpha1().BackendSecurityPolicies("default").Create(ctx, policy, metav1.CreateOptions{})
		require.NoError(t, err)

		// Wait for informer to sync
		time.Sleep(100 * time.Millisecond)

		// Verify lister can retrieve the policy
		fetched, err := lister.BackendSecurityPolicies("default").Get("test-policy-informer")
		require.NoError(t, err)
		assert.Equal(t, "test-policy-informer", fetched.Name)
		assert.Equal(t, v1alpha1.BackendSecurityPolicyTypeAPIKey, fetched.Spec.Type)
	})
}

func TestMCPRouteInformer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := fakeclientset.NewSimpleClientset()
	factory := informers.NewSharedInformerFactory(client, 0)

	informer := factory.Aigateway().V1alpha1().MCPRoutes()
	lister := informer.Lister()

	// Start informers
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	t.Run("MCPRoute informer works", func(t *testing.T) {
		route := &v1alpha1.MCPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-mcp-informer",
				Namespace: "default",
			},
			Spec: v1alpha1.MCPRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{
						Name: "test-gateway",
					},
				},
				BackendRefs: []v1alpha1.MCPRouteBackendRef{
					{
						BackendObjectReference: gwapiv1.BackendObjectReference{
							Name: "mcp-server",
						},
					},
				},
			},
		}

		_, err := client.AigatewayV1alpha1().MCPRoutes("default").Create(ctx, route, metav1.CreateOptions{})
		require.NoError(t, err)

		// Wait for informer to sync
		time.Sleep(100 * time.Millisecond)

		// Verify lister can retrieve the route
		fetched, err := lister.MCPRoutes("default").Get("test-mcp-informer")
		require.NoError(t, err)
		assert.Equal(t, "test-mcp-informer", fetched.Name)
		assert.Len(t, fetched.Spec.BackendRefs, 1)
	})
}

func ptrTo[T any](v T) *T {
	return &v
}
